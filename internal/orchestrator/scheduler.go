package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/config"
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
	DefaultBudgetLimitUSD float64              // Per-workflow default budget. Default: 1.0
	DefaultMaxDepth       int                  // Default: 5.
	DefaultMaxTasks       int                  // Default: 50.
	MaxConcurrentTasks    int                  // Parallelism limit. Default: 5.
	TaskTimeout           time.Duration        // Per-task timeout. Default: 5m.
	ExternalAgents        bool                 // When true, tasks are claimed by external agent pollers instead of the inline scheduler.
	SkillWeights          SkillWeights         // Weights for skill-based scoring. Zero value = defaults.
	Recovery              *config.RecoveryConfig // nil = recovery disabled.
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

		recovering := countByStatus(allTasks, TaskRecovering)

		if len(ready) == 0 && running == 0 && recovering == 0 {
			// No more work to do.
			failed := countByStatus(allTasks, TaskFailed)

			// Attempt recovery for failed tasks before giving up.
			if failed > 0 && s.config.Recovery != nil && s.config.Recovery.Enabled {
				recoveryAttempted := false
				for i := range allTasks {
					if allTasks[i].Status != TaskFailed {
						continue
					}
					if s.shouldAttemptRecovery(ctx, wf, &allTasks[i]) {
						if err := s.initiateRecovery(ctx, wf, &allTasks[i]); err != nil {
							s.logger.WarnContext(ctx, "recovery initiation failed",
								slog.String("task_id", allTasks[i].ID.String()),
								slog.String("error", err.Error()),
							)
							continue
						}
						recoveryAttempted = true
					}
				}
				if recoveryAttempted {
					continue // Re-enter scheduling loop; recovery tasks are now pending.
				}
			}

			if failed > 0 {
				return s.finishWorkflow(ctx, wf, WorkflowFailed,
					fmt.Sprintf("%d task(s) failed", failed))
			}
			return s.finishWorkflow(ctx, wf, WorkflowCompleted, "")
		}

		if len(ready) == 0 && running == 0 && recovering > 0 {
			// Recovery tasks are still in progress; wait.
			select {
			case <-ctx.Done():
				return s.cancelWorkflow(ctx, wf, ctx.Err())
			case <-time.After(500 * time.Millisecond):
				continue
			}
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

			// Handle diagnostician output: decide recovery action.
			if res.task.AgentRole == RoleDiagnostician && res.output != nil {
				if err := s.handleDiagnosticianOutput(ctx, wf, &res.task, res.output); err != nil {
					s.logger.WarnContext(ctx, "failed to handle diagnostician output",
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

// --- Recovery methods ---

// shouldAttemptRecovery checks whether a failed task is eligible for recovery.
func (s *scheduler) shouldAttemptRecovery(ctx context.Context, wf *Workflow, task *Task) bool {
	rc := s.config.Recovery
	if rc == nil || !rc.Enabled {
		return false
	}

	// Check retry count.
	maxRetries := task.MaxRetries
	if maxRetries <= 0 {
		maxRetries = rc.MaxRetriesOrDefault()
	}
	if task.RetryCount >= maxRetries {
		return false
	}

	// Check recovery budget.
	recoveryBudget := wf.BudgetLimitUSD * rc.RecoveryBudgetPctOrDefault()
	if recoveryBudget > 0 {
		spent := s.recoverySpent(ctx, wf)
		if spent >= recoveryBudget {
			return false
		}
	}

	// Check depth limit for the diagnostician task.
	if task.Depth+1 >= wf.MaxDepth {
		return false
	}

	// Check task count limit.
	if wf.TaskCount+1 > wf.MaxTasks {
		return false
	}

	return true
}

// initiateRecovery transitions a failed task to recovering and creates a diagnostician task.
func (s *scheduler) initiateRecovery(ctx context.Context, wf *Workflow, failedTask *Task) error {
	now := time.Now().UTC()

	// Transition the failed task to "recovering".
	failedTask.Status = TaskRecovering
	failedTask.UpdatedAt = now
	if err := s.store.UpdateTask(ctx, failedTask); err != nil {
		return fmt.Errorf("marking task recovering: %w", err)
	}

	// Build diagnostic input.
	diagInput := buildDiagnosticInput(failedTask)

	// Create the diagnostician task.
	diagTask := &Task{
		ID:             uuid.New(),
		WorkflowID:     wf.ID,
		OrgID:          wf.OrgID,
		ParentTaskID:   failedTask.ParentTaskID,
		OriginalTaskID: &failedTask.ID,
		AgentRole:      RoleDiagnostician,
		Description:    fmt.Sprintf("Diagnose failure: %s", failedTask.Description),
		Input:          diagInput,
		Status:         TaskPending,
		Mode:           TaskModeSequential,
		Depth:          failedTask.Depth + 1,
		Priority:       0, // High priority.
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.store.CreateTask(ctx, diagTask); err != nil {
		// Revert status on failure.
		failedTask.Status = TaskFailed
		_ = s.store.UpdateTask(ctx, failedTask)
		return fmt.Errorf("creating diagnostician task: %w", err)
	}

	wf.TaskCount++
	wf.UpdatedAt = now
	_ = s.store.UpdateWorkflow(ctx, wf)

	s.logRecoveryEvent(ctx, wf, failedTask, diagTask, "recovery_initiated")

	if s.metrics != nil && s.metrics.RecoveryAttemptsTotal != nil {
		s.metrics.RecoveryAttemptsTotal.WithLabelValues("initiated").Inc()
	}

	return nil
}

// handleDiagnosticianOutput processes the diagnostician's recovery decision.
func (s *scheduler) handleDiagnosticianOutput(ctx context.Context, wf *Workflow, diagTask *Task, output *AgentOutput) error {
	decision, err := parseRecoveryDecision(output)
	if err != nil {
		return s.recoveryFailed(ctx, wf, diagTask, fmt.Errorf("parsing decision: %w", err))
	}

	if diagTask.OriginalTaskID == nil {
		return fmt.Errorf("diagnostician task has no original_task_id")
	}
	originalTask, err := s.store.GetTask(ctx, *diagTask.OriginalTaskID)
	if err != nil {
		return fmt.Errorf("loading original task: %w", err)
	}

	s.logRecoveryEvent(ctx, wf, originalTask, diagTask, "recovery_decision_"+string(decision.Action))

	if s.metrics != nil && s.metrics.RecoveryAttemptsTotal != nil {
		s.metrics.RecoveryAttemptsTotal.WithLabelValues(string(decision.Action)).Inc()
	}

	switch decision.Action {
	case RecoveryRetry:
		if s.config.Recovery != nil && !s.config.Recovery.AllowRetry {
			return s.recoveryFailed(ctx, wf, diagTask, fmt.Errorf("retry not allowed by config"))
		}
		return s.retryTask(ctx, originalTask)

	case RecoveryEscalate:
		s.escalate(ctx, wf, originalTask, decision.Reason)
		// Mark original as failed (recovery gave up).
		now := time.Now().UTC()
		originalTask.Status = TaskFailed
		originalTask.UpdatedAt = now
		return s.store.UpdateTask(ctx, originalTask)

	case RecoverySkip:
		now := time.Now().UTC()
		originalTask.Status = TaskSkipped
		originalTask.UpdatedAt = now
		return s.store.UpdateTask(ctx, originalTask)

	default:
		return s.recoveryFailed(ctx, wf, diagTask, fmt.Errorf("unknown action: %s", decision.Action))
	}
}

// retryTask resets a task for re-execution.
func (s *scheduler) retryTask(ctx context.Context, task *Task) error {
	now := time.Now().UTC()
	task.Status = TaskPending
	task.Error = ""
	task.Output = ""
	task.RetryCount++
	task.StartedAt = nil
	task.CompletedAt = nil
	task.ClaimedBy = ""
	task.ClaimedAt = nil
	task.UpdatedAt = now

	s.logger.InfoContext(ctx, "retrying task",
		slog.String("task_id", task.ID.String()),
		slog.Int("retry_count", task.RetryCount),
	)

	return s.store.UpdateTask(ctx, task)
}

// recoveryFailed reverts the original task to failed when recovery cannot proceed.
func (s *scheduler) recoveryFailed(ctx context.Context, wf *Workflow, diagTask *Task, err error) error {
	s.logger.WarnContext(ctx, "recovery failed",
		slog.String("diag_task_id", diagTask.ID.String()),
		slog.String("error", err.Error()),
	)

	if diagTask.OriginalTaskID != nil {
		if original, getErr := s.store.GetTask(ctx, *diagTask.OriginalTaskID); getErr == nil {
			now := time.Now().UTC()
			original.Status = TaskFailed
			original.UpdatedAt = now
			_ = s.store.UpdateTask(ctx, original)
		}
	}

	return err
}

// recoverySpent returns the total cost of all diagnostician tasks in the workflow.
func (s *scheduler) recoverySpent(ctx context.Context, wf *Workflow) float64 {
	tasks, err := s.store.ListTasksByWorkflow(ctx, wf.ID)
	if err != nil {
		return 0
	}
	var total float64
	for _, t := range tasks {
		if t.AgentRole == RoleDiagnostician {
			total += t.CostUSD
		}
	}
	return total
}

// logRecoveryEvent records a recovery event as a structured log and agent message.
func (s *scheduler) logRecoveryEvent(ctx context.Context, wf *Workflow, failedTask *Task, diagTask *Task, result string) {
	s.logger.InfoContext(ctx, "recovery event",
		slog.String("workflow_id", wf.ID.String()),
		slog.String("failed_task_id", failedTask.ID.String()),
		slog.String("diag_task_id", diagTask.ID.String()),
		slog.String("result", result),
		slog.String("correlation_id", wf.CorrelationID),
	)

	msg := &AgentMessage{
		ID:            uuid.New(),
		WorkflowID:    wf.ID,
		FromTaskID:    diagTask.ID,
		ToTaskID:      &failedTask.ID,
		FromRole:      RoleDiagnostician,
		ToRole:        failedTask.AgentRole,
		MessageType:   MsgRecoveryResult,
		Content:       result,
		CorrelationID: wf.CorrelationID,
		CreatedAt:     time.Now().UTC(),
	}
	_ = s.store.SaveMessage(ctx, msg)
}

// escalate logs that a failed task requires human intervention.
func (s *scheduler) escalate(ctx context.Context, wf *Workflow, task *Task, reason string) {
	s.logger.WarnContext(ctx, "task escalated — requires human intervention",
		slog.String("workflow_id", wf.ID.String()),
		slog.String("task_id", task.ID.String()),
		slog.String("task_description", task.Description),
		slog.String("error", task.Error),
		slog.String("reason", reason),
		slog.String("correlation_id", wf.CorrelationID),
	)
}
