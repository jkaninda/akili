package ws

import (
	"log/slog"
	"sync"
	"time"

	"github.com/jkaninda/akili/internal/protocol"
)

// TaskState represents the lifecycle state of a dispatched task.
type TaskState string

const (
	TaskDispatched TaskState = "dispatched" // Sent to agent, awaiting ACK.
	TaskAccepted   TaskState = "accepted"   // Agent acknowledged receipt.
	TaskRunning    TaskState = "running"    // Agent reported progress.
	TaskCompleted  TaskState = "completed"  // Agent reported success.
	TaskFailed     TaskState = "failed"     // Agent reported failure.
	TaskTimedOut   TaskState = "timed_out"  // No ACK within timeout.
)

// TrackedTask holds the full lifecycle state for a single task assignment.
type TrackedTask struct {
	TaskID       string
	AgentID      string
	Assignment   protocol.TaskAssignment
	State        TaskState
	DispatchedAt time.Time
	AcceptedAt   time.Time
	CompletedAt  time.Time
	LastProgress time.Time
	Error        string
}

// TaskTracker manages the lifecycle of tasks dispatched via WebSocket.
// It tracks task state transitions and provides methods for querying
// task status, detecting unacknowledged tasks, and cleaning up.
type TaskTracker struct {
	mu         sync.RWMutex
	tasks      map[string]*TrackedTask // taskID -> TrackedTask
	ackTimeout time.Duration
	logger     *slog.Logger
}

// NewTaskTracker creates a new task tracker with the given ACK timeout.
func NewTaskTracker(ackTimeout time.Duration, logger *slog.Logger) *TaskTracker {
	if ackTimeout == 0 {
		ackTimeout = 30 * time.Second
	}
	return &TaskTracker{
		tasks:      make(map[string]*TrackedTask),
		ackTimeout: ackTimeout,
		logger:     logger,
	}
}

// Track records a newly dispatched task assignment.
func (t *TaskTracker) Track(assignment protocol.TaskAssignment, agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.tasks[assignment.TaskID] = &TrackedTask{
		TaskID:       assignment.TaskID,
		AgentID:      agentID,
		Assignment:   assignment,
		State:        TaskDispatched,
		DispatchedAt: time.Now(),
	}

	t.logger.Debug("task tracked",
		slog.String("task_id", assignment.TaskID),
		slog.String("agent_id", agentID),
		slog.String("state", string(TaskDispatched)),
	)
}

// MarkAccepted transitions a task from dispatched to accepted.
// Returns false if the task is not found or not in a valid state for acceptance.
func (t *TaskTracker) MarkAccepted(taskID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	task, ok := t.tasks[taskID]
	if !ok {
		return false
	}
	if task.State != TaskDispatched {
		t.logger.Warn("unexpected task acceptance",
			slog.String("task_id", taskID),
			slog.String("current_state", string(task.State)),
		)
		return false
	}

	task.State = TaskAccepted
	task.AcceptedAt = time.Now()

	t.logger.Debug("task accepted",
		slog.String("task_id", taskID),
		slog.String("agent_id", task.AgentID),
		slog.String("ack_latency", task.AcceptedAt.Sub(task.DispatchedAt).String()),
	)
	return true
}

// MarkProgress updates the last progress timestamp for a task.
func (t *TaskTracker) MarkProgress(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	task, ok := t.tasks[taskID]
	if !ok {
		return
	}

	if task.State == TaskAccepted {
		task.State = TaskRunning
	}
	task.LastProgress = time.Now()
}

// MarkCompleted transitions a task to the completed state.
func (t *TaskTracker) MarkCompleted(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	task, ok := t.tasks[taskID]
	if !ok {
		return
	}

	task.State = TaskCompleted
	task.CompletedAt = time.Now()

	t.logger.Debug("task completed",
		slog.String("task_id", taskID),
		slog.String("agent_id", task.AgentID),
		slog.String("total_duration", task.CompletedAt.Sub(task.DispatchedAt).String()),
	)
}

// MarkFailed transitions a task to the failed state with an error message.
func (t *TaskTracker) MarkFailed(taskID string, errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	task, ok := t.tasks[taskID]
	if !ok {
		return
	}

	task.State = TaskFailed
	task.CompletedAt = time.Now()
	task.Error = errMsg

	t.logger.Debug("task failed",
		slog.String("task_id", taskID),
		slog.String("agent_id", task.AgentID),
		slog.String("error", errMsg),
	)
}

// UnacknowledgedTasks returns tasks that have been dispatched but not accepted
// within the ACK timeout threshold.
func (t *TaskTracker) UnacknowledgedTasks() []*TrackedTask {
	t.mu.RLock()
	defer t.mu.RUnlock()

	deadline := time.Now().Add(-t.ackTimeout)
	var stale []*TrackedTask

	for _, task := range t.tasks {
		if task.State == TaskDispatched && task.DispatchedAt.Before(deadline) {
			stale = append(stale, task)
		}
	}
	return stale
}

// TasksForAgent returns all active (non-completed/failed) tasks for a given agent.
func (t *TaskTracker) TasksForAgent(agentID string) []*TrackedTask {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var tasks []*TrackedTask
	for _, task := range t.tasks {
		if task.AgentID == agentID && task.State != TaskCompleted && task.State != TaskFailed && task.State != TaskTimedOut {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

// Remove deletes a task from the tracker (cleanup after completion).
func (t *TaskTracker) Remove(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tasks, taskID)
}

// MarkTimedOut transitions a task to the timed-out state.
// Returns the tracked task for re-queuing, or nil if not found.
func (t *TaskTracker) MarkTimedOut(taskID string) *TrackedTask {
	t.mu.Lock()
	defer t.mu.Unlock()

	task, ok := t.tasks[taskID]
	if !ok {
		return nil
	}

	task.State = TaskTimedOut
	task.CompletedAt = time.Now()

	t.logger.Warn("task timed out (no acknowledgment)",
		slog.String("task_id", taskID),
		slog.String("agent_id", task.AgentID),
		slog.String("waited", time.Since(task.DispatchedAt).String()),
	)
	return task
}

// CleanCompleted removes all completed, failed, and timed-out tasks older than the given age.
func (t *TaskTracker) CleanCompleted(maxAge time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	deadline := time.Now().Add(-maxAge)
	cleaned := 0

	for id, task := range t.tasks {
		if (task.State == TaskCompleted || task.State == TaskFailed || task.State == TaskTimedOut) &&
			task.CompletedAt.Before(deadline) {
			delete(t.tasks, id)
			cleaned++
		}
	}
	return cleaned
}

// ActiveCount returns the number of tasks in dispatched, accepted, or running state.
func (t *TaskTracker) ActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	count := 0
	for _, task := range t.tasks {
		if task.State == TaskDispatched || task.State == TaskAccepted || task.State == TaskRunning {
			count++
		}
	}
	return count
}
