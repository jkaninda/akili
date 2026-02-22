package approval

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type DBManager struct {
	store  ApprovalStore
	orgID  uuid.UUID
	ttl    time.Duration
	logger *slog.Logger
}

func NewDBManager(store ApprovalStore, orgID uuid.UUID, ttl time.Duration, logger *slog.Logger) *DBManager {
	return &DBManager{
		store:  store,
		orgID:  orgID,
		ttl:    ttl,
		logger: logger,
	}
}

// Create stores a new pending approval and returns its unique ID.
func (m *DBManager) Create(ctx context.Context, req *CreateRequest) (string, error) {
	id, err := m.store.Create(ctx, m.orgID, req, m.ttl)
	if err != nil {
		return "", err
	}

	m.logger.Info("approval created (pg)",
		slog.String("approval_id", id),
		slog.String("user_id", req.UserID),
		slog.String("tool", req.ToolName),
		slog.String("action", req.ActionName),
		slog.String("risk", req.RiskLevel),
	)

	return id, nil
}

// Get retrieves a pending approval by ID.
func (m *DBManager) Get(ctx context.Context, id string) (*PendingApproval, error) {
	return m.store.Get(ctx, id)
}

// Approve marks a pending approval as approved.
func (m *DBManager) Approve(ctx context.Context, id, approverID string) error {
	err := m.store.Approve(ctx, id, approverID)
	if err == nil {
		m.logger.Info("approval approved (pg)",
			slog.String("approval_id", id),
			slog.String("approver", approverID),
		)
	}
	return err
}

// Deny marks a pending approval as denied.
func (m *DBManager) Deny(ctx context.Context, id, denierID string) error {
	err := m.store.Deny(ctx, id, denierID)
	if err == nil {
		m.logger.Info("approval denied (pg)",
			slog.String("approval_id", id),
			slog.String("denier", denierID),
		)
	}
	return err
}

// StartCleanup starts a background goroutine that expires old approvals
// and deletes resolved entries periodically.
func (m *DBManager) StartCleanup(ctx context.Context, interval time.Duration) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.store.ExpireOld(ctx); err != nil {
					m.logger.Error("expiring approvals", slog.String("error", err.Error()))
				}
				if err := m.store.DeleteResolved(ctx, 2*m.ttl); err != nil {
					m.logger.Error("deleting resolved approvals", slog.String("error", err.Error()))
				}
			}
		}
	}()
	return cancel
}
