package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/approval"
)

// ApprovalRepository implements approval.ApprovalStore with PostgreSQL.
type ApprovalRepository struct {
	db *gorm.DB
}

// NewApprovalRepository creates an ApprovalRepository.
func NewApprovalRepository(db *gorm.DB) *ApprovalRepository {
	return &ApprovalRepository{db: db}
}

// Create persists a new pending approval and returns its ID.
func (r *ApprovalRepository) Create(ctx context.Context, orgID uuid.UUID, req *approval.CreateRequest, ttl time.Duration) (string, error) {
	id, err := generateApprovalID()
	if err != nil {
		return "", fmt.Errorf("generating approval ID: %w", err)
	}

	model := toApprovalModel(orgID, req, id, ttl)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return "", fmt.Errorf("creating approval: %w", err)
	}
	return id, nil
}

// Get retrieves an approval by ID, marking it expired if past ExpiresAt.
func (r *ApprovalRepository) Get(ctx context.Context, id string) (*approval.PendingApproval, error) {
	var model ApprovalModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, approval.ErrNotFound
		}
		return nil, fmt.Errorf("getting approval: %w", err)
	}

	// Mark as expired on access if past TTL.
	if model.Status == int16(approval.StatusPending) && time.Now().UTC().After(model.ExpiresAt) {
		r.db.WithContext(ctx).Model(&model).Update("status", int16(approval.StatusExpired))
		model.Status = int16(approval.StatusExpired)
	}

	return toApprovalDomain(&model), nil
}

// Approve transitions a pending approval to StatusApproved.
func (r *ApprovalRepository) Approve(ctx context.Context, id, approverID string) error {
	return r.resolve(ctx, id, approverID, approval.StatusApproved)
}

// Deny transitions a pending approval to StatusDenied.
func (r *ApprovalRepository) Deny(ctx context.Context, id, denierID string) error {
	return r.resolve(ctx, id, denierID, approval.StatusDenied)
}

func (r *ApprovalRepository) resolve(ctx context.Context, id, resolverID string, status approval.Status) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model ApprovalModel
		if err := tx.First(&model, "id = ?", id).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return approval.ErrNotFound
			}
			return err
		}

		if time.Now().UTC().After(model.ExpiresAt) && model.Status == int16(approval.StatusPending) {
			tx.Model(&model).Update("status", int16(approval.StatusExpired))
			return approval.ErrExpired
		}

		if model.Status != int16(approval.StatusPending) {
			return approval.ErrAlreadyResolved
		}

		now := time.Now().UTC()
		return tx.Model(&model).Updates(map[string]any{
			"status":      int16(status),
			"approved_by": resolverID,
			"resolved_at": now,
		}).Error
	})
}

// ExpireOld bulk-updates status to expired for all pending rows past expires_at.
func (r *ApprovalRepository) ExpireOld(ctx context.Context) error {
	return r.db.WithContext(ctx).
		Model(&ApprovalModel{}).
		Where("status = ? AND expires_at < ?", int16(approval.StatusPending), time.Now().UTC()).
		Update("status", int16(approval.StatusExpired)).Error
}

// DeleteResolved removes resolved/expired rows older than the given age.
func (r *ApprovalRepository) DeleteResolved(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan)
	return r.db.WithContext(ctx).
		Where("status != ? AND created_at < ?", int16(approval.StatusPending), cutoff).
		Delete(&ApprovalModel{}).Error
}

func generateApprovalID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
