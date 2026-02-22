package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
)

// NotificationChannelRepository implements notification channel persistence with PostgreSQL.
type NotificationChannelRepository struct {
	db *gorm.DB
}

// NewNotificationChannelRepository creates a NotificationChannelRepository.
func NewNotificationChannelRepository(db *gorm.DB) *NotificationChannelRepository {
	return &NotificationChannelRepository{db: db}
}

// Create persists a new notification channel.
func (r *NotificationChannelRepository) Create(ctx context.Context, ch *domain.NotificationChannel) error {
	model := toNotificationChannelModel(ch)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("creating notification channel: %w", err)
	}
	return nil
}

// Get retrieves a notification channel by ID within an org.
func (r *NotificationChannelRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.NotificationChannel, error) {
	var model NotificationChannelModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		First(&model, "id = ?", id).Error; err != nil {
		return nil, fmt.Errorf("getting notification channel %s: %w", id, err)
	}
	return toNotificationChannelDomain(&model), nil
}

// GetByName retrieves a notification channel by name within an org.
func (r *NotificationChannelRepository) GetByName(ctx context.Context, orgID uuid.UUID, name string) (*domain.NotificationChannel, error) {
	var model NotificationChannelModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		First(&model, "name = ?", name).Error; err != nil {
		return nil, fmt.Errorf("getting notification channel %q: %w", name, err)
	}
	return toNotificationChannelDomain(&model), nil
}

// List returns all notification channels for an org.
func (r *NotificationChannelRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.NotificationChannel, error) {
	var models []NotificationChannelModel
	if err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Order("created_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing notification channels: %w", err)
	}
	channels := make([]domain.NotificationChannel, len(models))
	for i := range models {
		channels[i] = *toNotificationChannelDomain(&models[i])
	}
	return channels, nil
}

// Update persists changes to an existing notification channel.
func (r *NotificationChannelRepository) Update(ctx context.Context, ch *domain.NotificationChannel) error {
	model := toNotificationChannelModel(ch)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("updating notification channel: %w", err)
	}
	return nil
}

// Delete soft-deletes a notification channel by ID within an org.
func (r *NotificationChannelRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Delete(&NotificationChannelModel{}, "id = ?", id)
	if result.Error != nil {
		return fmt.Errorf("deleting notification channel %s: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("notification channel %s not found", id)
	}
	return nil
}
