package ws

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/protocol"
)

// SyncTaskResult holds the result of a synchronous task execution.
type SyncTaskResult struct {
	Success    bool
	Output     string
	TokensUsed int
	Error      string
}

// syncWaiter manages pending synchronous task result channels.
type syncWaiter struct {
	mu      sync.Mutex
	waiters map[string]chan *SyncTaskResult // taskID -> result channel
}

func newSyncWaiter() *syncWaiter {
	return &syncWaiter{
		waiters: make(map[string]chan *SyncTaskResult),
	}
}

// register creates a result channel for the given task ID.
func (sw *syncWaiter) register(taskID string) chan *SyncTaskResult {
	ch := make(chan *SyncTaskResult, 1)
	sw.mu.Lock()
	sw.waiters[taskID] = ch
	sw.mu.Unlock()
	return ch
}

// resolve sends a result to the waiting channel for the given task ID.
// Returns true if a waiter was found and notified.
func (sw *syncWaiter) resolve(taskID string, result *SyncTaskResult) bool {
	sw.mu.Lock()
	ch, ok := sw.waiters[taskID]
	if ok {
		delete(sw.waiters, taskID)
	}
	sw.mu.Unlock()

	if ok {
		ch <- result
		return true
	}
	return false
}

// remove cleans up a waiter without sending a result (e.g. on timeout).
func (sw *syncWaiter) remove(taskID string) {
	sw.mu.Lock()
	delete(sw.waiters, taskID)
	sw.mu.Unlock()
}

// ExecuteSync sends a task to an available agent and blocks until the result
// is received or the context expires. This bridges the async WebSocket protocol
// with synchronous gateway query handling.
func (s *Server) ExecuteSync(ctx context.Context, userID, message string, budgetUSD float64, timeout time.Duration) (*SyncTaskResult, error) {
	// Find an available agent.
	a, err := s.registry.Route(nil)
	if err != nil {
		return nil, err
	}

	taskID := uuid.New().String()
	assignment := protocol.TaskAssignment{
		TaskID:      taskID,
		UserID:      userID,
		Goal:        message,
		BudgetUSD:   budgetUSD,
		TimeoutSecs: int(timeout.Seconds()),
	}

	// Register a waiter before sending (avoids race with fast responses).
	ch := s.sync.register(taskID)

	// Send task to the agent.
	if err := s.sendTaskToAgent(ctx, a, assignment); err != nil {
		s.sync.remove(taskID)
		return nil, fmt.Errorf("dispatching task to agent %s: %w", a.Info.AgentID, err)
	}

	s.logger.Info("synchronous task dispatched",
		slog.String("task_id", taskID),
		slog.String("agent_id", a.Info.AgentID),
		slog.String("user_id", userID),
	)

	// Wait for result, timeout, or context cancellation.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-ch:
		return result, nil
	case <-timer.C:
		s.sync.remove(taskID)
		return nil, fmt.Errorf("task %s timed out after %s", taskID, timeout)
	case <-ctx.Done():
		s.sync.remove(taskID)
		return nil, ctx.Err()
	}
}
