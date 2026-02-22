package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/domain"
)

// OrgRepository manages organization records.
type OrgRepository struct {
	db *gorm.DB
}

// NewOrgRepository creates an OrgRepository.
func NewOrgRepository(db *gorm.DB) *OrgRepository {
	return &OrgRepository{db: db}
}

// EnsureDefaultOrg creates the default org if it doesn't exist and returns its ID.
func (r *OrgRepository) EnsureDefaultOrg(ctx context.Context, name string) (uuid.UUID, error) {
	if name == "" {
		name = "default"
	}
	slug := toSlug(name)

	var org OrgModel
	err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&org).Error
	if err == nil {
		return org.ID, nil
	}
	if err != gorm.ErrRecordNotFound {
		return uuid.Nil, fmt.Errorf("looking up org %q: %w", slug, err)
	}

	org = OrgModel{
		ID:   uuid.New(),
		Name: name,
		Slug: slug,
	}
	if err := r.db.WithContext(ctx).Create(&org).Error; err != nil {
		return uuid.Nil, fmt.Errorf("creating org %q: %w", name, err)
	}
	return org.ID, nil
}

// Get retrieves an organization by ID.
func (r *OrgRepository) Get(ctx context.Context, id uuid.UUID) (*domain.Organization, error) {
	var org OrgModel
	if err := r.db.WithContext(ctx).First(&org, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return toOrgDomain(&org), nil
}

func toSlug(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-"))
}
