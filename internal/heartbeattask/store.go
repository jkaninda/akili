// Package heartbeattask implements periodic task execution from Markdown-defined task files.
// Tasks are parsed from .md files with YAML frontmatter, synced to the database,
// and executed on cron schedules via the agent core.
package heartbeattask

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
)

// HeartbeatTaskStore is the persistence interface for heartbeat tasks.
type HeartbeatTaskStore interface {
	// Upsert inserts or updates a task keyed by (org_id, file_group, name).
	// This enables file-based reload without creating duplicates.
	Upsert(ctx context.Context, task *domain.HeartbeatTask) error

	// Get retrieves a task by ID.
	Get(ctx context.Context, orgID, id uuid.UUID) (*domain.HeartbeatTask, error)

	// List returns all tasks for an organization.
	List(ctx context.Context, orgID uuid.UUID) ([]domain.HeartbeatTask, error)

	// Delete removes a task by ID.
	Delete(ctx context.Context, orgID, id uuid.UUID) error

	// DeleteByFileGroup removes all tasks loaded from a specific file group.
	// Used when a task file is removed from disk.
	DeleteByFileGroup(ctx context.Context, orgID uuid.UUID, fileGroup string) error

	// GetDueTasks returns tasks that are due for execution.
	// Uses SELECT FOR UPDATE SKIP LOCKED (PostgreSQL) or BEGIN IMMEDIATE (SQLite)
	// to prevent double-firing across multiple gateway instances.
	GetDueTasks(ctx context.Context, orgID uuid.UUID, now time.Time) ([]domain.HeartbeatTask, error)

	// RecordExecution updates a task after execution with the next run time and result summary.
	RecordExecution(ctx context.Context, id uuid.UUID, nextRunAt time.Time, status, lastError, resultSummary string) error
}

// HeartbeatTaskResultStore is the persistence interface for task execution history.
// Results are append-only (never updated or deleted).
type HeartbeatTaskResultStore interface {
	// Append persists a task execution result.
	Append(ctx context.Context, result *domain.HeartbeatTaskResult) error

	// ListByTask returns recent results for a specific task.
	ListByTask(ctx context.Context, orgID, taskID uuid.UUID, limit int) ([]domain.HeartbeatTaskResult, error)

	// ListRecent returns the most recent results across all tasks.
	ListRecent(ctx context.Context, orgID uuid.UUID, limit int) ([]domain.HeartbeatTaskResult, error)
}

// StoreFactory creates a HeartbeatTaskStore from a *gorm.DB for transaction-scoped operations.
type StoreFactory func(db *gorm.DB) HeartbeatTaskStore
