package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/orchestrator"
)

// WorkflowRepository implements orchestrator.WorkflowStore with PostgreSQL.
type WorkflowRepository struct {
	db *gorm.DB
}

// NewWorkflowRepository creates a WorkflowRepository.
func NewWorkflowRepository(db *gorm.DB) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

func (r *WorkflowRepository) CreateWorkflow(ctx context.Context, wf *orchestrator.Workflow) error {
	model := toWorkflowModel(wf)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating workflow: %w", err)
	}
	return nil
}

func (r *WorkflowRepository) UpdateWorkflow(ctx context.Context, wf *orchestrator.Workflow) error {
	model := toWorkflowModel(wf)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating workflow: %w", err)
	}
	return nil
}

func (r *WorkflowRepository) GetWorkflow(ctx context.Context, id uuid.UUID) (*orchestrator.Workflow, error) {
	var model WorkflowModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("getting workflow %s: %w", id, err)
	}
	return toWorkflowDomain(&model), nil
}

func (r *WorkflowRepository) CreateTask(ctx context.Context, task *orchestrator.Task) error {
	model := toTaskModel(task)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating task: %w", err)
	}
	return nil
}

func (r *WorkflowRepository) UpdateTask(ctx context.Context, task *orchestrator.Task) error {
	model := toTaskModel(task)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating task: %w", err)
	}
	return nil
}

func (r *WorkflowRepository) GetTask(ctx context.Context, id uuid.UUID) (*orchestrator.Task, error) {
	var model TaskModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("getting task %s: %w", id, err)
	}
	return toTaskDomain(&model), nil
}

func (r *WorkflowRepository) ListTasksByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]orchestrator.Task, error) {
	var models []TaskModel
	if err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("created_at ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing tasks for workflow %s: %w", workflowID, err)
	}
	tasks := make([]orchestrator.Task, len(models))
	for i := range models {
		tasks[i] = *toTaskDomain(&models[i])
	}
	return tasks, nil
}

func (r *WorkflowRepository) GetReadyTasks(ctx context.Context, workflowID uuid.UUID) ([]orchestrator.Task, error) {
	// Load all tasks for the workflow, then compute readiness in-memory.
	// For workflows with ≤50 tasks (the default limit), this is efficient.
	var models []TaskModel
	if err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("loading tasks for readiness check: %w", err)
	}

	// Build completed set.
	completed := make(map[uuid.UUID]bool)
	type candidate struct {
		task *orchestrator.Task
	}
	var candidates []candidate
	for i := range models {
		t := toTaskDomain(&models[i])
		if t.Status == orchestrator.TaskCompleted {
			completed[t.ID] = true
		}
		if t.Status == orchestrator.TaskPending || t.Status == orchestrator.TaskBlocked {
			candidates = append(candidates, candidate{task: t})
		}
	}

	var ready []orchestrator.Task
	for _, c := range candidates {
		allDepsMet := true
		for _, depID := range c.task.DependsOn {
			if !completed[depID] {
				allDepsMet = false
				break
			}
		}
		if allDepsMet {
			ready = append(ready, *c.task)
		}
	}

	return ready, nil
}

func (r *WorkflowRepository) SaveMessage(ctx context.Context, msg *orchestrator.AgentMessage) error {
	model := toAgentMessageModel(msg)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("saving agent message: %w", err)
	}
	return nil
}

func (r *WorkflowRepository) ListMessages(ctx context.Context, workflowID uuid.UUID) ([]orchestrator.AgentMessage, error) {
	var models []AgentMessageModel
	if err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("created_at ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing messages for workflow %s: %w", workflowID, err)
	}
	msgs := make([]orchestrator.AgentMessage, len(models))
	for i := range models {
		msgs[i] = *toAgentMessageDomain(&models[i])
	}
	return msgs, nil
}

func (r *WorkflowRepository) ClaimReadyTasks(ctx context.Context, agentID string, roles []string, limit int) ([]orchestrator.Task, error) {
	// Load all tasks across running workflows, compute readiness in-memory,
	// then atomically claim them. This approach reuses the same readiness
	// logic as GetReadyTasks but works across all workflows.
	var allModels []TaskModel
	query := r.db.WithContext(ctx).
		Joins("JOIN workflows ON workflows.id = tasks.workflow_id AND workflows.status = 'running'").
		Where("tasks.status = 'pending'").
		Where("tasks.claimed_by = '' OR tasks.claimed_by IS NULL")

	if len(roles) > 0 {
		query = query.Where("tasks.agent_role IN ?", roles)
	}

	if err := query.Find(&allModels).Error; err != nil {
		return nil, fmt.Errorf("loading candidate tasks: %w", err)
	}

	if len(allModels) == 0 {
		return nil, nil
	}

	// Build completed set per workflow for dependency checking.
	workflowIDs := make(map[uuid.UUID]bool)
	for i := range allModels {
		workflowIDs[allModels[i].WorkflowID] = true
	}

	completedByWorkflow := make(map[uuid.UUID]map[uuid.UUID]bool)
	for wfID := range workflowIDs {
		var wfTasks []TaskModel
		if err := r.db.WithContext(ctx).
			Where("workflow_id = ? AND status = 'completed'", wfID).
			Find(&wfTasks).Error; err != nil {
			continue
		}
		completed := make(map[uuid.UUID]bool)
		for _, t := range wfTasks {
			completed[t.ID] = true
		}
		completedByWorkflow[wfID] = completed
	}

	// Filter to truly ready tasks (all deps met).
	var readyIDs []uuid.UUID
	for i := range allModels {
		m := &allModels[i]
		completed := completedByWorkflow[m.WorkflowID]

		var deps []uuid.UUID
		if len(m.DependsOn) > 0 {
			_ = json.Unmarshal(m.DependsOn, &deps)
		}

		allDepsMet := true
		for _, depID := range deps {
			if !completed[depID] {
				allDepsMet = false
				break
			}
		}
		if allDepsMet {
			readyIDs = append(readyIDs, m.ID)
			if len(readyIDs) >= limit {
				break
			}
		}
	}

	if len(readyIDs) == 0 {
		return nil, nil
	}

	// Atomically claim the ready tasks.
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&TaskModel{}).
		Where("id IN ? AND status = 'pending'", readyIDs).
		Updates(map[string]any{
			"status":     "running",
			"claimed_by": agentID,
			"claimed_at": now,
			"started_at": now,
			"updated_at": now,
		})
	if result.Error != nil {
		return nil, fmt.Errorf("claiming tasks: %w", result.Error)
	}

	// Load the claimed tasks back.
	var claimed []TaskModel
	if err := r.db.WithContext(ctx).
		Where("id IN ? AND claimed_by = ?", readyIDs, agentID).
		Order("priority ASC, created_at ASC").
		Find(&claimed).Error; err != nil {
		return nil, fmt.Errorf("loading claimed tasks: %w", err)
	}

	tasks := make([]orchestrator.Task, len(claimed))
	for i := range claimed {
		tasks[i] = *toTaskDomain(&claimed[i])
	}
	return tasks, nil
}

func (r *WorkflowRepository) HeartbeatTasks(ctx context.Context, agentID string, taskIDs []uuid.UUID) error {
	if len(taskIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&TaskModel{}).
		Where("id IN ? AND claimed_by = ?", taskIDs, agentID).
		Update("claimed_at", time.Now().UTC()).
		Error
}

// Compile-time check.
var _ orchestrator.WorkflowStore = (*WorkflowRepository)(nil)
