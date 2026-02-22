// Package orchestrator implements multi-agent workflow orchestration for Akili.
// It coordinates multiple agent roles (planner, researcher, executor, compliance)
// through a DAG-based task scheduler to decompose and execute complex goals.
//
// The orchestrator builds on top of the existing single-agent pkg/agent.Orchestrator,
// wrapping it with role-specific system prompts and tool subsets. The full security
// pipeline (RBAC, approval, budget, audit) is preserved per-agent.
package orchestrator

import (
	"time"

	"github.com/google/uuid"
)

// AgentRole identifies the function of an agent within a workflow.
type AgentRole string

const (
	RoleOrchestrator AgentRole = "orchestrator" // Coordinates workflow execution.
	RolePlanner      AgentRole = "planner"      // Decomposes goals into tasks.
	RoleResearcher   AgentRole = "researcher"   // Gathers information (read-only).
	RoleExecutor     AgentRole = "executor"     // Runs tools with side effects.
	RoleCompliance   AgentRole = "compliance"   // Validates security/policy.
)

// WorkflowStatus represents the lifecycle state of a workflow.
type WorkflowStatus string

const (
	WorkflowPending   WorkflowStatus = "pending"
	WorkflowRunning   WorkflowStatus = "running"
	WorkflowCompleted WorkflowStatus = "completed"
	WorkflowFailed    WorkflowStatus = "failed"
	WorkflowCancelled WorkflowStatus = "cancelled"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskBlocked   TaskStatus = "blocked" // Waiting on dependencies.
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskSkipped   TaskStatus = "skipped" // Skipped due to upstream failure.
)

// TaskMode controls how child tasks of a parent are scheduled.
type TaskMode string

const (
	TaskModeSequential TaskMode = "sequential"
	TaskModeParallel   TaskMode = "parallel"
)

// MessageType classifies inter-agent messages.
type MessageType string

const (
	MsgTaskAssignment   MessageType = "task_assignment"   // Orchestrator -> agent.
	MsgTaskResult       MessageType = "task_result"       // Agent -> orchestrator.
	MsgInfoRequest      MessageType = "info_request"      // Any -> researcher.
	MsgInfoResponse     MessageType = "info_response"     // Researcher -> requester.
	MsgComplianceCheck  MessageType = "compliance_check"  // Executor -> compliance.
	MsgComplianceResult MessageType = "compliance_result" // Compliance -> executor.
	MsgPlanProposal     MessageType = "plan_proposal"     // Planner -> orchestrator.
	MsgError            MessageType = "error"
)

// Workflow is the top-level unit of multi-agent work.
type Workflow struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	UserID         string
	CorrelationID  string
	Goal           string // The original user request.
	Status         WorkflowStatus
	RootTaskID     *uuid.UUID
	BudgetLimitUSD float64
	BudgetSpentUSD float64
	MaxDepth       int // Max nesting depth.
	MaxTasks       int // Max total tasks.
	TaskCount      int // Current count of tasks.
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
	Error          string
	Metadata       map[string]any
}

// Task is a single unit of work within a workflow DAG.
type Task struct {
	ID           uuid.UUID
	WorkflowID   uuid.UUID
	OrgID        uuid.UUID
	ParentTaskID *uuid.UUID
	AgentRole    AgentRole
	Description  string
	Input        string
	Output       string
	Status       TaskStatus
	Mode         TaskMode    // How this task's children execute.
	Depth        int         // Nesting depth (0 = root).
	Priority     int         // Lower = higher priority.
	DependsOn    []uuid.UUID // Task IDs that must complete before this runs.
	CreatedAt    time.Time
	UpdatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	Error        string
	CostUSD      float64
	TokensUsed   int
	ClaimedBy    string     // Agent instance ID that claimed this task.
	ClaimedAt    *time.Time // When the claim was made/refreshed.
	Metadata     map[string]any
}

// AgentMessage is the structured inter-agent communication format.
type AgentMessage struct {
	ID            uuid.UUID
	WorkflowID    uuid.UUID
	FromTaskID    uuid.UUID
	ToTaskID      *uuid.UUID // nil = broadcast.
	FromRole      AgentRole
	ToRole        AgentRole
	MessageType   MessageType
	Content       string
	Artifacts     []Artifact
	CorrelationID string
	CreatedAt     time.Time
}

// Artifact is a named data blob passed between agents.
type Artifact struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

// TaskSpec is a planner's description of a sub-task to create.
type TaskSpec struct {
	AgentRole   AgentRole `json:"agent_role"`
	Description string    `json:"description"`
	Input       string    `json:"input"`
	Mode        TaskMode  `json:"mode,omitempty"`       // Default: sequential.
	DependsOn   []int     `json:"depends_on,omitempty"` // Indices into the same []TaskSpec slice.
	Priority    int       `json:"priority,omitempty"`
}

// ToolSpec is an executor's request to run a tool.
type ToolSpec struct {
	ToolName   string         `json:"tool_name"`
	Parameters map[string]any `json:"parameters"`
}
