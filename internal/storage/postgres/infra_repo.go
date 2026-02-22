package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/infra"
)

// InfraNodeRepository implements infra.Store with PostgreSQL.
type InfraNodeRepository struct {
	db *gorm.DB
}

// NewInfraNodeRepository creates a PostgreSQL-backed infrastructure store.
func NewInfraNodeRepository(db *gorm.DB) *InfraNodeRepository {
	return &InfraNodeRepository{db: db}
}

func (r *InfraNodeRepository) Lookup(ctx context.Context, orgID uuid.UUID, query string) (*domain.InfraNode, error) {
	var model InfraNodeModel

	// Try by UUID first.
	if id, err := uuid.Parse(query); err == nil {
		if err := r.db.WithContext(ctx).
			Scopes(TenantScope(orgID)).
			Where("enabled = ?", true).
			First(&model, "id = ?", id).Error; err == nil {
			return toInfraNodeDomain(&model), nil
		}
	}

	// Try by name (case-insensitive).
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("enabled = ? AND LOWER(name) = LOWER(?)", true, query).
		First(&model).Error; err == nil {
		return toInfraNodeDomain(&model), nil
	}

	// Try by alias (JSONB array contains, case-insensitive).
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("enabled = ? AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(aliases) a WHERE LOWER(a) = LOWER(?))",
			true, query).
		First(&model).Error; err == nil {
		return toInfraNodeDomain(&model), nil
	}

	return nil, fmt.Errorf("%w: %q", infra.ErrNodeNotFound, query)
}

func (r *InfraNodeRepository) List(ctx context.Context, orgID uuid.UUID, nodeType string) ([]domain.InfraNode, error) {
	var models []InfraNodeModel
	q := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("enabled = ?", true)
	if nodeType != "" {
		q = q.Where("node_type = ?", nodeType)
	}
	if err := q.Order("name ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing infra nodes: %w", err)
	}
	nodes := make([]domain.InfraNode, len(models))
	for i := range models {
		nodes[i] = *toInfraNodeDomain(&models[i])
	}
	return nodes, nil
}

func (r *InfraNodeRepository) Create(ctx context.Context, node *domain.InfraNode) error {
	if node.ID == uuid.Nil {
		node.ID = uuid.New()
	}
	now := time.Now().UTC()
	node.CreatedAt = now
	node.UpdatedAt = now
	model := toInfraNodeModel(node)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating infra node: %w", err)
	}
	return nil
}

func (r *InfraNodeRepository) Update(ctx context.Context, node *domain.InfraNode) error {
	node.UpdatedAt = time.Now().UTC()
	model := toInfraNodeModel(node)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating infra node: %w", err)
	}
	return nil
}

func (r *InfraNodeRepository) Delete(ctx context.Context, orgID, nodeID uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Delete(&InfraNodeModel{}, "id = ?", nodeID)
	if result.Error != nil {
		return fmt.Errorf("deleting infra node %s: %w", nodeID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", infra.ErrNodeNotFound, nodeID)
	}
	return nil
}
