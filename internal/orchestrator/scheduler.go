package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// scheduler runs the DAG-based scheduling loop for a single workflow.
type scheduler struct {
	store       WorkflowStore
	executor    *taskExecutor
	skillScorer *SkillScorer
	config      EngineConfig
	logger      *slog.Logger
	metrics     *WorkflowMetrics
}

// EngineConfig configures the workflow engine behavior.
type EngineConfig struct {
	DefaultBudgetLimitUSD float64       // Per-workflow default budget. Default: 1.0
	DefaultMaxDepth       int           // Default: 5.
	DefaultMaxTasks       int           // Default: 50.
	MaxConcurrentTasks    int           // Parallelism limit. Default: 5.
	TaskTimeout           time.Duration // Per-task timeout. Default: 5m.
	ExternalAgents        bool          // When true, tasks are claimed by external agent pollers instead of the inline scheduler.
	SkillWeights          SkillWeights  // Weights for skill-based scoring. Zero value = defaults.
}

func (c EngineConfig) budgetLimit() float64 {
	if c.DefaultBudgetLimitUSD > 0 {
		return c.DefaultBudgetLimitUSD
	}
	return 1.0
}

func (c EngineConfig) maxDepth() int {
	if c.DefaultMaxDepth > 0 {
		return c.DefaultMaxDepth
	}
	return 5
}

func (c EngineConfig) maxTasks() int {
	if c.DefaultMaxTasks > 0 {
		return c.DefaultMaxTasks
	}
	return 50
}

func (c EngineConfig) concurrency() int {
	if c.MaxConcurrentTasks > 0 {
		return c.MaxConcurrentTasks
	}
	return 5
}

func (c EngineConfig) taskTimeout() time.Duration {
	if c.TaskTimeout > 0 {
		return c.TaskTimeout
	}
	return 5 * time.Minute
}

// run executes the scheduling loop for a workflow.
// It discovers ready tasks, dispatches them concurrently up to the limit,
// and loops until all tasks complete or an error occurs.
func (s *scheduler) run(ctx context.Context, wf *Workflow) error {
	s.logger.InfoContext(ctx, "workflow scheduling started",
		slog.String("workflow_id", wf.ID.String()),
		slog.String("user_id", wf.UserID),
		slog.String("correlation_id", wf.CorrelationID),
	)

	for {
		// Check context cancellation.
		if err := ctx.Err(); err != nil {
			return s.cancelWorkflow(ctx, wf, err)
		}

		// Get ready tasks.
		ready, err := s.store.GetReadyTasks(ctx, wf.ID)
		if err != nil {
			return fmt.Errorf("getting ready tasks: %w", err)
		}

		// Reorder ready tasks by skill score within same priority.
		if s.skillScorer != nil && len(ready) > 1 {
			ready = s.reorderBySkillScore(ctx, wf.OrgID, ready)
		}

		// Check for completion.
		allTasks, err := s.store.ListTasksByWorkflow(ctx, wf.ID)
		if err != nil {
			return fmt.Errorf("listing tasks: %w", err)
		}

		running := countByStatus(allTasks, TaskRunning)

		if len(ready) == 0 && running == 0 {
			// No more work to do.
			failed := countByStatus(allTasks, TaskFailed)
			if failed > 0 {
				return s.finishWorkflow(ctx, wf, WorkflowFailed,
					fmt.Sprintf("%d task(s) failed", failed))
			}
			return s.finishWorkflow(ctx, wf, WorkflowCompleted, "")
		}

		if len(ready) == 0 {
			// Tasks are still running, wait briefly before re-checking.
			select {
			case <-ctx.Done():
				return s.cancelWorkflow(ctx, wf, ctx.Err())
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		// Limit concurrency.
		slots := s.config.concurrency() - running
		if slots <= 0 {
			select {
			case <-ctx.Done():
				return s.cancelWorkflow(ctx, wf, ctx.Err())
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}
		if len(ready) > slots {
			ready = ready[:slots]
		}

		// Dispatch ready tasks concurrently.
		var wg sync.WaitGroup
		results := make(chan taskResult, len(ready))

		for i := range ready {
			task := ready[i]
			wg.Add(1)
			go func(t Task) {
				defer wg.Done()
				taskCtx, cancel := context.WithTimeout(ctx, s.config.taskTimeout())
				defer cancel()

				output, err := s.executor.executeTask(taskCtx, wf, &t)
				results <- taskResult{task: t, output: output, err: err}
			}(task)
		}

		// Wait for all dispatched tasks to complete.
		go func() {
			wg.Wait()
			close(results)
		}()

		for res := range results {
			if res.err != nil {
				s.logger.WarnContext(ctx, "task failed",
					slog.String("task_id", res.task.ID.String()),
					slog.String("agent_role", string(res.task.AgentRole)),
					slog.String("error", res.err.Error()),
				)
				continue
			}

			// Handle planner output: create sub-tasks from the plan.
			if res.task.AgentRole == RolePlanner && res.output != nil {
				if err := s.handlePlannerOutput(ctx, wf, &res.task, res.output); err != nil {
					s.logger.WarnContext(ctx, "failed to handle planner output",
						slog.String("task_id", res.task.ID.String()),
						slog.String("error", err.Error()),
					)
				}
			}

			// Accumulate cost.
			if res.output != nil {
				wf.BudgetSpentUSD += res.output.CostUSD
			}
		}

		// Refresh workflow state.
		latest, err := s.store.GetWorkflow(ctx, wf.ID)
		if err == nil {
			wf = latest
		}

		// Check budget.
		if wf.BudgetLimitUSD > 0 && wf.BudgetSpentUSD > wf.BudgetLimitUSD {
			return s.finishWorkflow(ctx, wf, WorkflowFailed,
				fmt.Sprintf("budget exceeded: spent $%.4f > limit $%.4f",
					wf.BudgetSpentUSD, wf.BudgetLimitUSD))
		}
	}
}

type taskResult struct {
	task   Task
	output *AgentOutput
	err    error
}

// handlePlannerOutput parses the planner's response and creates sub-tasks.
func (s *scheduler) handlePlannerOutput(ctx context.Context, wf *Workflow, plannerTask *Task, output *AgentOutput) error {
	specs, err := parsePlannerOutput(output)
	if err != nil {
		return fmt.Errorf("parsing planner output: %w", err)
	}

	// Validate the DAG.
	if err := ValidateDAG(specs); err != nil {
		return fmt.Errorf("invalid plan DAG: %w", err)
	}

	// Check task count limit.
	if wf.TaskCount+len(specs) > wf.MaxTasks {
		return fmt.Errorf("task count limit exceeded: %d + %d > %d",
			wf.TaskCount, len(specs), wf.MaxTasks)
	}

	// Check depth limit.
	newDepth := plannerTask.Depth + 1
	if newDepth >= wf.MaxDepth {
		return fmt.Errorf("depth limit exceeded: %d >= %d", newDepth, wf.MaxDepth)
	}

	// Create task IDs.
	taskIDs := make([]uuid.UUID, len(specs))
	for i := range specs {
		taskIDs[i] = uuid.New()
	}

	// Resolve dependencies.
	deps := ResolveDependencies(specs, taskIDs)

	// Create tasks.
	now := time.Now().UTC()
	for i, spec := range specs {
		if err := ValidateRole(spec.AgentRole); err != nil {
			return fmt.Errorf("task %d: %w", i, err)
		}
		mode := spec.Mode
		if mode == "" {
			mode = TaskModeSequential
		}

		task := &Task{
			ID:           taskIDs[i],
			WorkflowID:   wf.ID,
			OrgID:        wf.OrgID,
			ParentTaskID: &plannerTask.ID,
			AgentRole:    spec.AgentRole,
			Description:  spec.Description,
			Input:        spec.Input,
			Status:       TaskPending,
			Mode:         mode,
			Depth:        newDepth,
			Priority:     spec.Priority,
			DependsOn:    deps[i],
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := s.store.CreateTask(ctx, task); err != nil {
			return fmt.Errorf("creating task %d: %w", i, err)
		}

		// Save assignment message.
		msg := &AgentMessage{
			ID:            uuid.New(),
			WorkflowID:    wf.ID,
			FromTaskID:    plannerTask.ID,
			ToTaskID:      &taskIDs[i],
			FromRole:      RolePlanner,
			ToRole:        spec.AgentRole,
			MessageType:   MsgTaskAssignment,
			Content:       spec.Input,
			CorrelationID: wf.CorrelationID,
			CreatedAt:     now,
		}
		_ = s.store.SaveMessage(ctx, msg)
	}

	// Update workflow task count.
	wf.TaskCount += len(specs)
	wf.UpdatedAt = now
	return s.store.UpdateWorkflow(ctx, wf)
}

// finishWorkflow marks the workflow as completed or failed.
func (s *scheduler) finishWorkflow(ctx context.Context, wf *Workflow, status WorkflowStatus, errMsg string) error {
	now := time.Now().UTC()
	wf.Status = status
	wf.CompletedAt = &now
	wf.UpdatedAt = now
	wf.Error = errMsg

	if err := s.store.UpdateWorkflow(ctx, wf); err != nil {
		return fmt.Errorf("finishing workflow: %w", err)
	}

	if s.metrics != nil {
		s.metrics.WorkflowsTotal.WithLabelValues(string(status)).Inc()
		if wf.CreatedAt.Before(now) {
			duration := now.Sub(wf.CreatedAt).Seconds()
			s.metrics.WorkflowDuration.WithLabelValues(string(status)).Observe(duration)
		}
	}

	s.logger.InfoContext(ctx, "workflow finished",
		slog.String("workflow_id", wf.ID.String()),
		slog.String("status", string(status)),
		slog.String("error", errMsg),
		slog.Int("task_count", wf.TaskCount),
		slog.Float64("budget_spent", wf.BudgetSpentUSD),
	)

	if errMsg != "" {
		return fmt.Errorf("workflow %s: %s", status, errMsg)
	}
	return nil
}

// cancelWorkflow marks the workflow and all pending tasks as cancelled.
func (s *scheduler) cancelWorkflow(ctx context.Context, wf *Workflow, cause error) error {
	// Cancel pending tasks.
	tasks, _ := s.store.ListTasksByWorkflow(ctx, wf.ID)
	now := time.Now().UTC()
	for i := range tasks {
		if tasks[i].Status == TaskPending || tasks[i].Status == TaskBlocked {
			tasks[i].Status = TaskSkipped
			tasks[i].UpdatedAt = now
			_ = s.store.UpdateTask(ctx, &tasks[i])
		}
	}

	wf.Status = WorkflowCancelled
	wf.CompletedAt = &now
	wf.UpdatedAt = now
	wf.Error = cause.Error()
	_ = s.store.UpdateWorkflow(ctx, wf)

	if s.metrics != nil {
		s.metrics.WorkflowsTotal.WithLabelValues(string(WorkflowCancelled)).Inc()
	}

	return fmt.Errorf("workflow cancelled: %w", cause)
}

// reorderBySkillScore reorders tasks within the same priority level
// using skill-based scoring. Tasks with lower priority values (higher
// priority) are always scheduled first; skill scoring only breaks ties.
func (s *scheduler) reorderBySkillScore(ctx context.Context, orgID uuid.UUID, tasks []Task) []Task {
	scores := s.skillScorer.ScoreTasks(ctx, orgID, tasks)
	if len(scores) == 0 {
		return tasks
	}

	scoreMap := make(map[uuid.UUID]float64, len(scores))
	for _, sc := range scores {
		scoreMap[sc.TaskID] = sc.Score
	}

	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority < tasks[j].Priority
		}
		// Higher score = scheduled first.
		si := scoreMap[tasks[i].ID]
		sj := scoreMap[tasks[j].ID]
		return si > sj
	})

	return tasks
}

// countByStatus counts tasks with the given status.
func countByStatus(tasks []Task, status TaskStatus) int {
	count := 0
	for _, t := range tasks {
		if t.Status == status {
			count++
		}
	}
	return count
}
