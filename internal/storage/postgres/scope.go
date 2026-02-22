package postgres

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TenantScope returns a GORM scope that filters by org_id.
// Must be applied to every query in every repository method for multi-tenancy.
func TenantScope(orgID uuid.UUID) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("org_id = ?", orgID)
	}
}
