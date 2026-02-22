package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// Role defines a named set of permissions with a maximum auto-approved risk level.
type Role struct {
	Name            string   `json:"name"`
	Permissions     []string `json:"permissions"`      // Explicitly allowed action names.
	MaxRiskLevel    string   `json:"max_risk_level"`   // Highest risk level allowed without approval.
	RequireApproval []string `json:"require_approval"` // Actions that always require approval.
}

// RBACConfig is the full role-based access control configuration.
type RBACConfig struct {
	Roles       map[string]Role   // role name → definition
	UserRoles   map[string]string // user ID → role name
	DefaultRole string            // role for users not in UserRoles
}

// RBAC enforces role-based access control with default-deny semantics.
// Safe for concurrent use.
type RBAC struct {
	mu          sync.RWMutex
	roles       map[string]Role
	userRoles   map[string]string
	defaultRole string
	logger      *slog.Logger
}

// NewRBAC creates an RBAC enforcer from the given configuration.
func NewRBAC(cfg RBACConfig, logger *slog.Logger) *RBAC {
	return &RBAC{
		roles:       cfg.Roles,
		userRoles:   cfg.UserRoles,
		defaultRole: cfg.DefaultRole,
		logger:      logger,
	}
}

// CheckPermission returns nil if the user's role explicitly includes the action.
// Default-deny: no role or missing permission means denied.
func (r *RBAC) CheckPermission(ctx context.Context, userID string, action Action) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	role, ok := r.resolveRole(userID)
	if !ok {
		r.logger.WarnContext(ctx, "permission denied: no role found",
			slog.String("user_id", userID),
			slog.String("action", action.Name),
		)
		return fmt.Errorf("%w: user %q has no assigned role", ErrPermissionDenied, userID)
	}

	if !roleHasPermission(role, action.Name) {
		r.logger.WarnContext(ctx, "permission denied: action not in role",
			slog.String("user_id", userID),
			slog.String("role", role.Name),
			slog.String("action", action.Name),
		)
		return fmt.Errorf("%w: role %q does not include action %q", ErrPermissionDenied, role.Name, action.Name)
	}

	return nil
}

// RequireApproval returns ErrApprovalRequired if the action's risk level exceeds
// the role's max risk level, or if the action is in the role's require_approval list.
func (r *RBAC) RequireApproval(ctx context.Context, userID string, action Action) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	role, ok := r.resolveRole(userID)
	if !ok {
		return fmt.Errorf("%w: no role for user %q", ErrApprovalRequired, userID)
	}

	maxRisk := ParseRiskLevel(role.MaxRiskLevel)
	if action.RiskLevel > maxRisk {
		r.logger.WarnContext(ctx, "approval required: risk exceeds role maximum",
			slog.String("user_id", userID),
			slog.String("role", role.Name),
			slog.String("action", action.Name),
			slog.String("action_risk", action.RiskLevel.String()),
			slog.String("max_risk", maxRisk.String()),
		)
		return fmt.Errorf("%w: action %q (risk=%s) exceeds role %q max risk (%s)",
			ErrApprovalRequired, action.Name, action.RiskLevel, role.Name, maxRisk)
	}

	for _, a := range role.RequireApproval {
		if a == action.Name {
			r.logger.WarnContext(ctx, "approval required: action in require_approval list",
				slog.String("user_id", userID),
				slog.String("role", role.Name),
				slog.String("action", action.Name),
			)
			return fmt.Errorf("%w: action %q requires explicit approval for role %q",
				ErrApprovalRequired, action.Name, role.Name)
		}
	}

	return nil
}

// resolveRole returns the role for the user, falling back to defaultRole.
func (r *RBAC) resolveRole(userID string) (Role, bool) {
	roleName, ok := r.userRoles[userID]
	if !ok {
		roleName = r.defaultRole
	}
	if roleName == "" {
		return Role{}, false
	}
	role, ok := r.roles[roleName]
	return role, ok
}

// roleHasPermission checks if the action name is explicitly in the role's permission list.
// No wildcards — every permission must be explicitly enumerated (default-deny).
func roleHasPermission(role Role, actionName string) bool {
	for _, p := range role.Permissions {
		if p == actionName {
			return true
		}
	}
	return false
}
