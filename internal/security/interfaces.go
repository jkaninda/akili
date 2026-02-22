package security

import (
	"context"

	"github.com/google/uuid"
)

// RoleStore provides persistent storage for RBAC configuration.
// Implementations must be safe for concurrent use.
type RoleStore interface {
	// LoadRBACConfig loads the full RBAC config for an organization.
	LoadRBACConfig(ctx context.Context, orgID uuid.UUID) (RBACConfig, error)
	// SaveRole creates or updates a role and its permissions within an org.
	SaveRole(ctx context.Context, orgID uuid.UUID, role Role) error
	// AssignUserRole sets the role for a user within an organization.
	AssignUserRole(ctx context.Context, orgID uuid.UUID, externalUserID, roleName string) error
}

// BudgetStore provides persistent budget tracking with transactional semantics.
// Implementations use database transactions to prevent TOCTOU races.
type BudgetStore interface {
	// CheckBudget returns the remaining budget for a user.
	// Creates the budget record if absent, using defaultLimit.
	CheckBudget(ctx context.Context, orgID uuid.UUID, externalUserID string, defaultLimit float64) (remaining float64, err error)
	// ReserveBudget atomically checks available budget and creates a reservation.
	// Returns a reservationID on success, or ErrBudgetExceeded if insufficient.
	ReserveBudget(ctx context.Context, orgID uuid.UUID, externalUserID string, cost float64, defaultLimit float64) (reservationID uuid.UUID, err error)
	// ReleaseReservation marks a reservation as released without recording spend.
	ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error
	// RecordCost adds actualCost to spent and releases the associated reservation.
	RecordCost(ctx context.Context, reservationID uuid.UUID, actualCost float64) error
}

// AuditStore is an append-only store for audit events.
// No update or delete methods — immutability enforced at the interface level.
type AuditStore interface {
	// Append writes a single audit event. Never updates or deletes.
	Append(ctx context.Context, orgID uuid.UUID, event AuditEvent) error
}
