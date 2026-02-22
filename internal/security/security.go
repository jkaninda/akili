// Package security implements default-deny permission enforcement,
// RBAC, audit logging, and budget management for Akili.
package security

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors for security enforcement.
var (
	ErrPermissionDenied = errors.New("permission denied")
	ErrApprovalRequired = errors.New("approval required for high-risk action")
	ErrBudgetExceeded   = errors.New("budget limit exceeded")
)

// RiskLevel classifies the danger of an action.
type RiskLevel int

const (
	RiskLow      RiskLevel = iota // Read-only, no side effects.
	RiskMedium                    // Writes to scoped resources.
	RiskHigh                      // System changes, requires approval.
	RiskCritical                  // Destructive operations, always requires approval.
)

func (r RiskLevel) String() string {
	switch r {
	case RiskLow:
		return "low"
	case RiskMedium:
		return "medium"
	case RiskHigh:
		return "high"
	case RiskCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseRiskLevel converts a string to a RiskLevel.
// Unrecognized values default to RiskCritical (default-deny principle).
func ParseRiskLevel(s string) RiskLevel {
	switch s {
	case "low":
		return RiskLow
	case "medium":
		return RiskMedium
	case "high":
		return RiskHigh
	case "critical":
		return RiskCritical
	default:
		return RiskCritical
	}
}

// Action identifies a specific operation the agent wants to perform.
type Action struct {
	Name      string
	RiskLevel RiskLevel
}

// AuditEvent is a single entry in the append-only audit log.
type AuditEvent struct {
	Timestamp     time.Time      `json:"timestamp"`
	CorrelationID string         `json:"correlation_id"`
	UserID        string         `json:"user_id"`
	Action        string         `json:"action"`
	Tool          string         `json:"tool"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	Result        string         `json:"result"` // "intent", "success", "failure", "denied"
	TokensUsed    int            `json:"tokens_used,omitempty"`
	CostUSD       float64        `json:"cost_usd,omitempty"`
	ApprovedBy    string         `json:"approved_by,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// SecurityManager enforces all security checks before and after tool execution.
type SecurityManager interface {
	// CheckPermission verifies the user's role grants the action.
	// Returns ErrPermissionDenied if not.
	CheckPermission(ctx context.Context, userID string, action Action) error

	// RequireApproval checks if the action's risk level requires human approval.
	// Returns ErrApprovalRequired if approval is needed (no approval backend in Phase 2).
	RequireApproval(ctx context.Context, userID string, action Action) error

	// CheckBudget verifies the user has sufficient budget remaining.
	// Returns ErrBudgetExceeded if estimatedCost would exceed the limit.
	CheckBudget(ctx context.Context, userID string, estimatedCost float64) error

	// ReserveBudget atomically reserves an estimated cost before execution.
	// Returns a release function to call if execution is abandoned.
	ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (release func(), err error)

	// RecordCost records actual cost after execution, converting the reservation to spend.
	RecordCost(ctx context.Context, userID string, actualCost float64)

	// LogAction appends an audit event to the audit log.
	LogAction(ctx context.Context, event AuditEvent) error
}
