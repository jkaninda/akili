// Package heartbeat provides periodic agent health reporting.
// HeartbeatSender runs in agent processes, sending signed heartbeats to the gateway.
// RunStaleChecker runs in gateway processes, marking unresponsive agents as stale.
package heartbeat

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jkaninda/akili/internal/identity"
)

// Status represents the agent's self-reported health.
type Status string

const (
	StatusOnline   Status = "online"
	StatusBusy     Status = "busy"
	StatusDraining Status = "draining"
)

// Heartbeat is the payload sent periodically to the gateway.
type Heartbeat struct {
	AgentID      string            `json:"agent_id"`
	Version      string            `json:"version"`
	Status       Status            `json:"status"`
	Skills       []string          `json:"skills"`
	Capabilities []string          `json:"capabilities"`
	ActiveTasks  int               `json:"active_tasks"`
	MaxTasks     int               `json:"max_tasks"`
	Signature    []byte            `json:"signature,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// HeartbeatStore persists heartbeat data for gateway-side tracking.
type HeartbeatStore interface {
	RecordHeartbeat(ctx context.Context, hb *Heartbeat) error
	GetLastHeartbeat(ctx context.Context, agentID string) (*Heartbeat, error)
	ListOnlineAgents(ctx context.Context, staleThreshold time.Duration) ([]Heartbeat, error)
	MarkStale(ctx context.Context, staleThreshold time.Duration) (int, error)
}

// HeartbeatSender runs in the agent process, sending periodic heartbeats.
type HeartbeatSender struct {
	identity      *identity.IdentityConfig
	store         HeartbeatStore
	interval      time.Duration
	logger        *slog.Logger
	mu            sync.Mutex
	activeTasksFn func() int
	maxTasks      int
	skills        []string
}

// NewHeartbeatSender creates a sender.
func NewHeartbeatSender(
	id *identity.IdentityConfig,
	store HeartbeatStore,
	interval time.Duration,
	maxTasks int,
	activeTasksFn func() int,
	logger *slog.Logger,
) *HeartbeatSender {
	return &HeartbeatSender{
		identity:      id,
		store:         store,
		interval:      interval,
		maxTasks:      maxTasks,
		activeTasksFn: activeTasksFn,
		logger:        logger,
	}
}

// SetSkills updates the skills list sent with each heartbeat.
func (s *HeartbeatSender) SetSkills(skills []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skills = skills
}

// Run starts the heartbeat loop. Blocks until ctx is canceled.
func (s *HeartbeatSender) Run(ctx context.Context) error {
	s.logger.DebugContext(ctx, "heartbeat sender started",
		slog.String("agent_id", s.identity.AgentID),
		slog.String("interval", s.interval.String()),
	)

	// Initial heartbeat on startup.
	s.sendHeartbeat(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("heartbeat sender stopped")
			return nil
		case <-ticker.C:
			s.sendHeartbeat(ctx)
		}
	}
}

func (s *HeartbeatSender) sendHeartbeat(ctx context.Context) {
	s.mu.Lock()
	skills := make([]string, len(s.skills))
	copy(skills, s.skills)
	s.mu.Unlock()

	activeTasks := 0
	if s.activeTasksFn != nil {
		activeTasks = s.activeTasksFn()
	}

	status := StatusOnline
	if activeTasks >= s.maxTasks {
		status = StatusBusy
	}

	hb := &Heartbeat{
		AgentID:      s.identity.AgentID,
		Version:      s.identity.Version,
		Status:       status,
		Skills:       skills,
		Capabilities: s.identity.Capabilities,
		ActiveTasks:  activeTasks,
		MaxTasks:     s.maxTasks,
		Timestamp:    time.Now().UTC(),
	}

	// Sign the heartbeat payload.
	payload, err := json.Marshal(hb)
	if err == nil {
		hb.Signature = s.identity.Sign(payload)
	}

	if err := s.store.RecordHeartbeat(ctx, hb); err != nil {
		s.logger.ErrorContext(ctx, "heartbeat failed",
			slog.String("agent_id", s.identity.AgentID),
			slog.String("error", err.Error()),
		)
		return
	}

	s.logger.DebugContext(ctx, "heartbeat sent",
		slog.String("agent_id", s.identity.AgentID),
		slog.String("status", string(status)),
		slog.Int("active_tasks", activeTasks),
	)
}

// RunStaleChecker runs in the gateway process, periodically marking stale agents.
// Blocks until ctx is canceled.
func RunStaleChecker(ctx context.Context, store HeartbeatStore, interval time.Duration, staleThreshold time.Duration, logger *slog.Logger) {
	logger.Debug("heartbeat stale checker started",
		slog.String("interval", interval.String()),
		slog.String("stale_threshold", staleThreshold.String()),
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Debug("heartbeat stale checker stopped")
			return
		case <-ticker.C:
			count, err := store.MarkStale(ctx, staleThreshold)
			if err != nil {
				logger.ErrorContext(ctx, "stale check failed",
					slog.String("error", err.Error()),
				)
				continue
			}
			if count > 0 {
				logger.WarnContext(ctx, "agents marked stale",
					slog.Int("count", count),
				)
			}
		}
	}
}
