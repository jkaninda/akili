package agent

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jkaninda/akili/internal/protocol"
)

// ErrNoAvailableAgent is returned when no connected agent can handle a task.
var ErrNoAvailableAgent = errors.New("no available agent with matching skills and capacity")

// AgentStatus represents the current state of a connected agent.
type AgentStatus string

const (
	AgentIdle     AgentStatus = "idle"
	AgentBusy     AgentStatus = "busy"
	AgentDraining AgentStatus = "draining"
)

// ConnectedAgent represents a remote agent connected via WebSocket.
type ConnectedAgent struct {
	Info        protocol.AgentCapabilities
	Conn        *websocket.Conn
	ActiveTasks int
	LastSeen    time.Time
	Status      AgentStatus
	ConnectedAt time.Time

	mu sync.Mutex
}

// Registry manages connected WebSocket agents.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*ConnectedAgent
	logger *slog.Logger
}

// NewRegistry creates a new agent registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		agents: make(map[string]*ConnectedAgent),
		logger: logger,
	}
}

// Register adds or updates an agent in the registry.
func (r *Registry) Register(info protocol.AgentCapabilities, conn *websocket.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	r.agents[info.AgentID] = &ConnectedAgent{
		Info:        info,
		Conn:        conn,
		Status:      AgentIdle,
		ConnectedAt: now,
		LastSeen:    now,
	}
	r.logger.Info("agent registered",
		slog.String("agent_id", info.AgentID),
		slog.Int("max_parallel", info.MaxParallel),
		slog.String("model", info.Model),
		slog.Int("skills", len(info.Skills)),
	)
}

// Deregister removes an agent from the registry.
func (r *Registry) Deregister(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.agents[agentID]; ok {
		delete(r.agents, agentID)
		r.logger.Info("agent deregistered", slog.String("agent_id", agentID))
	}
}

// Get returns a connected agent by ID.
func (r *Registry) Get(agentID string) (*ConnectedAgent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[agentID]
	return a, ok
}

// Route finds the best available agent for the given skill requirements.
// Uses least-loaded routing among agents that have matching skills and capacity.
func (r *Registry) Route(skills []string) (*ConnectedAgent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best *ConnectedAgent
	bestLoad := int(^uint(0) >> 1) // max int

	for _, a := range r.agents {
		if a.Status == AgentDraining {
			continue
		}
		// Check capacity.
		a.mu.Lock()
		active := a.ActiveTasks
		maxP := a.Info.MaxParallel
		a.mu.Unlock()

		if maxP > 0 && active >= maxP {
			continue
		}

		// Check skill match (empty skills = accept any agent).
		if len(skills) > 0 && !hasAllSkills(a.Info.Skills, skills) {
			continue
		}

		if active < bestLoad {
			best = a
			bestLoad = active
		}
	}

	if best == nil {
		return nil, ErrNoAvailableAgent
	}
	return best, nil
}

// UpdateHeartbeat refreshes the last-seen time for an agent.
func (r *Registry) UpdateHeartbeat(agentID string, activeTasks int) {
	r.mu.RLock()
	a, ok := r.agents[agentID]
	r.mu.RUnlock()

	if !ok {
		return
	}

	a.mu.Lock()
	a.LastSeen = time.Now().UTC()
	a.ActiveTasks = activeTasks
	if activeTasks > 0 {
		a.Status = AgentBusy
	} else {
		a.Status = AgentIdle
	}
	a.mu.Unlock()
}

// IncrementTasks increases the active task count for an agent.
func (r *Registry) IncrementTasks(agentID string) {
	r.mu.RLock()
	a, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.ActiveTasks++
	a.Status = AgentBusy
	a.mu.Unlock()
}

// DecrementTasks decreases the active task count for an agent.
func (r *Registry) DecrementTasks(agentID string) {
	r.mu.RLock()
	a, ok := r.agents[agentID]
	r.mu.RUnlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.ActiveTasks--
	if a.ActiveTasks <= 0 {
		a.ActiveTasks = 0
		a.Status = AgentIdle
	}
	a.mu.Unlock()
}

// List returns all connected agents.
func (r *Registry) List() []*ConnectedAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*ConnectedAgent, 0, len(r.agents))
	for _, a := range r.agents {
		result = append(result, a)
	}
	return result
}

// Count returns the number of connected agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// hasAllSkills checks if agentSkills contains all of the required skills.
func hasAllSkills(agentSkills, required []string) bool {
	skillSet := make(map[string]bool, len(agentSkills))
	for _, s := range agentSkills {
		skillSet[s] = true
	}
	for _, req := range required {
		if !skillSet[req] {
			return false
		}
	}
	return true
}
