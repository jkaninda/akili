package orchestrator

import (
	"context"

	"github.com/google/uuid"
)

// WorkflowEngine is the public API for multi-agent workflow orchestration.
type WorkflowEngine interface {
	// Submit creates a new workflow from a user goal and begins execution.
	Submit(ctx context.Context, req *WorkflowRequest) (*Workflow, error)

	// Status returns the current state of a workflow.
	Status(ctx context.Context, workflowID uuid.UUID) (*Workflow, error)

	// Cancel requests cancellation of a running workflow.
	Cancel(ctx context.Context, workflowID uuid.UUID) error

	// ListTasks returns all tasks for a workflow.
	ListTasks(ctx context.Context, workflowID uuid.UUID) ([]Task, error)
}

// WorkflowRequest is the input to create a new workflow.
type WorkflowRequest struct {
	UserID         string
	OrgID          uuid.UUID
	Goal           string
	CorrelationID  string
	BudgetLimitUSD float64 // 0 = use default from config.
	MaxDepth       int     // 0 = use default from config.
	MaxTasks       int     // 0 = use default from config.
}

// WorkflowStore persists workflow and task state.
// Implementations: postgres-backed or in-memory.
type WorkflowStore interface {
	CreateWorkflow(ctx context.Context, wf *Workflow) error
	UpdateWorkflow(ctx context.Context, wf *Workflow) error
	GetWorkflow(ctx context.Context, id uuid.UUID) (*Workflow, error)

	CreateTask(ctx context.Context, task *Task) error
	UpdateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, id uuid.UUID) (*Task, error)
	ListTasksByWorkflow(ctx context.Context, workflowID uuid.UUID) ([]Task, error)
	// GetReadyTasks returns tasks whose dependencies are all completed
	// and whose status is pending or blocked.
	GetReadyTasks(ctx context.Context, workflowID uuid.UUID) ([]Task, error)

	SaveMessage(ctx context.Context, msg *AgentMessage) error
	ListMessages(ctx context.Context, workflowID uuid.UUID) ([]AgentMessage, error)

	// ClaimReadyTasks atomically finds tasks across all workflows whose
	// dependencies are met and whose status is "pending", then transitions
	// them to "running" with the given agentID. Only tasks matching the
	// given roles are returned (empty roles = all roles).
	// Returns at most limit tasks.
	ClaimReadyTasks(ctx context.Context, agentID string, roles []string, limit int) ([]Task, error)

	// HeartbeatTasks refreshes the claim timestamp for the given task IDs,
	// preventing other agents from reclaiming them as stale.
	HeartbeatTasks(ctx context.Context, agentID string, taskIDs []uuid.UUID) error
}

// AgentFactory creates configured agent instances for each role.
type AgentFactory interface {
	// Create returns a RoleAgent configured for the given role with
	// the appropriate system prompt, tool subset, and security context.
	Create(role AgentRole) (RoleAgent, error)
}

// RoleAgent is a role-scoped agent that processes tasks within a workflow.
type RoleAgent interface {
	// Role returns the agent's role.
	Role() AgentRole

	// Process executes the agent's role-specific logic for a task.
	Process(ctx context.Context, input *AgentInput) (*AgentOutput, error)
}

// AgentInput is the input for a role-scoped agent invocation.
type AgentInput struct {
	WorkflowID    uuid.UUID
	TaskID        uuid.UUID
	UserID        string
	CorrelationID string
	Messages      []AgentMessage // Context from prior agents.
	Instruction   string         // The specific task instruction.
}

// AgentOutput is the result from a role-scoped agent invocation.
type AgentOutput struct {
	Response     string         // The agent's textual response.
	SubTasks     []TaskSpec     // Tasks to spawn (planner/orchestrator roles).
	ToolRequests []ToolSpec     // Tools to invoke (executor role).
	TokensUsed   int
	CostUSD      float64
	Metadata     map[string]any
}
