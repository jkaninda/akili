package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/soul"
)

// SoulRepository implements soul.SoulStore with PostgreSQL/SQLite via GORM.
type SoulRepository struct {
	db *gorm.DB
}

// NewSoulRepository creates a SoulRepository.
func NewSoulRepository(db *gorm.DB) *SoulRepository {
	return &SoulRepository{db: db}
}

func (r *SoulRepository) AppendEvent(ctx context.Context, event *soul.SoulEvent) error {
	model := toSoulEventModel(event)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("appending soul event: %w", err)
	}
	return nil
}

func (r *SoulRepository) ListEvents(ctx context.Context, orgID uuid.UUID) ([]soul.SoulEvent, error) {
	var models []SoulEventModel
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("version ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("listing soul events: %w", err)
	}
	events := make([]soul.SoulEvent, len(models))
	for i := range models {
		events[i] = toSoulEventDomain(&models[i])
	}
	return events, nil
}

func (r *SoulRepository) ListEventsSince(ctx context.Context, orgID uuid.UUID, afterVersion int) ([]soul.SoulEvent, error) {
	var models []SoulEventModel
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND version > ?", orgID, afterVersion).
		Order("version ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("listing soul events since v%d: %w", afterVersion, err)
	}
	events := make([]soul.SoulEvent, len(models))
	for i := range models {
		events[i] = toSoulEventDomain(&models[i])
	}
	return events, nil
}

func (r *SoulRepository) LatestVersion(ctx context.Context, orgID uuid.UUID) (int, error) {
	var maxVersion *int
	err := r.db.WithContext(ctx).
		Model(&SoulEventModel{}).
		Where("org_id = ?", orgID).
		Select("MAX(version)").
		Scan(&maxVersion).Error
	if err != nil {
		return 0, fmt.Errorf("getting latest soul version: %w", err)
	}
	if maxVersion == nil {
		return 0, nil
	}
	return *maxVersion, nil
}

func (r *SoulRepository) CountEvents(ctx context.Context, orgID uuid.UUID) (int, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&SoulEventModel{}).
		Where("org_id = ?", orgID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("counting soul events: %w", err)
	}
	return int(count), nil
}

func toSoulEventModel(e *soul.SoulEvent) SoulEventModel {
	return SoulEventModel{
		ID:          e.ID,
		OrgID:       e.OrgID,
		Version:     e.Version,
		EventType:   string(e.EventType),
		Severity:    string(e.Severity),
		Category:    e.Category,
		Title:       e.Title,
		Description: e.Description,
		Evidence:    e.Evidence,
		PriorState:  e.PriorState,
		CreatedAt:   e.CreatedAt,
	}
}

func toSoulEventDomain(m *SoulEventModel) soul.SoulEvent {
	return soul.SoulEvent{
		ID:          m.ID,
		OrgID:       m.OrgID,
		Version:     m.Version,
		EventType:   soul.EventType(m.EventType),
		Severity:    soul.EventSeverity(m.Severity),
		Category:    m.Category,
		Title:       m.Title,
		Description: m.Description,
		Evidence:    m.Evidence,
		PriorState:  m.PriorState,
		CreatedAt:   m.CreatedAt,
	}
}

// Compile-time check.
var _ soul.SoulStore = (*SoulRepository)(nil)
