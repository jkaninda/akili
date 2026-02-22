package heartbeattask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/scheduler"
)

// TaskPollerConfig configures the task file poller.
type TaskPollerConfig struct {
	TasksDirs   []string
	PollInterval time.Duration
	OrgID       uuid.UUID
	DefaultUser string
}

// fileState tracks a file's hash for change detection.
type fileState struct {
	Hash      string
	FileGroup string // frontmatter name
}

// TaskPoller periodically scans task directories for changes.
// Detects new files, modified files (hash change), and removed files.
type TaskPoller struct {
	config TaskPollerConfig
	loader *Loader
	store  HeartbeatTaskStore
	logger *slog.Logger

	mu         sync.RWMutex
	fileStates map[string]*fileState // absolute_path → state
}

// NewTaskPoller creates a new TaskPoller.
func NewTaskPoller(config TaskPollerConfig, store HeartbeatTaskStore, logger *slog.Logger) *TaskPoller {
	return &TaskPoller{
		config:     config,
		loader:     NewLoader(logger),
		store:      store,
		logger:     logger,
		fileStates: make(map[string]*fileState),
	}
}

// Run starts the polling loop. Performs an initial sync, then polls at interval.
// Blocks until ctx is canceled.
func (p *TaskPoller) Run(ctx context.Context) error {
	p.syncAll(ctx)

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.syncAll(ctx)
		}
	}
}

// syncAll scans all directories, parses files, upserts tasks, removes stale ones.
func (p *TaskPoller) syncAll(ctx context.Context) {
	seen := make(map[string]bool)

	for _, dir := range p.config.TasksDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			p.logger.Warn("failed to read heartbeat tasks directory",
				slog.String("dir", dir),
				slog.String("error", err.Error()),
			)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			absPath := filepath.Join(dir, entry.Name())
			seen[absPath] = true

			hash, err := fileHash(absPath)
			if err != nil {
				p.logger.Warn("failed to hash heartbeat task file",
					slog.String("file", absPath),
					slog.String("error", err.Error()),
				)
				continue
			}

			p.mu.RLock()
			prev, exists := p.fileStates[absPath]
			p.mu.RUnlock()

			if exists && prev.Hash == hash {
				continue // No change.
			}

			// File is new or modified — parse and upsert.
			p.syncFile(ctx, absPath, hash)
		}
	}

	// Remove tasks from files that no longer exist.
	p.mu.Lock()
	for path, state := range p.fileStates {
		if !seen[path] {
			if err := p.store.DeleteByFileGroup(ctx, p.config.OrgID, state.FileGroup); err != nil {
				p.logger.Warn("failed to delete removed heartbeat task group",
					slog.String("file_group", state.FileGroup),
					slog.String("error", err.Error()),
				)
			} else {
				p.logger.Info("removed heartbeat task group (file deleted)",
					slog.String("file_group", state.FileGroup),
					slog.String("file", path),
				)
			}
			delete(p.fileStates, path)
		}
	}
	p.mu.Unlock()
}

// syncFile parses a single file and upserts its tasks.
func (p *TaskPoller) syncFile(ctx context.Context, path, hash string) {
	file, tasks, err := p.loader.ParseFile(path)
	if err != nil {
		p.logger.Warn("failed to parse heartbeat task file",
			slog.String("file", path),
			slog.String("error", err.Error()),
		)
		return
	}

	if err := p.loader.Validate(file, tasks); err != nil {
		p.logger.Warn("heartbeat task file validation failed",
			slog.String("file", path),
			slog.String("error", err.Error()),
		)
		return
	}

	userID := file.UserID
	if userID == "" {
		userID = p.config.DefaultUser
	}

	var channelIDs []uuid.UUID
	for _, idStr := range file.NotificationChannelIDs {
		if id, err := uuid.Parse(idStr); err == nil {
			channelIDs = append(channelIDs, id)
		}
	}

	upserted := 0
	for _, t := range tasks {
		cronExpr := t.CronExpression
		if cronExpr == "" {
			cronExpr = file.DefaultCron
		}
		if cronExpr == "" {
			p.logger.Warn("heartbeat task has no cron expression, skipping",
				slog.String("task", t.Name),
				slog.String("file", path),
			)
			continue
		}

		budget := t.BudgetUSD
		if budget <= 0 {
			budget = file.DefaultBudgetUSD
		}
		if budget <= 0 {
			budget = 0.50 // fallback default
		}

		nextRun, err := scheduler.ComputeNextRunFrom(cronExpr, time.Now().UTC())
		if err != nil {
			p.logger.Warn("invalid cron expression for heartbeat task",
				slog.String("task", t.Name),
				slog.String("cron", cronExpr),
				slog.String("error", err.Error()),
			)
			continue
		}

		now := time.Now().UTC()
		task := &domain.HeartbeatTask{
			ID:                     uuid.New(),
			OrgID:                  p.config.OrgID,
			Name:                   t.Name,
			Description:            t.Prompt,
			SourceFile:             path,
			FileGroup:              file.Name,
			CronExpression:         cronExpr,
			Mode:                   t.Mode,
			UserID:                 userID,
			BudgetLimitUSD:         budget,
			NotificationChannelIDs: channelIDs,
			Enabled:                file.Enabled,
			NextRunAt:              &nextRun,
			CreatedAt:              now,
			UpdatedAt:              now,
		}

		if err := p.store.Upsert(ctx, task); err != nil {
			p.logger.Warn("failed to upsert heartbeat task",
				slog.String("task", t.Name),
				slog.String("file_group", file.Name),
				slog.String("error", err.Error()),
			)
			continue
		}
		upserted++
	}

	p.mu.Lock()
	p.fileStates[path] = &fileState{Hash: hash, FileGroup: file.Name}
	p.mu.Unlock()

	p.logger.Info("heartbeat task file synced",
		slog.String("file", path),
		slog.String("file_group", file.Name),
		slog.Int("tasks", upserted),
	)
}

// fileHash computes the SHA-256 hash of a file.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
