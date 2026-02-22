// Package protocol defines the WebSocket message types for Gateway ↔ Agent communication.
// All messages are JSON-encoded and wrapped in an Envelope for uniform routing.
package protocol

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MessageType identifies the kind of message in the WebSocket protocol.
type MessageType string

const (
	// Agent → Gateway
	MsgAgentRegister   MessageType = "agent.register"
	MsgAgentHeartbeat  MessageType = "agent.heartbeat"
	MsgTaskAccepted    MessageType = "task.accepted"
	MsgTaskProgress    MessageType = "task.progress"
	MsgTaskResult      MessageType = "task.result"
	MsgTaskFailed      MessageType = "task.failed"
	MsgApprovalRequest MessageType = "approval.request"

	// Gateway → Agent
	MsgRegistered     MessageType = "gateway.registered"
	MsgTaskAssign     MessageType = "task.assign"
	MsgTaskCancel     MessageType = "task.cancel"
	MsgApprovalResult MessageType = "approval.result"
	MsgPing           MessageType = "gateway.ping"
	MsgPong           MessageType = "gateway.pong"

	// Bidirectional
	MsgError MessageType = "error"
)

// Envelope is the top-level message wrapper for all WebSocket communication.
// Every message sent between Gateway and Agent is wrapped in an Envelope.
type Envelope struct {
	Type      MessageType     `json:"type"`
	ID        string          `json:"id"` // Message ID for correlation and deduplication.
	AgentID   string          `json:"agent_id,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// NewEnvelope creates an Envelope with a fresh ID and current timestamp.
func NewEnvelope(msgType MessageType, payload any) (*Envelope, error) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return &Envelope{
		Type:      msgType,
		ID:        uuid.New().String(),
		Payload:   raw,
		Timestamp: time.Now().UTC(),
	}, nil
}

// Decode unmarshals the Payload into the given target.
func (e *Envelope) Decode(target any) error {
	return json.Unmarshal(e.Payload, target)
}

// --- Agent → Gateway payloads ---

// AgentCapabilities is sent with MsgAgentRegister when an agent connects.
type AgentCapabilities struct {
	AgentID     string   `json:"agent_id"`
	Skills      []string `json:"skills"`
	Model       string   `json:"model"`
	MaxParallel int      `json:"max_parallel"`
	Version     string   `json:"version"`
}

// HeartbeatPayload is sent with MsgAgentHeartbeat periodically.
type HeartbeatPayload struct {
	ActiveTasks int `json:"active_tasks"`
}

// TaskAcceptedPayload is sent with MsgTaskAccepted when an agent starts working on a task.
type TaskAcceptedPayload struct {
	TaskID string `json:"task_id"`
}

// TaskProgress is sent with MsgTaskProgress to stream progress back to the user.
type TaskProgress struct {
	Message    string  `json:"message"`
	TokensUsed int     `json:"tokens_used"`
	CostUSD    float64 `json:"cost_usd"`
}

// TaskResultPayload is sent with MsgTaskResult when an agent completes a task.
type TaskResultPayload struct {
	Output     string  `json:"output"`
	TokensUsed int     `json:"tokens_used"`
	CostUSD    float64 `json:"cost_usd"`
	ToolCalls  int     `json:"tool_calls"`
	Duration   string  `json:"duration"`
}

// TaskFailedPayload is sent with MsgTaskFailed when an agent fails a task.
type TaskFailedPayload struct {
	Error      string  `json:"error"`
	TokensUsed int     `json:"tokens_used"`
	CostUSD    float64 `json:"cost_usd"`
}

// ApprovalRequestPayload is sent with MsgApprovalRequest when a tool requires human approval.
type ApprovalRequestPayload struct {
	ApprovalID string          `json:"approval_id"`
	UserID     string          `json:"user_id"`
	ToolName   string          `json:"tool_name"`
	Parameters json.RawMessage `json:"parameters"`
	RiskLevel  string          `json:"risk_level"`
}

// --- Gateway → Agent payloads ---

// RegisteredPayload is sent with MsgRegistered to confirm agent registration.
type RegisteredPayload struct {
	Message string `json:"message"`
}

// TaskAssignment is sent with MsgTaskAssign to assign a task to an agent.
type TaskAssignment struct {
	TaskID      string            `json:"task_id"`
	WorkflowID  string            `json:"workflow_id,omitempty"`
	UserID      string            `json:"user_id"`
	Goal        string            `json:"goal"`
	SkillName   string            `json:"skill_name,omitempty"`
	Context     map[string]string `json:"context,omitempty"`
	BudgetUSD   float64           `json:"budget_usd"`
	TimeoutSecs int               `json:"timeout_secs"`
}

// TaskCancelPayload is sent with MsgTaskCancel to cancel an in-progress task.
type TaskCancelPayload struct {
	Reason string `json:"reason"`
}

// ApprovalResultPayload is sent with MsgApprovalResult to relay the human decision.
type ApprovalResultPayload struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"` // "approve" or "deny"
	ApproverID string `json:"approver_id"`
}

// ErrorPayload is sent with MsgError for protocol-level errors.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
