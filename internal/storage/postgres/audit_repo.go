package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/security"
)

// AuditRepository implements security.AuditStore with PostgreSQL.
// Append-only: no Update or Delete methods exist on this type.
type AuditRepository struct {
	db *gorm.DB
}

// NewAuditRepository creates an AuditRepository.
func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Append inserts a single audit event. This is the only write method —
// immutability is enforced at the interface level.
func (r *AuditRepository) Append(ctx context.Context, orgID uuid.UUID, event security.AuditEvent) error {
	model := toAuditModel(orgID, event)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("appending audit event: %w", err)
	}
	return nil
}

// Query returns audit events for an org, newest first.
// If userID is non-empty, filters to that user. Limit defaults to 100.
func (r *AuditRepository) Query(ctx context.Context, orgID uuid.UUID, userID string, limit int) ([]security.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	q := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("created_at DESC").
		Limit(limit)

	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}

	var models []AuditEventModel
	if err := q.Find(&models).Error; err != nil {
		return nil, fmt.Errorf("querying audit events: %w", err)
	}

	events := make([]security.AuditEvent, len(models))
	for i := range models {
		events[i] = toAuditDomain(&models[i])
	}
	return events, nil
}
