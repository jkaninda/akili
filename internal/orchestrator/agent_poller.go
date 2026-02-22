package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentPollerConfig configures the headless task poller.
type AgentPollerConfig struct {
	AgentID            string
	Roles              []AgentRole
	PollInterval       time.Duration
	MaxConcurrentTasks int
	TaskTimeout        time.Duration
	HeartbeatInterval  time.Duration
}

// AgentPoller polls the database for ready tasks and executes them.
// It is the agent-mode equivalent of the scheduler, but operates
// across all workflows rather than within a single one.
type AgentPoller struct {
	store    WorkflowStore
	executor *taskExecutor
	config   AgentPollerConfig
	logger   *slog.Logger
	metrics  *WorkflowMetrics
}

// NewAgentPoller creates an agent poller.
func NewAgentPoller(
	store WorkflowStore,
	factory AgentFactory,
	metrics *WorkflowMetrics,
	logger *slog.Logger,
	config AgentPollerConfig,
) *AgentPoller {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &AgentPoller{
		store: store,
		executor: &taskExecutor{
			store:   store,
			factory: factory,
			metrics: metrics,
			logger:  logger,
		},
		config:  config,
		logger:  logger,
		metrics: metrics,
	}
}

// WithSkillTracker attaches skill tracking to the poller's task executor.
func (p *AgentPoller) WithSkillTracker(tracker *SkillTracker) *AgentPoller {
	p.executor.skillTracker = tracker
	return p
}

// Run starts the polling loop. Blocks until ctx is canceled.
func (p *AgentPoller) Run(ctx context.Context) error {
	p.logger.InfoContext(ctx, "agent poller started",
		slog.String("agent_id", p.config.AgentID),
		slog.Any("roles", p.config.Roles),
		slog.Duration("poll_interval", p.config.PollInterval),
		slog.Int("max_concurrent", p.config.MaxConcurrentTasks),
	)

	// Semaphore for concurrency limiting.
	sem := make(chan struct{}, p.config.MaxConcurrentTasks)

	// Track running tasks for heartbeat.
	var mu sync.Mutex
	runningTasks := make(map[uuid.UUID]struct{})

	// Start heartbeat goroutine.
	go p.heartbeatLoop(ctx, &mu, runningTasks)

	ticker := time.NewTicker(p.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.InfoContext(ctx, "agent poller stopping, waiting for in-flight tasks")
			// Wait for in-flight tasks to complete.
			for i := 0; i < p.config.MaxConcurrentTasks; i++ {
				sem <- struct{}{}
			}
			return nil
		case <-ticker.C:
			// Compute available slots.
			mu.Lock()
			available := p.config.MaxConcurrentTasks - len(runningTasks)
			mu.Unlock()

			if available <= 0 {
				continue
			}

			// Convert roles to string slice.
			roleStrs := make([]string, len(p.config.Roles))
			for i, r := range p.config.Roles {
				roleStrs[i] = string(r)
			}

			tasks, err := p.store.ClaimReadyTasks(ctx, p.config.AgentID, roleStrs, available)
			if err != nil {
				p.logger.ErrorContext(ctx, "claiming tasks failed",
					slog.String("error", err.Error()),
				)
				continue
			}

			if len(tasks) == 0 {
				continue
			}

			p.logger.InfoContext(ctx, "claimed tasks",
				slog.Int("count", len(tasks)),
			)

			for i := range tasks {
				task := tasks[i]

				mu.Lock()
				runningTasks[task.ID] = struct{}{}
				mu.Unlock()

				sem <- struct{}{} // Acquire slot.

				go func(t Task) {
					defer func() {
						<-sem // Release slot.
						mu.Lock()
						delete(runningTasks, t.ID)
						mu.Unlock()
					}()

					p.executeTask(ctx, &t)
				}(task)
			}
		}
	}
}

// executeTask runs a single claimed task within its workflow context.
func (p *AgentPoller) executeTask(ctx context.Context, task *Task) {
	// Load the workflow for context.
	wf, err := p.store.GetWorkflow(ctx, task.WorkflowID)
	if err != nil {
		p.logger.ErrorContext(ctx, "loading workflow for task",
			slog.String("task_id", task.ID.String()),
			slog.String("error", err.Error()),
		)
		return
	}

	taskCtx, cancel := context.WithTimeout(ctx, p.config.TaskTimeout)
	defer cancel()

	output, err := p.executor.executeTask(taskCtx, wf, task)
	if err != nil {
		p.logger.WarnContext(ctx, "task execution failed",
			slog.String("task_id", task.ID.String()),
			slog.String("agent_role", string(task.AgentRole)),
			slog.String("error", err.Error()),
		)
		p.maybeFinishWorkflow(ctx, wf)
		return
	}

	// Handle planner output (create sub-tasks).
	if task.AgentRole == RolePlanner && output != nil {
		sched := &scheduler{
			store:   p.store,
			logger:  p.logger,
			metrics: p.metrics,
		}
		if err := sched.handlePlannerOutput(ctx, wf, task, output); err != nil {
			p.logger.WarnContext(ctx, "handling planner output",
				slog.String("task_id", task.ID.String()),
				slog.String("error", err.Error()),
			)
		}
	}

	// Accumulate cost on workflow.
	if output != nil && output.CostUSD > 0 {
		p.updateWorkflowCost(ctx, wf, output.CostUSD)
	}

	p.maybeFinishWorkflow(ctx, wf)
}

// maybeFinishWorkflow checks whether a workflow should be marked complete/failed.
func (p *AgentPoller) maybeFinishWorkflow(ctx context.Context, wf *Workflow) {
	// Reload latest workflow state.
	latest, err := p.store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		return
	}

	allTasks, err := p.store.ListTasksByWorkflow(ctx, wf.ID)
	if err != nil {
		return
	}

	running := countByStatus(allTasks, TaskRunning)
	pending := countByStatus(allTasks, TaskPending)
	blocked := countByStatus(allTasks, TaskBlocked)

	if running > 0 || pending > 0 || blocked > 0 {
		return // Still in progress.
	}

	failed := countByStatus(allTasks, TaskFailed)
	sched := &scheduler{store: p.store, logger: p.logger, metrics: p.metrics}

	if failed > 0 {
		_ = sched.finishWorkflow(ctx, latest, WorkflowFailed,
			fmt.Sprintf("%d task(s) failed", failed))
	} else {
		_ = sched.finishWorkflow(ctx, latest, WorkflowCompleted, "")
	}
}

// updateWorkflowCost adds cost to a workflow's budget spent.
func (p *AgentPoller) updateWorkflowCost(ctx context.Context, wf *Workflow, costUSD float64) {
	latest, err := p.store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		return
	}
	latest.BudgetSpentUSD += costUSD
	latest.UpdatedAt = time.Now().UTC()
	_ = p.store.UpdateWorkflow(ctx, latest)
}

// heartbeatLoop periodically refreshes claim timestamps for running tasks.
func (p *AgentPoller) heartbeatLoop(ctx context.Context, mu *sync.Mutex, running map[uuid.UUID]struct{}) {
	ticker := time.NewTicker(p.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mu.Lock()
			ids := make([]uuid.UUID, 0, len(running))
			for id := range running {
				ids = append(ids, id)
			}
			mu.Unlock()

			if len(ids) == 0 {
				continue
			}

			if err := p.store.HeartbeatTasks(ctx, p.config.AgentID, ids); err != nil {
				p.logger.WarnContext(ctx, "heartbeat failed",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}
