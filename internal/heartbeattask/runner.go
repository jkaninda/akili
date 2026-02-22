package heartbeattask

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/akili/internal/security"
)

// Runner polls for due heartbeat tasks and executes them via the agent core.
type Runner struct {
	store            HeartbeatTaskStore
	resultStore      HeartbeatTaskResultStore
	storeFactory     StoreFactory
	db               *gorm.DB
	agentCore        agent.Agent
	security         security.SecurityManager
	dispatcher       *notification.Dispatcher
	proactiveHandler *ProactiveHandler
	metrics          *Metrics
	logger           *slog.Logger
	config           *config.HeartbeatTasksConfig
	orgID            uuid.UUID
}

// New creates a new heartbeat task Runner.
func New(
	store HeartbeatTaskStore,
	resultStore HeartbeatTaskResultStore,
	storeFactory StoreFactory,
	db *gorm.DB,
	agentCore agent.Agent,
	sec security.SecurityManager,
	dispatcher *notification.Dispatcher,
	metrics *Metrics,
	logger *slog.Logger,
	cfg *config.HeartbeatTasksConfig,
	orgID uuid.UUID,
) *Runner {
	return &Runner{
		store:        store,
		resultStore:  resultStore,
		storeFactory: storeFactory,
		db:           db,
		agentCore:    agentCore,
		security:     sec,
		dispatcher:   dispatcher,
		metrics:      metrics,
		logger:       logger,
		config:       cfg,
		orgID:        orgID,
	}
}

// WithProactiveHandler attaches a proactive alert handler for task failures.
func (r *Runner) WithProactiveHandler(h *ProactiveHandler) *Runner {
	r.proactiveHandler = h
	return r
}

// Start begins the runner loop. Returns a cancel function.
func (r *Runner) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		r.logger.Info("heartbeat task runner started",
			slog.String("poll_interval", r.config.PollInterval().String()),
			slog.Int("max_concurrent", r.config.MaxConcurrent()),
		)

		ticker := time.NewTicker(r.config.PollInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				r.logger.Info("heartbeat task runner stopped")
				return
			case <-ticker.C:
				r.tick(ctx)
			}
		}
	}()
	return cancel
}

// tick performs a single poll cycle: fetch due tasks and execute them.
func (r *Runner) tick(ctx context.Context) {
	start := time.Now()
	defer func() {
		if r.metrics != nil {
			r.metrics.TickDuration.Observe(time.Since(start).Seconds())
		}
	}()

	// Use transaction for atomic job claiming.
	var tasks []domain.HeartbeatTask
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := r.storeFactory(tx)
		var err error
		tasks, err = txStore.GetDueTasks(ctx, r.orgID, time.Now().UTC())
		return err
	})
	if err != nil {
		r.logger.Warn("heartbeat task tick: failed to get due tasks",
			slog.String("error", err.Error()),
		)
		return
	}

	if len(tasks) == 0 {
		return
	}

	r.logger.Debug("heartbeat tasks due",
		slog.Int("count", len(tasks)),
	)

	// Semaphore for concurrency limit.
	sem := make(chan struct{}, r.config.MaxConcurrent())
	var wg sync.WaitGroup

	for i := range tasks {
		task := tasks[i]
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r.executeTask(ctx, task)
		}()
	}

	wg.Wait()
}

// executeTask runs a single heartbeat task via agent.Process and records the result.
func (r *Runner) executeTask(ctx context.Context, task domain.HeartbeatTask) {
	correlationID := fmt.Sprintf("hbt-%s", uuid.New().String()[:8])
	start := time.Now()

	if r.metrics != nil {
		r.metrics.TasksExecuted.Inc()
	}

	// Determine timeout based on mode.
	timeout := r.config.QuickTaskTimeout()
	if task.Mode == "long" {
		timeout = r.config.LongTaskTimeout()
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Audit: log intent.
	_ = r.security.LogAction(ctx, security.AuditEvent{
		Timestamp:     time.Now().UTC(),
		CorrelationID: correlationID,
		UserID:        task.UserID,
		Action:        "heartbeat_task.fire",
		Tool:          "heartbeat_task_runner",
		Parameters: map[string]any{
			"task_id":         task.ID.String(),
			"task_name":       task.Name,
			"file_group":      task.FileGroup,
			"mode":            task.Mode,
			"cron_expression": task.CronExpression,
		},
		Result: "intent",
	})

	r.logger.Info("executing heartbeat task",
		slog.String("task", task.Name),
		slog.String("mode", task.Mode),
		slog.String("user_id", task.UserID),
		slog.String("correlation_id", correlationID),
	)

	// Execute via agent core.
	resp, err := r.agentCore.Process(taskCtx, &agent.Input{
		UserID:        task.UserID,
		Message:       task.Description,
		CorrelationID: correlationID,
	})

	// Compute next run.
	nextRun, cronErr := scheduler.ComputeNextRunFrom(task.CronExpression, time.Now().UTC())
	if cronErr != nil {
		r.logger.Warn("failed to compute next run for heartbeat task",
			slog.String("task", task.Name),
			slog.String("error", cronErr.Error()),
		)
		nextRun = time.Now().UTC().Add(5 * time.Minute) // fallback
	}

	duration := time.Since(start)

	// Build result record.
	result := &domain.HeartbeatTaskResult{
		ID:            uuid.New(),
		OrgID:         task.OrgID,
		TaskID:        task.ID,
		CorrelationID: correlationID,
		DurationMS:    duration.Milliseconds(),
		CreatedAt:     time.Now().UTC(),
	}

	if err != nil {
		result.Status = "failure"
		result.Output = err.Error()
		result.OutputSummary = truncate(err.Error(), 500)

		if r.metrics != nil {
			r.metrics.TasksFailed.Inc()
		}
	} else {
		result.Status = "success"
		result.Output = resp.Message
		result.OutputSummary = truncate(resp.Message, 500)
		result.TokensUsed = resp.TokensUsed

		if r.metrics != nil {
			r.metrics.TasksSucceeded.Inc()
		}
	}

	if r.metrics != nil {
		r.metrics.TaskDuration.(prometheus.Observer).Observe(duration.Seconds())
	}

	// Persist result.
	if appendErr := r.resultStore.Append(ctx, result); appendErr != nil {
		r.logger.Warn("failed to persist heartbeat task result",
			slog.String("task", task.Name),
			slog.String("error", appendErr.Error()),
		)
	}

	// Update task state.
	if recErr := r.store.RecordExecution(ctx, task.ID, nextRun, result.Status, result.OutputSummary, result.OutputSummary); recErr != nil {
		r.logger.Warn("failed to record heartbeat task execution",
			slog.String("task", task.Name),
			slog.String("error", recErr.Error()),
		)
	}

	// Audit: log result.
	auditResult := "success"
	auditError := ""
	if err != nil {
		auditResult = "failure"
		auditError = err.Error()
	}
	_ = r.security.LogAction(ctx, security.AuditEvent{
		Timestamp:     time.Now().UTC(),
		CorrelationID: correlationID,
		UserID:        task.UserID,
		Action:        "heartbeat_task.fire",
		Tool:          "heartbeat_task_runner",
		Parameters: map[string]any{
			"task_id":    task.ID.String(),
			"task_name":  task.Name,
			"file_group": task.FileGroup,
		},
		Result:     auditResult,
		TokensUsed: result.TokensUsed,
		CostUSD:    result.CostUSD,
		Error:      auditError,
	})

	// Proactive alert on failure.
	if result.Status == "failure" && r.proactiveHandler != nil && len(task.NotificationChannelIDs) > 0 {
		r.proactiveHandler.HandleFailure(ctx, task.Name, result.Output, task.UserID, task.NotificationChannelIDs)
	}

	// Notify if channels configured.
	if len(task.NotificationChannelIDs) > 0 && r.dispatcher != nil {
		r.notify(ctx, task, result)
	}

	r.logger.Info("heartbeat task completed",
		slog.String("task", task.Name),
		slog.String("status", result.Status),
		slog.String("duration", duration.String()),
		slog.String("correlation_id", correlationID),
	)
}

// notify sends task results to configured notification channels.
func (r *Runner) notify(ctx context.Context, task domain.HeartbeatTask, result *domain.HeartbeatTaskResult) {
	subject := fmt.Sprintf("Heartbeat Task: %s [%s]", task.Name, result.Status)
	body := fmt.Sprintf("Task: %s\nStatus: %s\nDuration: %dms\n\n%s",
		task.Name, result.Status, result.DurationMS, result.OutputSummary)

	errs := r.dispatcher.Notify(ctx, r.orgID, task.NotificationChannelIDs, &notification.Message{
		Subject: subject,
		Body:    body,
		Metadata: map[string]string{
			"task_id":        task.ID.String(),
			"task_name":      task.Name,
			"status":         result.Status,
			"correlation_id": result.CorrelationID,
		},
	}, task.UserID)

	var notifiedVia []string
	var notifyErrors []string
	for chID, err := range errs {
		if err != nil {
			notifyErrors = append(notifyErrors, fmt.Sprintf("%s: %s", chID, err.Error()))
		} else {
			notifiedVia = append(notifiedVia, chID.String())
		}
	}

	result.NotifiedVia = notifiedVia
	result.NotifyErrors = notifyErrors
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
