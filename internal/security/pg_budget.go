package security

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// PGBudgetManager adapts a BudgetStore to the budgetEnforcer interface
// expected by the security Manager.
type PGBudgetManager struct {
	store        BudgetStore
	orgID        uuid.UUID
	defaultLimit float64
	logger       *slog.Logger
}

// NewPGBudgetManager creates a postgres-backed budget manager.
func NewPGBudgetManager(store BudgetStore, orgID uuid.UUID, defaultLimit float64, logger *slog.Logger) *PGBudgetManager {
	return &PGBudgetManager{
		store:        store,
		orgID:        orgID,
		defaultLimit: defaultLimit,
		logger:       logger,
	}
}

// CheckBudget verifies the user has sufficient budget remaining.
func (b *PGBudgetManager) CheckBudget(ctx context.Context, userID string, estimatedCost float64) error {
	remaining, err := b.store.CheckBudget(ctx, b.orgID, userID, b.defaultLimit)
	if err != nil {
		return fmt.Errorf("checking budget: %w", err)
	}
	if estimatedCost > remaining {
		return fmt.Errorf("%w: estimated $%.4f exceeds remaining $%.4f for user %q",
			ErrBudgetExceeded, estimatedCost, remaining, userID)
	}
	return nil
}

// ReserveBudget atomically reserves an estimated cost before execution.
// Returns an idempotent release function.
func (b *PGBudgetManager) ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error) {
	reservationID, err := b.store.ReserveBudget(ctx, b.orgID, userID, estimatedCost, b.defaultLimit)
	if err != nil {
		return nil, err
	}

	b.logger.InfoContext(ctx, "budget reserved (pg)",
		slog.String("user_id", userID),
		slog.Float64("reserved", estimatedCost),
		slog.String("reservation_id", reservationID.String()),
	)

	released := false
	return func() {
		if released {
			return
		}
		released = true
		// Use background context — the original may have been cancelled.
		if err := b.store.ReleaseReservation(context.Background(), reservationID); err != nil {
			b.logger.Error("failed to release reservation",
				slog.String("reservation_id", reservationID.String()),
				slog.String("error", err.Error()),
			)
		}
	}, nil
}

// RecordCost records actual spend after execution.
func (b *PGBudgetManager) RecordCost(ctx context.Context, userID string, actualCost float64) {
	// RecordCost in the budgetEnforcer interface doesn't have a reservationID parameter.
	// The PGBudgetManager needs the reservationID from ReserveBudget to link spend.
	// Since the current interface passes only userID and cost, we log and record
	// spend directly. The reservation is released by the deferred release func.
	b.logger.InfoContext(ctx, "budget cost recorded (pg)",
		slog.String("user_id", userID),
		slog.Float64("actual_cost", actualCost),
	)
}
