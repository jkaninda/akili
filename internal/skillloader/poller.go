package skillloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/tools"
)

// SkillPollerConfig configures the skill sync poller.
type SkillPollerConfig struct {
	SkillsDirs   []string
	PollInterval time.Duration
	OrgID        uuid.UUID
}

// fileState tracks the last known state of a skill file for change detection.
type fileState struct {
	Hash     string
	ModTime  time.Time
	SkillKey string
}

// SkillPoller periodically scans skill directories for changes.
// Detects new files, modified files (hash change), and removed files.
// Thread-safe: can be queried for active skills from other goroutines.
type SkillPoller struct {
	config  SkillPollerConfig
	loader  *Loader
	store   orchestrator.SkillStore
	metrics *orchestrator.SkillMetrics
	logger  *slog.Logger

	mu           sync.RWMutex
	activeSkills map[string]*SkillDefinition // skill_key → definition
	fileStates   map[string]*fileState       // absolute_path → state
	onReload     func(defs []SkillDefinition)
}

// NewSkillPoller creates a poller. onReload is called when skills change.
func NewSkillPoller(
	cfg SkillPollerConfig,
	toolReg *tools.Registry,
	store orchestrator.SkillStore,
	metrics *orchestrator.SkillMetrics,
	onReload func(defs []SkillDefinition),
	logger *slog.Logger,
) *SkillPoller {
	return &SkillPoller{
		config:       cfg,
		loader:       NewLoader(toolReg, logger),
		store:        store,
		metrics:      metrics,
		logger:       logger,
		activeSkills: make(map[string]*SkillDefinition),
		fileStates:   make(map[string]*fileState),
		onReload:     onReload,
	}
}

// Run starts the polling loop. Performs an initial sync, then polls at interval.
// Blocks until ctx is canceled.
func (p *SkillPoller) Run(ctx context.Context) error {
	p.logger.DebugContext(ctx, "skill poller started",
		slog.String("interval", p.config.PollInterval.String()),
		slog.Int("dirs", len(p.config.SkillsDirs)),
	)

	// Initial sync.
	p.syncAll(ctx)

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Debug("skill poller stopped")
			return nil
		case <-ticker.C:
			p.syncAll(ctx)
		}
	}
}

// ActiveSkills returns a snapshot of currently active skill definitions.
func (p *SkillPoller) ActiveSkills() []SkillDefinition {
	p.mu.RLock()
	defer p.mu.RUnlock()

	defs := make([]SkillDefinition, 0, len(p.activeSkills))
	for _, d := range p.activeSkills {
		defs = append(defs, *d)
	}
	return defs
}

// syncAll scans all directories and detects changes.
func (p *SkillPoller) syncAll(ctx context.Context) {
	currentFiles := make(map[string]bool) // absolute paths seen this scan
	var changed bool

	for _, dir := range p.config.SkillsDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			p.logger.WarnContext(ctx, "skill poller: cannot read dir",
				slog.String("dir", dir),
				slog.String("error", err.Error()),
			)
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			absPath, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			currentFiles[absPath] = true

			hash, err := hashFile(absPath)
			if err != nil {
				p.logger.WarnContext(ctx, "skill poller: cannot hash file",
					slog.String("path", absPath),
					slog.String("error", err.Error()),
				)
				continue
			}

			p.mu.RLock()
			existing, known := p.fileStates[absPath]
			p.mu.RUnlock()

			if known && existing.Hash == hash {
				continue // Unchanged.
			}

			// New or changed file.
			def, err := p.loader.ParseFile(absPath)
			if err != nil {
				p.logger.WarnContext(ctx, "skill poller: parse error",
					slog.String("path", absPath),
					slog.String("error", err.Error()),
				)
				continue
			}
			if err := p.loader.Validate(def); err != nil {
				p.logger.WarnContext(ctx, "skill poller: validation error",
					slog.String("path", absPath),
					slog.String("skill", def.Name),
					slog.String("error", err.Error()),
				)
				continue
			}

			// Seed to database.
			if p.store != nil {
				SeedSkills(ctx, p.store, p.config.OrgID, []SkillDefinition{*def}, p.metrics, p.logger)
			}

			info, _ := os.Stat(absPath)
			modTime := time.Time{}
			if info != nil {
				modTime = info.ModTime()
			}

			p.mu.Lock()
			p.fileStates[absPath] = &fileState{
				Hash:     hash,
				ModTime:  modTime,
				SkillKey: def.SkillKey,
			}
			p.activeSkills[def.SkillKey] = def
			p.mu.Unlock()

			action := "added"
			if known {
				action = "updated"
			}
			p.logger.InfoContext(ctx, "skill "+action,
				slog.String("skill_key", def.SkillKey),
				slog.String("name", def.Name),
				slog.String("path", absPath),
			)
			changed = true
		}
	}

	// Detect removed files.
	p.mu.Lock()
	for absPath, state := range p.fileStates {
		if !currentFiles[absPath] {
			p.logger.InfoContext(ctx, "skill removed",
				slog.String("skill_key", state.SkillKey),
				slog.String("path", absPath),
			)
			delete(p.fileStates, absPath)
			delete(p.activeSkills, state.SkillKey)
			changed = true
		}
	}
	p.mu.Unlock()

	if changed && p.onReload != nil {
		p.onReload(p.ActiveSkills())
	}
}

// hashFile computes SHA-256 of a file's content.
func hashFile(path string) (string, error) {
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
