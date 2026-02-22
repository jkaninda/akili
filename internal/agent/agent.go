// Package agent defines the core agent interface and domain types.
package agent

import (
	"context"
	"fmt"

	"github.com/jkaninda/akili/internal/security"
)

// Agent processes user inputs through the LLM and executes tools with security enforcement.
type Agent interface {
	// Process sends a user message to the LLM and returns the response.
	Process(ctx context.Context, input *Input) (*Response, error)

	// ExecuteTool runs a named tool through the full security pipeline:
	ExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error)

	// ResumeWithApproval resumes a tool execution that was blocked on approval.
	ResumeWithApproval(ctx context.Context, approvalID string) (*ToolResponse, error)
}

// Input represents a user request entering the agent.
type Input struct {
	UserID         string
	Message        string
	CorrelationID  string
	ConversationID string
}

// DefaultMaxIterations is the safety guard against infinite tool-use loops.
const DefaultMaxIterations = 25

// Response is the agent's output after LLM processing.
type Response struct {
	Message          string
	TokensUsed       int
	ApprovalRequests []ApprovalRequest // Pending approvals that need user action.
	ToolResults      []ToolCallResult  // Summary of tools executed during processing.
}

// ToolCallResult summarizes a single tool execution within the agentic loop.
type ToolCallResult struct {
	ToolName string
	Success  bool
}

// ApprovalRequest is surfaced to gateways when a tool execution needs human approval.
type ApprovalRequest struct {
	ApprovalID string
	ToolName   string
	ActionName string
	RiskLevel  string
	UserID     string
}

// ToolRequest is the input for a security-checked tool execution.
type ToolRequest struct {
	UserID         string
	ToolName       string
	Parameters     map[string]any
	CorrelationID  string
	ConversationID string // Set during Process() for approval state persistence.
	ToolUseID      string // LLM tool_use block ID, used to update pending tool_result after approval.
}

// ToolResponse is the result of a tool execution after all security checks pass.
type ToolResponse struct {
	Output   string
	Success  bool
	CostUSD  float64
	Metadata map[string]any
}

// ErrApprovalPending is returned by ExecuteTool when an action requires
// human approval.
type ErrApprovalPending struct {
	ApprovalID string
	ToolName   string
	ActionName string
	RiskLevel  string
	UserID     string
}

func (e *ErrApprovalPending) Error() string {
	return fmt.Sprintf("approval pending (id=%s) for %s by user %s", e.ApprovalID, e.ActionName, e.UserID)
}

// Unwrap returns ErrApprovalRequired so that errors.Is(err, ErrApprovalRequired) works.
func (e *ErrApprovalPending) Unwrap() error {
	return security.ErrApprovalRequired
}
