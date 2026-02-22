package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/security"
)

// Engine implements WorkflowEngine. It is the main entry point for
// multi-agent workflow orchestration, building on top of existing
// agent.Agent instances via the AgentFactory.
type Engine struct {
	store        WorkflowStore
	factory      AgentFactory
	security     security.SecurityManager
	metrics      *WorkflowMetrics
	skillTracker *SkillTracker
	skillScorer  *SkillScorer
	logger       *slog.Logger
	config       EngineConfig

	mu      sync.Mutex
	cancels map[uuid.UUID]context.CancelFunc // Active workflow cancellation functions.
}

// NewEngine creates a workflow engine with the given components.
func NewEngine(
	store WorkflowStore,
	factory AgentFactory,
	security security.SecurityManager,
	metrics *WorkflowMetrics,
	logger *slog.Logger,
	config EngineConfig,
) *Engine {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Engine{
		store:    store,
		factory:  factory,
		security: security,
		metrics:  metrics,
		logger:   logger,
		config:   config,
		cancels:  make(map[uuid.UUID]context.CancelFunc),
	}
}

// WithSkills attaches the skill intelligence layer to the engine.
// Both tracker and scorer are nil-safe (no-op if nil).
func (e *Engine) WithSkills(tracker *SkillTracker, scorer *SkillScorer) *Engine {
	e.skillTracker = tracker
	e.skillScorer = scorer
	return e
}

func (e *Engine) Submit(ctx context.Context, req *WorkflowRequest) (*Workflow, error) {
	now := time.Now().UTC()

	// Apply defaults.
	budgetLimit := req.BudgetLimitUSD
	if budgetLimit <= 0 {
		budgetLimit = e.config.budgetLimit()
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = e.config.maxDepth()
	}
	maxTasks := req.MaxTasks
	if maxTasks <= 0 {
		maxTasks = e.config.maxTasks()
	}

	wf := &Workflow{
		ID:             uuid.New(),
		OrgID:          req.OrgID,
		UserID:         req.UserID,
		CorrelationID:  req.CorrelationID,
		Goal:           req.Goal,
		Status:         WorkflowPending,
		BudgetLimitUSD: budgetLimit,
		MaxDepth:       maxDepth,
		MaxTasks:       maxTasks,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := e.store.CreateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("creating workflow: %w", err)
	}

	// Create root task (planner).
	rootTask := &Task{
		ID:          uuid.New(),
		WorkflowID:  wf.ID,
		OrgID:       wf.OrgID,
		AgentRole:   RolePlanner,
		Description: "Decompose goal into sub-tasks",
		Input:       req.Goal,
		Status:      TaskPending,
		Mode:        TaskModeSequential,
		Depth:       0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := e.store.CreateTask(ctx, rootTask); err != nil {
		return nil, fmt.Errorf("creating root task: %w", err)
	}

	wf.RootTaskID = &rootTask.ID
	wf.TaskCount = 1
	wf.Status = WorkflowRunning
	wf.UpdatedAt = now
	if err := e.store.UpdateWorkflow(ctx, wf); err != nil {
		return nil, fmt.Errorf("updating workflow with root task: %w", err)
	}

	if e.metrics != nil {
		e.metrics.ActiveWorkflows.Inc()
	}

	e.logger.InfoContext(ctx, "workflow submitted",
		slog.String("workflow_id", wf.ID.String()),
		slog.String("user_id", req.UserID),
		slog.String("correlation_id", req.CorrelationID),
		slog.String("goal", req.Goal),
		slog.Int("max_depth", maxDepth),
		slog.Int("max_tasks", maxTasks),
		slog.Float64("budget_limit", budgetLimit),
	)

	// When external agents are configured, tasks are picked up by agent pollers.
	// Otherwise, run the inline scheduler in a background goroutine.
	if !e.config.ExternalAgents {
		wfCtx, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.cancels[wf.ID] = cancel
		e.mu.Unlock()

		go func() {
			defer func() {
				cancel()
				e.mu.Lock()
				delete(e.cancels, wf.ID)
				e.mu.Unlock()
				if e.metrics != nil {
					e.metrics.ActiveWorkflows.Dec()
				}
			}()

			sched := &scheduler{
				store: e.store,
				executor: &taskExecutor{
					store:        e.store,
					factory:      e.factory,
					metrics:      e.metrics,
					skillTracker: e.skillTracker,
					logger:       e.logger,
				},
				skillScorer: e.skillScorer,
				config:      e.config,
				logger:      e.logger,
				metrics:     e.metrics,
			}

			if err := sched.run(wfCtx, wf); err != nil {
				e.logger.WarnContext(ctx, "workflow finished with error",
					slog.String("workflow_id", wf.ID.String()),
					slog.String("error", err.Error()),
				)
			}
		}()
	}

	return wf, nil
}

func (e *Engine) Status(ctx context.Context, workflowID uuid.UUID) (*Workflow, error) {
	return e.store.GetWorkflow(ctx, workflowID)
}

func (e *Engine) Cancel(ctx context.Context, workflowID uuid.UUID) error {
	e.mu.Lock()
	cancel, ok := e.cancels[workflowID]
	e.mu.Unlock()

	if !ok {
		// Workflow may have already completed.
		wf, err := e.store.GetWorkflow(ctx, workflowID)
		if err != nil {
			return fmt.Errorf("workflow not found: %w", err)
		}
		if wf.Status == WorkflowRunning {
			return fmt.Errorf("workflow %s is running but cancel function not found", workflowID)
		}
		return nil // Already finished.
	}

	cancel()
	e.logger.InfoContext(ctx, "workflow cancellation requested",
		slog.String("workflow_id", workflowID.String()),
	)
	return nil
}

func (e *Engine) ListTasks(ctx context.Context, workflowID uuid.UUID) ([]Task, error) {
	return e.store.ListTasksByWorkflow(ctx, workflowID)
}

// Compile-time check.
var _ WorkflowEngine = (*Engine)(nil)
