package soul

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/llm"
)

// Config holds soul subsystem configuration.
type Config struct {
	Enabled                bool `json:"enabled" yaml:"enabled"`
	ReflectionIntervalMins int  `json:"reflection_interval_mins" yaml:"reflection_interval_mins"` // 0 = reflection disabled. Default: 60.
	MaxPatternsPerCategory int  `json:"max_patterns_per_category" yaml:"max_patterns_per_category"` // Default: 20.
	MaxReflections         int  `json:"max_reflections" yaml:"max_reflections"`                     // Default: 50.
	MaxStrategies          int  `json:"max_strategies" yaml:"max_strategies"`                       // Default: 20.
}

func (c *Config) reflectionInterval() time.Duration {
	if c.ReflectionIntervalMins > 0 {
		return time.Duration(c.ReflectionIntervalMins) * time.Minute
	}
	return 60 * time.Minute
}

// Manager coordinates soul loading, evolution, rendering, and prompt injection.
// All methods are nil-safe: if the receiver is nil, they are no-ops.
type Manager struct {
	store    SoulStore
	orgID    uuid.UUID
	soulDir  string // Path to workspace soul directory.
	logger   *slog.Logger
	provider llm.Provider // For reflection cycles (nil = reflection disabled).
	config   Config

	mu   sync.RWMutex
	soul *Soul // Cached materialized state.
}

// NewManager creates a soul Manager.
// Returns nil if store is nil (disabled mode).
func NewManager(store SoulStore, orgID uuid.UUID, soulDir string, provider llm.Provider, cfg Config, logger *slog.Logger) *Manager {
	if store == nil {
		return nil
	}
	return &Manager{
		store:    store,
		orgID:    orgID,
		soulDir:  soulDir,
		provider: provider,
		config:   cfg,
		logger:   logger,
	}
}

// Load materializes the current soul from the event log.
// Should be called once at startup.
func (m *Manager) Load(ctx context.Context) error {
	if m == nil {
		return nil
	}

	events, err := m.store.ListEvents(ctx, m.orgID)
	if err != nil {
		return fmt.Errorf("loading soul events: %w", err)
	}

	// Seed core principles on first run.
	if len(events) == 0 {
		if err := m.seedCorePrinciples(ctx); err != nil {
			return fmt.Errorf("seeding core principles: %w", err)
		}
		events, err = m.store.ListEvents(ctx, m.orgID)
		if err != nil {
			return fmt.Errorf("reloading soul events: %w", err)
		}
	}

	s := Materialize(events)
	m.mu.Lock()
	m.soul = s
	m.mu.Unlock()

	// Render SOUL.md.
	if err := WriteSoulFile(m.soulDir, s); err != nil {
		m.logger.Warn("failed to render SOUL.md", slog.String("error", err.Error()))
	}

	m.logger.Info("soul loaded",
		slog.Int("version", s.Version),
		slog.Int("total_events", s.TotalEvents),
		slog.Int("patterns", len(s.LearnedPatterns)),
		slog.Int("reflections", len(s.Reflections)),
		slog.Int("strategies", len(s.ReasoningStrategies)),
		slog.Int("guardrails", len(s.Guardrails)),
	)

	return nil
}

// Evolve appends a new event and updates the cached soul.
func (m *Manager) Evolve(ctx context.Context, event *SoulEvent) error {
	if m == nil {
		return nil
	}

	// Assign version.
	latestVersion, err := m.store.LatestVersion(ctx, m.orgID)
	if err != nil {
		return fmt.Errorf("getting latest version: %w", err)
	}

	event.ID = uuid.New()
	event.OrgID = m.orgID
	event.Version = latestVersion + 1
	event.CreatedAt = time.Now().UTC()

	if err := m.store.AppendEvent(ctx, event); err != nil {
		return fmt.Errorf("appending soul event: %w", err)
	}

	// Incrementally update cached state.
	m.mu.Lock()
	if m.soul == nil {
		m.soul = &Soul{}
	}
	applyEvent(m.soul, event)
	m.mu.Unlock()

	// Re-render SOUL.md (non-fatal).
	m.mu.RLock()
	s := m.soul
	m.mu.RUnlock()
	if err := WriteSoulFile(m.soulDir, s); err != nil {
		m.logger.Warn("failed to render SOUL.md", slog.String("error", err.Error()))
	}

	m.logger.Info("soul evolved",
		slog.Int("version", event.Version),
		slog.String("event_type", string(event.EventType)),
		slog.String("category", event.Category),
		slog.String("title", event.Title),
	)

	return nil
}

// CurrentPromptContext returns the soul context formatted for system prompt injection.
func (m *Manager) CurrentPromptContext() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.soul == nil {
		return ""
	}
	return RenderPromptContext(m.soul)
}

// Current returns a snapshot of the current soul state.
func (m *Manager) Current() *Soul {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.soul
}

// StartReflectionLoop starts the periodic LLM reflection cycle.
// Returns a cancel function. Non-blocking.
func (m *Manager) StartReflectionLoop(ctx context.Context) context.CancelFunc {
	if m == nil || m.provider == nil || m.config.ReflectionIntervalMins == 0 {
		return func() {} // no-op
	}
	ctx, cancel := context.WithCancel(ctx)
	go m.reflectionLoop(ctx)
	return cancel
}

// seedCorePrinciples writes the immutable core axioms on first run.
func (m *Manager) seedCorePrinciples(ctx context.Context) error {
	principles := []struct{ name, desc string }{
		{"Default Deny", "If something is ambiguous, unsafe, or outside explicit authorization, do not execute it."},
		{"Explicit Allow", "Act only when permission, scope, and intent are clear."},
		{"Auditability Over Convenience", "Every action must be explainable and justifiable."},
		{"Reliability Over Speed", "Fast and wrong is worse than slow and correct."},
		{"Security Before Automation", "Automation that weakens control is a failure."},
	}

	for i, p := range principles {
		event := &SoulEvent{
			ID:          uuid.New(),
			OrgID:       m.orgID,
			Version:     i + 1,
			EventType:   EventManualUpdate,
			Severity:    SeverityMajor,
			Category:    "principle",
			Title:       p.name,
			Description: p.desc,
			Evidence:    `{"source": "CONTEXT.md", "immutable": true}`,
			CreatedAt:   time.Now().UTC(),
		}
		if err := m.store.AppendEvent(ctx, event); err != nil {
			return fmt.Errorf("seeding principle %q: %w", p.name, err)
		}
	}

	m.logger.Info("soul core principles seeded", slog.Int("count", len(principles)))
	return nil
}
