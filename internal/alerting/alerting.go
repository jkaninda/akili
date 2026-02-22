// Package alerting implements the alert rule scheduler for Akili.
package alerting

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/security"
)

// AlertRuleStore is the persistence interface for alert rules.
type AlertRuleStore interface {
	Create(ctx context.Context, rule *domain.AlertRule) error
	Get(ctx context.Context, orgID, id uuid.UUID) (*domain.AlertRule, error)
	List(ctx context.Context, orgID uuid.UUID) ([]domain.AlertRule, error)
	Update(ctx context.Context, rule *domain.AlertRule) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	GetDueAlerts(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.AlertRule, error)
	RecordCheck(ctx context.Context, id uuid.UUID, nextRunAt time.Time, status, errMsg string) error
	RecordAlerted(ctx context.Context, id uuid.UUID) error
}

// AlertHistoryStore is the persistence interface for alert history.
type AlertHistoryStore interface {
	Append(ctx context.Context, h *domain.AlertHistory) error
	ListByRule(ctx context.Context, orgID, ruleID uuid.UUID, limit int) ([]domain.AlertHistory, error)
	ListRecent(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.AlertHistory, error)
}

// StoreFactory creates an AlertRuleStore from a *gorm.DB (for transactions).
type StoreFactory func(db *gorm.DB) AlertRuleStore

// CheckerConfig configures the alert checker.
type CheckerConfig struct {
	PollIntervalSeconds int // Default: 30.
	MaxConcurrentChecks int // Default: 10.
}

func (c *CheckerConfig) pollInterval() time.Duration {
	if c != nil && c.PollIntervalSeconds > 0 {
		return time.Duration(c.PollIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

func (c *CheckerConfig) maxConcurrent() int {
	if c != nil && c.MaxConcurrentChecks > 0 {
		return c.MaxConcurrentChecks
	}
	return 10
}

// Checker polls for due alert rules and evaluates them.
type Checker struct {
	store        AlertRuleStore
	historyStore AlertHistoryStore
	storeFactory StoreFactory
	db           *gorm.DB
	dispatcher   *notification.Dispatcher
	security     security.SecurityManager
	metrics      *Metrics
	logger       *slog.Logger
	config       *CheckerConfig
	orgID        uuid.UUID
	parser       cron.Parser
}

// NewChecker creates an alert Checker.
func NewChecker(
	store AlertRuleStore,
	historyStore AlertHistoryStore,
	storeFactory StoreFactory,
	db *gorm.DB,
	dispatcher *notification.Dispatcher,
	sec security.SecurityManager,
	metrics *Metrics,
	logger *slog.Logger,
	cfg *CheckerConfig,
	orgID uuid.UUID,
) *Checker {
	return &Checker{
		store:        store,
		historyStore: historyStore,
		storeFactory: storeFactory,
		db:           db,
		dispatcher:   dispatcher,
		security:     sec,
		metrics:      metrics,
		logger:       logger,
		config:       cfg,
		orgID:        orgID,
		parser:       cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// Start begins the alert checker loop. Returns a cancel function.
func (c *Checker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		c.logger.InfoContext(ctx, "alert checker started",
			slog.String("poll_interval", c.config.pollInterval().String()),
			slog.Int("max_concurrent", c.config.maxConcurrent()),
		)

		ticker := time.NewTicker(c.config.pollInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				c.logger.Info("alert checker stopped")
				return
			case <-ticker.C:
				c.tick(ctx)
			}
		}
	}()

	return cancel
}

// tick runs a single poll cycle: find due alerts, evaluate, notify.
func (c *Checker) tick(ctx context.Context) {
	start := time.Now()
	now := start.UTC()

	err := c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := c.storeFactory(tx)

		dueAlerts, err := txStore.GetDueAlerts(ctx, c.orgID, now)
		if err != nil {
			return fmt.Errorf("polling due alerts: %w", err)
		}

		if len(dueAlerts) == 0 {
			return nil
		}

		c.logger.InfoContext(ctx, "alert rules due",
			slog.Int("count", len(dueAlerts)),
		)

		sem := make(chan struct{}, c.config.maxConcurrent())
		var wg sync.WaitGroup

		for i := range dueAlerts {
			rule := dueAlerts[i]
			sem <- struct{}{}
			wg.Add(1)

			go func(r domain.AlertRule) {
				defer wg.Done()
				defer func() { <-sem }()
				c.evaluateRule(ctx, txStore, &r)
			}(rule)
		}

		wg.Wait()
		return nil
	})

	if err != nil {
		c.logger.ErrorContext(ctx, "alert checker tick failed",
			slog.String("error", err.Error()),
		)
	}

	if c.metrics != nil {
		c.metrics.CheckDuration.Observe(time.Since(start).Seconds())
	}
}

// evaluateRule runs a single check, dispatches notifications, records history.
func (c *Checker) evaluateRule(ctx context.Context, store AlertRuleStore, rule *domain.AlertRule) {
	correlationID := newCorrelationID()
	checkStart := time.Now()

	c.logger.InfoContext(ctx, "evaluating alert rule",
		slog.String("rule_id", rule.ID.String()),
		slog.String("name", rule.Name),
		slog.String("check_type", rule.CheckType),
		slog.String("correlation_id", correlationID),
	)

	if c.metrics != nil {
		c.metrics.ChecksRun.Inc()
	}

	// Audit: log check intent.
	c.auditLog(ctx, correlationID, rule.UserID, "alert.check", rule, "intent", "")

	// Run the check.
	status, message, checkErr := RunCheck(ctx, rule.CheckType, rule.CheckConfig)
	checkDuration := time.Since(checkStart).Milliseconds()

	if checkErr != nil {
		status = "error"
		message = checkErr.Error()
	}

	// Compute next run time.
	nextRun := c.computeNextRun(rule.CronExpression)

	// Determine if we need to notify.
	previousStatus := rule.LastStatus
	statusChanged := previousStatus != "" && previousStatus != status
	shouldNotify := false

	if status == "alert" || status == "error" {
		if statusChanged || previousStatus == "" {
			shouldNotify = true
		}
	}

	// Apply cooldown.
	if shouldNotify && rule.CooldownS > 0 && rule.LastAlertedAt != nil {
		cooldownUntil := rule.LastAlertedAt.Add(time.Duration(rule.CooldownS) * time.Second)
		if time.Now().UTC().Before(cooldownUntil) {
			shouldNotify = false
			c.logger.DebugContext(ctx, "alert notification suppressed by cooldown",
				slog.String("rule_id", rule.ID.String()),
			)
		}
	}

	// Dispatch notifications.
	var notifiedVia []string
	var notifyErrors []string

	if shouldNotify && len(rule.ChannelIDs) > 0 && c.dispatcher != nil {
		notifMsg := &notification.Message{
			Subject: fmt.Sprintf("[Akili Alert] %s — %s", rule.Name, status),
			Body: fmt.Sprintf(
				"Alert: %s\nTarget: %s\nStatus: %s (was: %s)\nDetails: %s",
				rule.Name, rule.Target, status, previousStatus, message,
			),
			Metadata: map[string]string{
				"alert_rule_id":   rule.ID.String(),
				"status":          status,
				"previous_status": previousStatus,
				"target":          rule.Target,
				"type":            "alert",
			},
		}

		results := c.dispatcher.Notify(ctx, rule.OrgID, rule.ChannelIDs, notifMsg, rule.UserID)
		for chID, err := range results {
			if err != nil {
				notifyErrors = append(notifyErrors, fmt.Sprintf("%s: %s", chID, err.Error()))
			} else {
				notifiedVia = append(notifiedVia, chID.String())
			}
		}

		if c.metrics != nil {
			c.metrics.AlertsFired.Inc()
		}

		// Record that we alerted.
		_ = store.RecordAlerted(ctx, rule.ID)
	}

	// Write alert history (append-only).
	history := &domain.AlertHistory{
		ID:              uuid.New(),
		OrgID:           rule.OrgID,
		AlertRuleID:     rule.ID,
		Status:          status,
		PreviousStatus:  previousStatus,
		Message:         message,
		NotifiedVia:     notifiedVia,
		NotifyErrors:    notifyErrors,
		CheckDurationMS: checkDuration,
		CreatedAt:       time.Now().UTC(),
	}
	if err := c.historyStore.Append(ctx, history); err != nil {
		c.logger.ErrorContext(ctx, "failed to record alert history",
			slog.String("rule_id", rule.ID.String()),
			slog.String("error", err.Error()),
		)
	}

	// Update alert rule state.
	errMsg := ""
	if checkErr != nil {
		errMsg = checkErr.Error()
		if c.metrics != nil {
			c.metrics.ChecksFailed.Inc()
		}
	} else {
		if c.metrics != nil {
			c.metrics.ChecksSucceeded.Inc()
		}
	}

	if recordErr := store.RecordCheck(ctx, rule.ID, nextRun, status, errMsg); recordErr != nil {
		c.logger.ErrorContext(ctx, "failed to record alert check",
			slog.String("rule_id", rule.ID.String()),
			slog.String("error", recordErr.Error()),
		)
	}

	// Audit: log check result.
	resultStr := "success"
	if checkErr != nil {
		resultStr = "failure"
	}
	c.auditLog(ctx, correlationID, rule.UserID, "alert.check", rule, resultStr, errMsg)
}

func (c *Checker) computeNextRun(expr string) time.Time {
	sched, err := c.parser.Parse(expr)
	if err != nil {
		c.logger.Error("invalid cron expression", slog.String("expr", expr), slog.String("error", err.Error()))
		return time.Now().UTC().Add(24 * time.Hour)
	}
	return sched.Next(time.Now().UTC())
}

func (c *Checker) auditLog(ctx context.Context, correlationID, userID, action string, rule *domain.AlertRule, result, errMsg string) {
	if c.security == nil {
		return
	}
	_ = c.security.LogAction(ctx, security.AuditEvent{
		Timestamp:     time.Now().UTC(),
		CorrelationID: correlationID,
		UserID:        userID,
		Action:        action,
		Tool:          "alerting",
		Parameters: map[string]any{
			"rule_id":    rule.ID.String(),
			"rule_name":  rule.Name,
			"target":     rule.Target,
			"check_type": rule.CheckType,
		},
		Result: result,
		Error:  errMsg,
	})
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
