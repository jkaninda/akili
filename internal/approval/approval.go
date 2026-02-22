// Package approval implements an in-memory approval manager for tool executions
// that require human sign-off before proceeding.
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	ErrNotFound        = errors.New("approval not found")
	ErrExpired         = errors.New("approval expired")
	ErrAlreadyResolved = errors.New("approval already resolved")
)

// Status represents the state of an approval request.
type Status int

const (
	StatusPending Status = iota
	StatusApproved
	StatusDenied
	StatusExpired
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusApproved:
		return "approved"
	case StatusDenied:
		return "denied"
	case StatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// PendingApproval stores the full context needed to resume tool execution
// after approval is granted.
type PendingApproval struct {
	ID             string
	UserID         string // Who triggered the action.
	ToolName       string
	Parameters     map[string]any
	ActionName     string // Security action name (for audit).
	RiskLevel      string // For display in approval UI.
	EstimatedCost  float64
	CorrelationID  string
	ConversationID string // Conversation to update after execution.
	ToolUseID      string // LLM tool_use block ID for updating the pending tool_result.
	Status         Status
	ApprovedBy     string // Set when approved or denied.
	CreatedAt      time.Time
	ExpiresAt      time.Time
	ResolvedAt     time.Time // Set when approved or denied.
}

// CreateRequest contains the fields needed to create a pending approval.
type CreateRequest struct {
	UserID         string
	ToolName       string
	Parameters     map[string]any
	ActionName     string
	RiskLevel      string
	EstimatedCost  float64
	CorrelationID  string
	ConversationID string // Conversation to update after execution.
	ToolUseID      string // LLM tool_use block ID for updating the pending tool_result.
}

// ApprovalManager is the public contract for the approval workflow.
// Both the in-memory *Manager and the postgres-backed *DBManager satisfy this.
type ApprovalManager interface {
	Create(ctx context.Context, req *CreateRequest) (string, error)
	Get(ctx context.Context, id string) (*PendingApproval, error)
	Approve(ctx context.Context, id, approverID string) error
	Deny(ctx context.Context, id, denierID string) error
	StartCleanup(ctx context.Context, interval time.Duration) func()
}

// Manager stores pending approval requests in memory.
// Thread-safe. Approvals expire after a configurable TTL.
type Manager struct {
	mu      sync.Mutex
	pending map[string]*PendingApproval
	ttl     time.Duration
	logger  *slog.Logger
}

// NewManager creates an approval manager with the given default TTL.
func NewManager(ttl time.Duration, logger *slog.Logger) *Manager {
	return &Manager{
		pending: make(map[string]*PendingApproval),
		ttl:     ttl,
		logger:  logger,
	}
}

// Create stores a new pending approval and returns its unique ID.
func (m *Manager) Create(_ context.Context, req *CreateRequest) (string, error) {
	id, err := generateID()
	if err != nil {
		return "", fmt.Errorf("generating approval ID: %w", err)
	}

	now := time.Now().UTC()
	pa := &PendingApproval{
		ID:             id,
		UserID:         req.UserID,
		ToolName:       req.ToolName,
		Parameters:     req.Parameters,
		ActionName:     req.ActionName,
		RiskLevel:      req.RiskLevel,
		EstimatedCost:  req.EstimatedCost,
		CorrelationID:  req.CorrelationID,
		ConversationID: req.ConversationID,
		ToolUseID:      req.ToolUseID,
		Status:         StatusPending,
		CreatedAt:      now,
		ExpiresAt:      now.Add(m.ttl),
	}

	m.mu.Lock()
	m.pending[id] = pa
	m.mu.Unlock()

	m.logger.Info("approval created",
		slog.String("approval_id", id),
		slog.String("user_id", req.UserID),
		slog.String("tool", req.ToolName),
		slog.String("action", req.ActionName),
		slog.String("risk", req.RiskLevel),
	)

	return id, nil
}

// Approve marks a pending approval as approved by the given approver.
func (m *Manager) Approve(_ context.Context, id, approverID string) error {
	return m.resolve(id, approverID, StatusApproved)
}

// Deny marks a pending approval as denied.
func (m *Manager) Deny(_ context.Context, id, denierID string) error {
	return m.resolve(id, denierID, StatusDenied)
}

func (m *Manager) resolve(id, resolverID string, status Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pa, ok := m.pending[id]
	if !ok {
		return ErrNotFound
	}

	if time.Now().UTC().After(pa.ExpiresAt) {
		pa.Status = StatusExpired
		return ErrExpired
	}

	if pa.Status != StatusPending {
		return ErrAlreadyResolved
	}

	pa.Status = status
	pa.ApprovedBy = resolverID
	pa.ResolvedAt = time.Now().UTC()

	m.logger.Info("approval resolved",
		slog.String("approval_id", id),
		slog.String("resolver", resolverID),
		slog.String("status", status.String()),
		slog.String("tool", pa.ToolName),
	)

	return nil
}

// Get retrieves a pending approval by ID.
func (m *Manager) Get(_ context.Context, id string) (*PendingApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pa, ok := m.pending[id]
	if !ok {
		return nil, ErrNotFound
	}

	// Mark as expired on access if past TTL.
	if pa.Status == StatusPending && time.Now().UTC().After(pa.ExpiresAt) {
		pa.Status = StatusExpired
	}

	return pa, nil
}

// Cleanup removes expired and old resolved approvals.
func (m *Manager) Cleanup(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	for id, pa := range m.pending {
		// Remove expired entries.
		if pa.Status == StatusPending && now.After(pa.ExpiresAt) {
			pa.Status = StatusExpired
		}
		// Remove anything resolved or expired more than 2x TTL ago.
		if pa.Status != StatusPending && now.After(pa.ExpiresAt.Add(m.ttl)) {
			delete(m.pending, id)
		}
	}
}

// StartCleanup starts a background goroutine that calls Cleanup periodically.
// Returns a cancel function to stop the goroutine.
func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Cleanup(ctx)
			}
		}
	}()
	return cancel
}

func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
