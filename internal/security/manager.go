package security

import (
	"context"
	"log/slog"
)

// rbacEnforcer is the RBAC check contract.
// Satisfied by *RBAC (in-memory) and *PGRBAC (postgres-backed).
type rbacEnforcer interface {
	CheckPermission(ctx context.Context, userID string, action Action) error
	RequireApproval(ctx context.Context, userID string, action Action) error
}

// auditAppender is the audit logging contract.
// Satisfied by *AuditLogger (JSONL file) and *PGAuditLogger (postgres-backed).
type auditAppender interface {
	LogAction(ctx context.Context, event AuditEvent) error
	Close() error
}

// budgetEnforcer is the budget enforcement contract.
// Satisfied by *BudgetManager (in-memory) and *PGBudgetManager (postgres-backed).
type budgetEnforcer interface {
	CheckBudget(ctx context.Context, userID string, estimatedCost float64) error
	ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error)
	RecordCost(ctx context.Context, userID string, actualCost float64)
}

// Manager composes RBAC, audit, and budget enforcement into a single SecurityManager.
// It holds no state of its own — each sub-component manages its own synchronization.
type Manager struct {
	rbac   rbacEnforcer
	audit  auditAppender
	budget budgetEnforcer
	logger *slog.Logger
}

// NewManager creates a fully composed security manager.
// The concrete types (*RBAC, *AuditLogger, *BudgetManager) satisfy the local
// interfaces, as do the postgres-backed adapters.
func NewManager(rbac rbacEnforcer, audit auditAppender, budget budgetEnforcer, logger *slog.Logger) *Manager {
	return &Manager{
		rbac:   rbac,
		audit:  audit,
		budget: budget,
		logger: logger,
	}
}

func (m *Manager) CheckPermission(ctx context.Context, userID string, action Action) error {
	return m.rbac.CheckPermission(ctx, userID, action)
}

func (m *Manager) RequireApproval(ctx context.Context, userID string, action Action) error {
	return m.rbac.RequireApproval(ctx, userID, action)
}

func (m *Manager) CheckBudget(ctx context.Context, userID string, estimatedCost float64) error {
	return m.budget.CheckBudget(ctx, userID, estimatedCost)
}

func (m *Manager) ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error) {
	return m.budget.ReserveBudget(ctx, userID, estimatedCost)
}

func (m *Manager) RecordCost(ctx context.Context, userID string, actualCost float64) {
	m.budget.RecordCost(ctx, userID, actualCost)
}

func (m *Manager) LogAction(ctx context.Context, event AuditEvent) error {
	return m.audit.LogAction(ctx, event)
}

// Close releases resources (closes the audit log file).
func (m *Manager) Close() error {
	return m.audit.Close()
}
