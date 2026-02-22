package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// taskExecutor handles execution of a single task within a workflow.
type taskExecutor struct {
	store        WorkflowStore
	factory      AgentFactory
	metrics      *WorkflowMetrics
	skillTracker *SkillTracker
	logger       *slog.Logger
}

// executeTask runs a task: creates the role agent, invokes it, processes output,
// and updates the task record. Returns the agent output or an error.
func (e *taskExecutor) executeTask(ctx context.Context, wf *Workflow, task *Task) (*AgentOutput, error) {
	// Mark task as running.
	now := time.Now().UTC()
	task.Status = TaskRunning
	task.StartedAt = &now
	task.UpdatedAt = now
	if err := e.store.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("marking task running: %w", err)
	}

	if e.metrics != nil {
		e.metrics.ActiveTasks.Inc()
		defer e.metrics.ActiveTasks.Dec()
	}

	// Create role agent.
	agent, err := e.factory.Create(task.AgentRole)
	if err != nil {
		return nil, e.failTask(ctx, task, fmt.Errorf("creating %s agent: %w", task.AgentRole, err))
	}

	// Build agent input with workflow context.
	messages, _ := e.store.ListMessages(ctx, wf.ID)
	input := &AgentInput{
		WorkflowID:    wf.ID,
		TaskID:        task.ID,
		UserID:        wf.UserID,
		CorrelationID: wf.CorrelationID,
		Messages:      filterMessagesForTask(messages, task),
		Instruction:   task.Input,
	}

	// Record invocation.
	if e.metrics != nil {
		e.metrics.AgentInvocationsTotal.WithLabelValues(string(task.AgentRole), "started").Inc()
	}

	// Invoke the agent.
	output, err := agent.Process(ctx, input)
	if err != nil {
		if e.metrics != nil {
			e.metrics.AgentInvocationsTotal.WithLabelValues(string(task.AgentRole), "error").Inc()
		}
		return nil, e.failTask(ctx, task, fmt.Errorf("agent %s execution: %w", task.AgentRole, err))
	}

	if e.metrics != nil {
		e.metrics.AgentInvocationsTotal.WithLabelValues(string(task.AgentRole), "success").Inc()
	}

	// Save the task result as an agent message.
	resultMsg := &AgentMessage{
		ID:            uuid.New(),
		WorkflowID:    wf.ID,
		FromTaskID:    task.ID,
		FromRole:      task.AgentRole,
		ToRole:        RoleOrchestrator,
		MessageType:   MsgTaskResult,
		Content:       output.Response,
		CorrelationID: wf.CorrelationID,
		CreatedAt:     time.Now().UTC(),
	}
	_ = e.store.SaveMessage(ctx, resultMsg)

	// Mark task completed.
	completedAt := time.Now().UTC()
	task.Status = TaskCompleted
	task.Output = output.Response
	task.CompletedAt = &completedAt
	task.UpdatedAt = completedAt
	task.TokensUsed = output.TokensUsed
	task.CostUSD = output.CostUSD
	if err := e.store.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("marking task completed: %w", err)
	}

	// Record task metrics.
	if e.metrics != nil && task.StartedAt != nil {
		duration := completedAt.Sub(*task.StartedAt).Seconds()
		e.metrics.TaskDuration.WithLabelValues(string(task.AgentRole)).Observe(duration)
		e.metrics.TasksTotal.WithLabelValues(string(task.AgentRole), "completed").Inc()
	}

	// Record skill metrics.
	if e.skillTracker != nil && task.StartedAt != nil {
		durationMS := completedAt.Sub(*task.StartedAt).Seconds() * 1000
		e.skillTracker.RecordCompletion(ctx, wf.OrgID, task, durationMS, output.CostUSD, true)
	}

	return output, nil
}

// failTask marks a task as failed, records the failure in skill metrics, and returns the error.
func (e *taskExecutor) failTask(ctx context.Context, task *Task, err error) error {
	now := time.Now().UTC()
	task.Status = TaskFailed
	task.Error = err.Error()
	task.CompletedAt = &now
	task.UpdatedAt = now
	if updateErr := e.store.UpdateTask(ctx, task); updateErr != nil {
		e.logger.Error("failed to update task status",
			slog.String("task_id", task.ID.String()),
			slog.String("error", updateErr.Error()),
		)
	}
	if e.metrics != nil {
		e.metrics.TasksTotal.WithLabelValues(string(task.AgentRole), "failed").Inc()
	}
	// Record failure in skill tracker (duration=0, cost=0 for failures).
	if e.skillTracker != nil && task.WorkflowID != (uuid.UUID{}) {
		e.skillTracker.RecordCompletion(ctx, task.OrgID, task, 0, 0, false)
	}
	return err
}

// filterMessagesForTask returns messages relevant to the given task:
// messages addressed to this task, or broadcast messages from the same workflow.
func filterMessagesForTask(messages []AgentMessage, task *Task) []AgentMessage {
	var relevant []AgentMessage
	for _, msg := range messages {
		// Include messages addressed to this task.
		if msg.ToTaskID != nil && *msg.ToTaskID == task.ID {
			relevant = append(relevant, msg)
			continue
		}
		// Include broadcast messages from parent tasks.
		if msg.ToTaskID == nil && task.ParentTaskID != nil && msg.FromTaskID == *task.ParentTaskID {
			relevant = append(relevant, msg)
			continue
		}
		// Include task results from sibling tasks (same parent).
		if msg.MessageType == MsgTaskResult && task.ParentTaskID != nil {
			relevant = append(relevant, msg)
		}
	}
	return relevant
}

// parsePlannerOutput extracts TaskSpec array from the planner's response.
func parsePlannerOutput(output *AgentOutput) ([]TaskSpec, error) {
	if output.Response == "" {
		return nil, fmt.Errorf("planner returned empty response")
	}

	// Try to parse the response as JSON array of TaskSpec.
	var specs []TaskSpec
	if err := json.Unmarshal([]byte(output.Response), &specs); err != nil {
		// Try to find JSON array within the response (planner might include surrounding text).
		start := findJSONArrayStart(output.Response)
		if start < 0 {
			return nil, fmt.Errorf("planner response does not contain a JSON task array: %w", err)
		}
		end := findJSONArrayEnd(output.Response, start)
		if end < 0 {
			return nil, fmt.Errorf("planner response contains malformed JSON array: %w", err)
		}
		if err := json.Unmarshal([]byte(output.Response[start:end+1]), &specs); err != nil {
			return nil, fmt.Errorf("parsing planner JSON: %w", err)
		}
	}

	if len(specs) == 0 {
		return nil, fmt.Errorf("planner returned empty task list")
	}

	return specs, nil
}

// findJSONArrayStart finds the index of the first '[' in the string.
func findJSONArrayStart(s string) int {
	for i, c := range s {
		if c == '[' {
			return i
		}
	}
	return -1
}

// findJSONArrayEnd finds the matching ']' for the '[' at start.
func findJSONArrayEnd(s string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '[' {
			depth++
		} else if c == ']' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
