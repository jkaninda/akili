// Package scheduler implements the cron job scheduler for Akili.
// It polls PostgreSQL for due jobs and submits them as standard workflows,
// inheriting the full security pipeline (RBAC, budget, approval, audit).
//
// Core invariant: scheduled execution is NOT privileged execution.
// All cron-triggered workflows run as the UserID that created the job.
package scheduler

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

	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/security"
)

// CronJobStore is the persistence interface for cron jobs.
type CronJobStore interface {
	Create(ctx context.Context, cj *domain.CronJob) error
	Get(ctx context.Context, orgID, id uuid.UUID) (*domain.CronJob, error)
	List(ctx context.Context, orgID uuid.UUID) ([]domain.CronJob, error)
	Update(ctx context.Context, cj *domain.CronJob) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	GetDueJobs(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.CronJob, error)
	RecordExecution(ctx context.Context, id uuid.UUID, workflowID uuid.UUID, nextRunAt time.Time, errMsg string) error
}

// StoreFactory creates a CronJobStore from a *gorm.DB.
// Used to create transaction-scoped stores without importing the postgres package.
type StoreFactory func(db *gorm.DB) CronJobStore

// Scheduler polls for due cron jobs and submits them as workflows.
// It runs as a background goroutine in gateway mode.
type Scheduler struct {
	store        CronJobStore
	storeFactory StoreFactory
	db           *gorm.DB // For transaction wrapping around GetDueJobs + RecordExecution.
	engine       orchestrator.WorkflowEngine
	security     security.SecurityManager
	metrics      *Metrics
	logger       *slog.Logger
	config       *config.SchedulerConfig
	orgID        uuid.UUID

	// Optional notification on job failure.
	dispatcher     *notification.Dispatcher
	failChannelIDs []uuid.UUID

	parser cron.Parser
}

// New creates a Scheduler.
func New(
	store CronJobStore,
	storeFactory StoreFactory,
	db *gorm.DB,
	engine orchestrator.WorkflowEngine,
	sec security.SecurityManager,
	metrics *Metrics,
	logger *slog.Logger,
	cfg *config.SchedulerConfig,
	orgID uuid.UUID,
) *Scheduler {
	return &Scheduler{
		store:        store,
		storeFactory: storeFactory,
		db:           db,
		engine:       engine,
		security:     sec,
		metrics:      metrics,
		logger:       logger,
		config:       cfg,
		orgID:        orgID,
		parser:       cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// WithNotifications enables failure notifications through the given dispatcher and channel IDs.
func (s *Scheduler) WithNotifications(d *notification.Dispatcher, channelIDs []uuid.UUID) *Scheduler {
	s.dispatcher = d
	s.failChannelIDs = channelIDs
	return s
}

// Start begins the scheduler loop. Returns a cancel function (matches approval.StartCleanup pattern).
func (s *Scheduler) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		s.logger.InfoContext(ctx, "cron scheduler started",
			slog.String("poll_interval", s.config.PollInterval().String()),
			slog.Int("max_concurrent", s.config.MaxConcurrent()),
		)

		// Recover missed jobs on startup.
		s.recoverMissedJobs(ctx)

		ticker := time.NewTicker(s.config.PollInterval())
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.logger.Info("cron scheduler stopped")
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()

	return cancel
}

// tick runs a single poll cycle: find due jobs, fire them, record results.
func (s *Scheduler) tick(ctx context.Context) {
	start := time.Now()
	now := start.UTC()

	// Execute within a transaction so that SELECT FOR UPDATE SKIP LOCKED
	// holds the lock until RecordExecution commits.
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := s.storeFactory(tx)

		dueJobs, err := txStore.GetDueJobs(ctx, s.orgID, now)
		if err != nil {
			return fmt.Errorf("polling due jobs: %w", err)
		}

		if len(dueJobs) == 0 {
			return nil
		}

		s.logger.InfoContext(ctx, "cron jobs due",
			slog.Int("count", len(dueJobs)),
		)

		sem := make(chan struct{}, s.config.MaxConcurrent())
		var wg sync.WaitGroup

		for i := range dueJobs {
			job := dueJobs[i]
			sem <- struct{}{}
			wg.Add(1)

			go func(j domain.CronJob) {
				defer wg.Done()
				defer func() { <-sem }()
				s.fireJob(ctx, txStore, &j)
			}(job)
		}

		wg.Wait()
		return nil
	})

	if err != nil {
		s.logger.ErrorContext(ctx, "scheduler tick failed",
			slog.String("error", err.Error()),
		)
	}

	if s.metrics != nil {
		s.metrics.TickDuration.Observe(time.Since(start).Seconds())
	}
}

// fireJob submits a single cron job as a workflow and records the result.
func (s *Scheduler) fireJob(ctx context.Context, store CronJobStore, job *domain.CronJob) {
	correlationID := newCorrelationID()

	s.logger.InfoContext(ctx, "firing cron job",
		slog.String("cronjob_id", job.ID.String()),
		slog.String("name", job.Name),
		slog.String("user_id", job.UserID),
		slog.String("correlation_id", correlationID),
	)

	// Audit: log the scheduled execution intent.
	if s.security != nil {
		_ = s.security.LogAction(ctx, security.AuditEvent{
			Timestamp:     time.Now().UTC(),
			CorrelationID: correlationID,
			UserID:        job.UserID,
			Action:        "cronjob.fire",
			Tool:          "scheduler",
			Parameters: map[string]any{
				"cronjob_id":      job.ID.String(),
				"cronjob_name":    job.Name,
				"cron_expression": job.CronExpression,
				"goal":            job.Goal,
			},
			Result: "intent",
		})
	}

	if s.metrics != nil {
		s.metrics.JobsFired.Inc()
	}

	// Submit workflow — runs as the cron job creator's identity (RBAC, budget, audit apply).
	wf, err := s.engine.Submit(ctx, &orchestrator.WorkflowRequest{
		UserID:         job.UserID,
		OrgID:          job.OrgID,
		Goal:           job.Goal,
		CorrelationID:  correlationID,
		BudgetLimitUSD: job.BudgetLimitUSD,
		MaxDepth:       job.MaxDepth,
		MaxTasks:       job.MaxTasks,
	})

	nextRun := s.computeNextRun(job.CronExpression)

	var errMsg string
	var workflowID uuid.UUID

	if err != nil {
		errMsg = err.Error()
		s.logger.ErrorContext(ctx, "cron job workflow submission failed",
			slog.String("cronjob_id", job.ID.String()),
			slog.String("error", errMsg),
		)
		if s.metrics != nil {
			s.metrics.JobsFailed.Inc()
		}

		if s.security != nil {
			_ = s.security.LogAction(ctx, security.AuditEvent{
				Timestamp:     time.Now().UTC(),
				CorrelationID: correlationID,
				UserID:        job.UserID,
				Action:        "cronjob.fire",
				Tool:          "scheduler",
				Result:        "failure",
				Error:         errMsg,
			})
		}

		// Send failure notification if configured.
		if s.dispatcher != nil && len(s.failChannelIDs) > 0 {
			msg := &notification.Message{
				Subject: fmt.Sprintf("[Akili] CronJob Failed: %s", job.Name),
				Body: fmt.Sprintf(
					"CronJob %q failed.\nJob ID: %s\nGoal: %s\nError: %s",
					job.Name, job.ID, job.Goal, errMsg,
				),
				Metadata: map[string]string{
					"type":       "cronjob_failure",
					"cronjob_id": job.ID.String(),
				},
			}
			_ = s.dispatcher.Notify(ctx, job.OrgID, s.failChannelIDs, msg, job.UserID)
		}
	} else {
		workflowID = wf.ID
		if s.metrics != nil {
			s.metrics.JobsSucceeded.Inc()
		}

		if s.security != nil {
			_ = s.security.LogAction(ctx, security.AuditEvent{
				Timestamp:     time.Now().UTC(),
				CorrelationID: correlationID,
				UserID:        job.UserID,
				Action:        "cronjob.fire",
				Tool:          "scheduler",
				Parameters: map[string]any{
					"workflow_id": workflowID.String(),
				},
				Result: "success",
			})
		}
	}

	if recordErr := store.RecordExecution(ctx, job.ID, workflowID, nextRun, errMsg); recordErr != nil {
		s.logger.ErrorContext(ctx, "failed to record cron execution",
			slog.String("cronjob_id", job.ID.String()),
			slog.String("error", recordErr.Error()),
		)
	}
}

// recoverMissedJobs finds jobs whose NextRunAt is in the past (within
// the missed window) and fires them. Handles crash recovery.
func (s *Scheduler) recoverMissedJobs(ctx context.Context) {
	now := time.Now().UTC()
	window := now.Add(-s.config.MissedJobWindow())

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := s.storeFactory(tx)
		dueJobs, err := txStore.GetDueJobs(ctx, s.orgID, now)
		if err != nil {
			return err
		}

		var missedCount, firedCount int
		for i := range dueJobs {
			job := &dueJobs[i]
			if job.NextRunAt != nil && job.NextRunAt.Before(window) {
				// Too old — skip and advance to next valid time.
				nextRun := s.computeNextRun(job.CronExpression)
				_ = txStore.RecordExecution(ctx, job.ID, uuid.Nil, nextRun, "skipped: outside missed job window")
				if s.metrics != nil {
					s.metrics.JobsMissed.Inc()
				}
				missedCount++
				continue
			}
			firedCount++
			s.fireJob(ctx, txStore, job)
		}

		if firedCount > 0 || missedCount > 0 {
			s.logger.InfoContext(ctx, "recovered missed cron jobs",
				slog.Int("fired", firedCount),
				slog.Int("skipped", missedCount),
			)
		}
		return nil
	})

	if err != nil {
		s.logger.ErrorContext(ctx, "failed to recover missed jobs",
			slog.String("error", err.Error()),
		)
	}
}

// computeNextRun parses the cron expression and returns the next run time after now.
func (s *Scheduler) computeNextRun(expr string) time.Time {
	sched, err := s.parser.Parse(expr)
	if err != nil {
		s.logger.Error("invalid cron expression", slog.String("expr", expr), slog.String("error", err.Error()))
		return time.Now().UTC().Add(24 * time.Hour)
	}
	return sched.Next(time.Now().UTC())
}

// ComputeNextRunFrom computes the next run time from a given reference time.
// Exported for use by the HTTP API when creating/updating jobs.
func ComputeNextRunFrom(expr string, from time.Time) (time.Time, error) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return sched.Next(from), nil
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
