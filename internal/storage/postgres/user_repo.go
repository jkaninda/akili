package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
)

// UserRepository manages user records within an organization.
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a UserRepository.
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// EnsureUser creates a user if it doesn't exist and returns its internal UUID.
// Uses the combination of (org_id, external_id) as the unique key.
func (r *UserRepository) EnsureUser(ctx context.Context, orgID uuid.UUID, externalID string) (uuid.UUID, error) {
	var user UserModel
	err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("external_id = ?", externalID).
		First(&user).Error
	if err == nil {
		return user.ID, nil
	}
	if err != gorm.ErrRecordNotFound {
		return uuid.Nil, fmt.Errorf("looking up user %q: %w", externalID, err)
	}

	user = UserModel{
		ID:         uuid.New(),
		OrgID:      orgID,
		ExternalID: externalID,
	}
	if err := r.db.WithContext(ctx).Create(&user).Error; err != nil {
		return uuid.Nil, fmt.Errorf("creating user %q: %w", externalID, err)
	}
	return user.ID, nil
}

// Get retrieves a user by internal ID.
func (r *UserRepository) Get(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user UserModel
	if err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return toUserDomain(&user), nil
}

// GetByExternalID retrieves a user by org and external ID.
func (r *UserRepository) GetByExternalID(ctx context.Context, orgID uuid.UUID, externalID string) (*domain.User, error) {
	var user UserModel
	err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Where("external_id = ?", externalID).
		First(&user).Error
	if err != nil {
		return nil, err
	}
	return toUserDomain(&user), nil
}
