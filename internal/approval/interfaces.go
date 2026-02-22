package approval

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ApprovalStore is the persistence contract for approval records.
// Implementations must enforce the state machine:
//   - Pending -> Approved
//   - Pending -> Denied
//   - Pending -> Expired
//
// Once Approved/Denied/Expired, status is immutable.
type ApprovalStore interface {
	// Create persists a new pending approval and returns its ID.
	Create(ctx context.Context, orgID uuid.UUID, req *CreateRequest, ttl time.Duration) (id string, err error)
	// Get retrieves an approval by ID, marking it expired if past ExpiresAt.
	Get(ctx context.Context, id string) (*PendingApproval, error)
	// Approve transitions a pending approval to StatusApproved.
	Approve(ctx context.Context, id, approverID string) error
	// Deny transitions a pending approval to StatusDenied.
	Deny(ctx context.Context, id, denierID string) error
	// ExpireOld bulk-updates status to expired for all pending rows where expires_at < now().
	ExpireOld(ctx context.Context) error
	// DeleteResolved removes resolved/expired rows older than the given age.
	DeleteResolved(ctx context.Context, olderThan time.Duration) error
}
