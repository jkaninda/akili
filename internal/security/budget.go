package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// userBudget tracks spending for a single user.
type userBudget struct {
	limit    float64
	spent    float64
	reserved float64
}

// BudgetManager enforces per-user budget limits with a reservation pattern.
// All state is in-memory (resets on restart). Thread-safe.
type BudgetManager struct {
	mu              sync.Mutex
	defaultLimitUSD float64
	users           map[string]*userBudget
	logger          *slog.Logger
}

// NewBudgetManager creates a budget manager with the given default per-user limit.
func NewBudgetManager(defaultLimitUSD float64, logger *slog.Logger) *BudgetManager {
	return &BudgetManager{
		defaultLimitUSD: defaultLimitUSD,
		users:           make(map[string]*userBudget),
		logger:          logger,
	}
}

// CheckBudget verifies that the estimated cost fits within the user's remaining budget.
func (b *BudgetManager) CheckBudget(ctx context.Context, userID string, estimatedCost float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	ub := b.getOrCreate(userID)
	remaining := ub.limit - ub.spent - ub.reserved

	if estimatedCost > remaining {
		b.logger.WarnContext(ctx, "budget exceeded",
			slog.String("user_id", userID),
			slog.Float64("estimated_cost", estimatedCost),
			slog.Float64("remaining", remaining),
			slog.Float64("limit", ub.limit),
		)
		return fmt.Errorf("%w: estimated $%.4f exceeds remaining $%.4f for user %q",
			ErrBudgetExceeded, estimatedCost, remaining, userID)
	}

	return nil
}

// ReserveBudget atomically reserves an estimated cost before execution.
// Returns an idempotent release function to call if execution is abandoned.
// This prevents TOCTOU races between check and execution.
func (b *BudgetManager) ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ub := b.getOrCreate(userID)
	remaining := ub.limit - ub.spent - ub.reserved

	if estimatedCost > remaining {
		return nil, fmt.Errorf("%w: cannot reserve $%.4f, remaining $%.4f for user %q",
			ErrBudgetExceeded, estimatedCost, remaining, userID)
	}

	ub.reserved += estimatedCost

	b.logger.InfoContext(ctx, "budget reserved",
		slog.String("user_id", userID),
		slog.Float64("reserved", estimatedCost),
		slog.Float64("total_reserved", ub.reserved),
	)

	released := false
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !released {
			ub.reserved -= estimatedCost
			released = true
		}
	}, nil
}

// RecordCost records actual spend after execution, converting the reservation to real spend.
func (b *BudgetManager) RecordCost(ctx context.Context, userID string, actualCost float64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ub := b.getOrCreate(userID)
	ub.spent += actualCost

	// Remove the reservation that this cost replaces.
	if ub.reserved >= actualCost {
		ub.reserved -= actualCost
	} else {
		ub.reserved = 0
	}

	b.logger.InfoContext(ctx, "budget cost recorded",
		slog.String("user_id", userID),
		slog.Float64("actual_cost", actualCost),
		slog.Float64("total_spent", ub.spent),
		slog.Float64("remaining", ub.limit-ub.spent-ub.reserved),
	)
}

// Remaining returns the available budget for a user (limit - spent - reserved).
func (b *BudgetManager) Remaining(userID string) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	ub := b.getOrCreate(userID)
	return ub.limit - ub.spent - ub.reserved
}

func (b *BudgetManager) getOrCreate(userID string) *userBudget {
	ub, ok := b.users[userID]
	if !ok {
		ub = &userBudget{limit: b.defaultLimitUSD}
		b.users[userID] = ub
	}
	return ub
}
