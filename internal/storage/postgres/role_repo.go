package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/security"
)

// RoleRepository implements security.RoleStore with PostgreSQL.
type RoleRepository struct {
	db   *gorm.DB
	user *UserRepository
}

// NewRoleRepository creates a RoleRepository.
func NewRoleRepository(db *gorm.DB, userRepo *UserRepository) *RoleRepository {
	return &RoleRepository{db: db, user: userRepo}
}

// LoadRBACConfig loads the full RBAC config for an organization from the database.
func (r *RoleRepository) LoadRBACConfig(ctx context.Context, orgID uuid.UUID) (security.RBACConfig, error) {
	// Load roles with permissions.
	var roles []RoleModel
	err := r.db.WithContext(ctx).
		Scopes(TenantScope(orgID)).
		Preload("Permissions").
		Find(&roles).Error
	if err != nil {
		return security.RBACConfig{}, fmt.Errorf("loading roles: %w", err)
	}

	roleMap := make(map[string]security.Role, len(roles))
	for _, rm := range roles {
		roleMap[rm.Name] = toSecurityRole(&rm)
	}

	// Load user-role assignments.
	type userRoleRow struct {
		ExternalID string
		RoleName   string
	}
	var rows []userRoleRow
	err = r.db.WithContext(ctx).Raw(`
		SELECT u.external_id, ro.name AS role_name
		FROM user_roles ur
		JOIN users u  ON u.id = ur.user_id  AND u.org_id = ?
		JOIN roles ro ON ro.id = ur.role_id AND ro.org_id = ?
		WHERE u.deleted_at IS NULL
	`, orgID, orgID).Scan(&rows).Error
	if err != nil {
		return security.RBACConfig{}, fmt.Errorf("loading user roles: %w", err)
	}

	userRoles := make(map[string]string, len(rows))
	for _, row := range rows {
		userRoles[row.ExternalID] = row.RoleName
	}

	return security.RBACConfig{
		Roles:     roleMap,
		UserRoles: userRoles,
	}, nil
}

// SaveRole creates or updates a role and its permissions.
func (r *RoleRepository) SaveRole(ctx context.Context, orgID uuid.UUID, role security.Role) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Find or create the role.
		var rm RoleModel
		err := tx.Scopes(TenantScope(orgID)).Where("name = ?", role.Name).First(&rm).Error
		if err == gorm.ErrRecordNotFound {
			rm = RoleModel{
				ID:           uuid.New(),
				OrgID:        orgID,
				Name:         role.Name,
				MaxRiskLevel: role.MaxRiskLevel,
			}
			if err := tx.Create(&rm).Error; err != nil {
				return fmt.Errorf("creating role %q: %w", role.Name, err)
			}
		} else if err != nil {
			return fmt.Errorf("looking up role %q: %w", role.Name, err)
		} else {
			rm.MaxRiskLevel = role.MaxRiskLevel
			if err := tx.Save(&rm).Error; err != nil {
				return fmt.Errorf("updating role %q: %w", role.Name, err)
			}
		}

		// Replace permissions: delete old, insert new.
		if err := tx.Where("role_id = ?", rm.ID).Delete(&PermissionModel{}).Error; err != nil {
			return fmt.Errorf("clearing permissions for %q: %w", role.Name, err)
		}

		requireApprovalSet := make(map[string]bool, len(role.RequireApproval))
		for _, a := range role.RequireApproval {
			requireApprovalSet[a] = true
		}

		for _, actionName := range role.Permissions {
			pm := PermissionModel{
				ID:               uuid.New(),
				RoleID:           rm.ID,
				ActionName:       actionName,
				RequiresApproval: requireApprovalSet[actionName],
			}
			if err := tx.Create(&pm).Error; err != nil {
				return fmt.Errorf("creating permission %q: %w", actionName, err)
			}
		}

		return nil
	})
}

// AssignUserRole sets the role for a user within an organization.
func (r *RoleRepository) AssignUserRole(ctx context.Context, orgID uuid.UUID, externalUserID, roleName string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Ensure user exists.
		userID, err := r.user.EnsureUser(ctx, orgID, externalUserID)
		if err != nil {
			return err
		}

		// Look up the role.
		var rm RoleModel
		err = tx.Scopes(TenantScope(orgID)).Where("name = ?", roleName).First(&rm).Error
		if err != nil {
			return fmt.Errorf("role %q not found: %w", roleName, err)
		}

		// Delete existing assignments for this user, then insert.
		if err := tx.Where("user_id = ?", userID).Delete(&UserRoleModel{}).Error; err != nil {
			return fmt.Errorf("clearing user role: %w", err)
		}

		ur := UserRoleModel{UserID: userID, RoleID: rm.ID}
		if err := tx.Create(&ur).Error; err != nil {
			return fmt.Errorf("assigning role: %w", err)
		}

		return nil
	})
}
