package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jkaninda/akili/internal/security"
)

// BudgetRepository implements security.BudgetStore with PostgreSQL.
// Uses SELECT ... FOR UPDATE for atomic budget reservation.
type BudgetRepository struct {
	db   *gorm.DB
	user *UserRepository
}

// NewBudgetRepository creates a BudgetRepository.
func NewBudgetRepository(db *gorm.DB, userRepo *UserRepository) *BudgetRepository {
	return &BudgetRepository{db: db, user: userRepo}
}

// CheckBudget returns the remaining budget for a user.
// Creates the budget record if absent, using defaultLimit.
func (r *BudgetRepository) CheckBudget(ctx context.Context, orgID uuid.UUID, externalUserID string, defaultLimit float64) (float64, error) {
	userID, err := r.user.EnsureUser(ctx, orgID, externalUserID)
	if err != nil {
		return 0, err
	}

	budget, err := r.getOrCreateBudget(ctx, r.db, orgID, userID, defaultLimit)
	if err != nil {
		return 0, err
	}

	// Sum active reservations.
	var activeReserved float64
	err = r.db.WithContext(ctx).
		Model(&BudgetReservationModel{}).
		Where("budget_id = ? AND released_at IS NULL", budget.ID).
		Select("COALESCE(SUM(amount_usd), 0)").
		Scan(&activeReserved).Error
	if err != nil {
		return 0, fmt.Errorf("summing reservations: %w", err)
	}

	return budget.LimitUSD - budget.SpentUSD - activeReserved, nil
}

// ReserveBudget atomically checks available budget and creates a reservation.
// Uses SELECT ... FOR UPDATE to serialize concurrent reservations.
func (r *BudgetRepository) ReserveBudget(ctx context.Context, orgID uuid.UUID, externalUserID string, cost float64, defaultLimit float64) (uuid.UUID, error) {
	userID, err := r.user.EnsureUser(ctx, orgID, externalUserID)
	if err != nil {
		return uuid.Nil, err
	}

	var reservationID uuid.UUID
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Lock the budget row.
		budget, err := r.getOrCreateBudget(ctx, tx, orgID, userID, defaultLimit)
		if err != nil {
			return err
		}

		// Re-fetch with FOR UPDATE lock.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&budget, "id = ?", budget.ID).Error; err != nil {
			return fmt.Errorf("locking budget: %w", err)
		}

		// 2. Sum active reservations.
		var activeReserved float64
		if err := tx.Model(&BudgetReservationModel{}).
			Where("budget_id = ? AND released_at IS NULL", budget.ID).
			Select("COALESCE(SUM(amount_usd), 0)").
			Scan(&activeReserved).Error; err != nil {
			return fmt.Errorf("summing reservations: %w", err)
		}

		// 3. Check remaining.
		remaining := budget.LimitUSD - budget.SpentUSD - activeReserved
		if cost > remaining {
			return fmt.Errorf("%w: cannot reserve $%.4f, remaining $%.4f for user %q",
				security.ErrBudgetExceeded, cost, remaining, externalUserID)
		}

		// 4. Insert reservation.
		reservationID = uuid.New()
		res := BudgetReservationModel{
			ID:        reservationID,
			OrgID:     orgID,
			BudgetID:  budget.ID,
			AmountUSD: cost,
		}
		return tx.Create(&res).Error
	})

	return reservationID, err
}

// ReleaseReservation marks a reservation as released without recording spend.
func (r *BudgetRepository) ReleaseReservation(ctx context.Context, reservationID uuid.UUID) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).
		Model(&BudgetReservationModel{}).
		Where("id = ? AND released_at IS NULL", reservationID).
		Update("released_at", now)
	if result.Error != nil {
		return fmt.Errorf("releasing reservation: %w", result.Error)
	}
	return nil
}

// RecordCost adds actualCost to the budget's spent_usd and releases the reservation.
func (r *BudgetRepository) RecordCost(ctx context.Context, reservationID uuid.UUID, actualCost float64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Find the reservation.
		var res BudgetReservationModel
		if err := tx.First(&res, "id = ?", reservationID).Error; err != nil {
			return fmt.Errorf("finding reservation: %w", err)
		}

		// Update budget spent.
		if err := tx.Model(&BudgetModel{}).
			Where("id = ?", res.BudgetID).
			Update("spent_usd", gorm.Expr("spent_usd + ?", actualCost)).Error; err != nil {
			return fmt.Errorf("updating spent: %w", err)
		}

		// Release the reservation.
		now := time.Now().UTC()
		return tx.Model(&res).Update("released_at", now).Error
	})
}

// getOrCreateBudget returns the budget for a user, creating it if absent.
func (r *BudgetRepository) getOrCreateBudget(ctx context.Context, tx *gorm.DB, orgID, userID uuid.UUID, defaultLimit float64) (BudgetModel, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)

	var budget BudgetModel
	err := tx.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("user_id = ? AND period_start = ?", userID, today).
		First(&budget).Error

	if err == nil {
		return budget, nil
	}
	if err != gorm.ErrRecordNotFound {
		return BudgetModel{}, fmt.Errorf("looking up budget: %w", err)
	}

	budget = BudgetModel{
		ID:          uuid.New(),
		OrgID:       orgID,
		UserID:      userID,
		LimitUSD:    defaultLimit,
		SpentUSD:    0,
		PeriodStart: today,
	}
	if err := tx.WithContext(ctx).Create(&budget).Error; err != nil {
		return BudgetModel{}, fmt.Errorf("creating budget: %w", err)
	}
	return budget, nil
}
