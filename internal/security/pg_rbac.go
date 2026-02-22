package security

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PGRBAC is a postgres-backed RBAC enforcer that caches RBACConfig in memory.
// It loads from the RoleStore on first access and refreshes after cacheTTL.
// On first startup with an empty database, it bootstraps from the provided
// fallback config (from the JSON config file).
type PGRBAC struct {
	store       RoleStore
	orgID       uuid.UUID
	fallbackCfg RBACConfig
	logger      *slog.Logger

	mu       sync.RWMutex
	cached   *RBAC
	loadedAt time.Time
	cacheTTL time.Duration
}

// NewPGRBAC creates a postgres-backed RBAC enforcer.
// fallbackCfg is used to bootstrap the database on first startup.
func NewPGRBAC(store RoleStore, orgID uuid.UUID, fallbackCfg RBACConfig, logger *slog.Logger) *PGRBAC {
	return &PGRBAC{
		store:       store,
		orgID:       orgID,
		fallbackCfg: fallbackCfg,
		logger:      logger,
		cacheTTL:    60 * time.Second,
	}
}

// CheckPermission delegates to the cached in-memory RBAC after ensuring it's loaded.
func (p *PGRBAC) CheckPermission(ctx context.Context, userID string, action Action) error {
	rbac, err := p.ensureLoaded(ctx)
	if err != nil {
		return fmt.Errorf("loading RBAC config: %w", err)
	}
	return rbac.CheckPermission(ctx, userID, action)
}

// RequireApproval delegates to the cached in-memory RBAC after ensuring it's loaded.
func (p *PGRBAC) RequireApproval(ctx context.Context, userID string, action Action) error {
	rbac, err := p.ensureLoaded(ctx)
	if err != nil {
		return fmt.Errorf("loading RBAC config: %w", err)
	}
	return rbac.RequireApproval(ctx, userID, action)
}

// ensureLoaded returns the cached RBAC, refreshing if stale or unloaded.
func (p *PGRBAC) ensureLoaded(ctx context.Context) (*RBAC, error) {
	p.mu.RLock()
	if p.cached != nil && time.Since(p.loadedAt) < p.cacheTTL {
		rbac := p.cached
		p.mu.RUnlock()
		return rbac, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock.
	if p.cached != nil && time.Since(p.loadedAt) < p.cacheTTL {
		return p.cached, nil
	}

	cfg, err := p.store.LoadRBACConfig(ctx, p.orgID)
	if err != nil {
		return nil, err
	}

	// Bootstrap: if DB is empty, seed from fallback config.
	if len(cfg.Roles) == 0 && len(p.fallbackCfg.Roles) > 0 {
		p.logger.Info("bootstrapping RBAC from config file",
			slog.Int("roles", len(p.fallbackCfg.Roles)),
		)
		if err := p.bootstrap(ctx); err != nil {
			return nil, fmt.Errorf("bootstrapping RBAC: %w", err)
		}
		// Reload after bootstrap.
		cfg, err = p.store.LoadRBACConfig(ctx, p.orgID)
		if err != nil {
			return nil, err
		}
	}

	// Apply fallback default role if not set in DB.
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = p.fallbackCfg.DefaultRole
	}

	p.cached = NewRBAC(cfg, p.logger)
	p.loadedAt = time.Now()
	return p.cached, nil
}

// bootstrap seeds the database from the fallback config.
func (p *PGRBAC) bootstrap(ctx context.Context) error {
	for _, role := range p.fallbackCfg.Roles {
		if err := p.store.SaveRole(ctx, p.orgID, role); err != nil {
			return fmt.Errorf("saving role %q: %w", role.Name, err)
		}
	}
	for userID, roleName := range p.fallbackCfg.UserRoles {
		if err := p.store.AssignUserRole(ctx, p.orgID, userID, roleName); err != nil {
			return fmt.Errorf("assigning user %q to role %q: %w", userID, roleName, err)
		}
	}
	return nil
}
