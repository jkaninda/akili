package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jkaninda/akili/internal/identity"
)

// IdentityRepository implements identity.IdentityStore with PostgreSQL.
type IdentityRepository struct {
	db *gorm.DB
}

// NewIdentityRepository creates an IdentityRepository.
func NewIdentityRepository(db *gorm.DB) *IdentityRepository {
	return &IdentityRepository{db: db}
}

// Register persists a new agent identity. Uses upsert to allow re-registration.
func (r *IdentityRepository) Register(ctx context.Context, id *identity.IdentityConfig) error {
	caps, _ := json.Marshal(id.Capabilities)
	model := AgentIdentityModel{
		ID:               uuid.New(),
		AgentID:          id.AgentID,
		Name:             id.Name,
		Version:          id.Version,
		PublicKey:        id.PublicKeyHex(),
		Capabilities:     JSONB(caps),
		TrustLevel:       string(id.TrustLevel),
		EnvironmentScope: id.EnvironmentScope,
		Status:           "online",
		RegisteredAt:     id.RegisteredAt,
	}

	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "agent_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "version", "public_key", "capabilities", "environment_scope", "status", "updated_at"}),
		}).
		Create(&model).Error; err != nil {
		return fmt.Errorf("registering agent identity: %w", err)
	}
	return nil
}

// GetByAgentID retrieves an identity by agent ID.
func (r *IdentityRepository) GetByAgentID(ctx context.Context, agentID string) (*identity.IdentityConfig, error) {
	var model AgentIdentityModel
	if err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		First(&model).Error; err != nil {
		return nil, fmt.Errorf("getting agent identity %s: %w", agentID, err)
	}
	return toIdentityDomain(&model), nil
}

// UpdateTrustLevel updates the trust level for an agent.
func (r *IdentityRepository) UpdateTrustLevel(ctx context.Context, agentID string, level identity.TrustLevel) error {
	if err := r.db.WithContext(ctx).
		Model(&AgentIdentityModel{}).
		Where("agent_id = ?", agentID).
		Update("trust_level", string(level)).Error; err != nil {
		return fmt.Errorf("updating trust level for %s: %w", agentID, err)
	}
	return nil
}

// UpdateStatus updates the status and heartbeat timestamp for an agent.
func (r *IdentityRepository) UpdateStatus(ctx context.Context, agentID string, status string, heartbeatAt time.Time) error {
	if err := r.db.WithContext(ctx).
		Model(&AgentIdentityModel{}).
		Where("agent_id = ?", agentID).
		Updates(map[string]any{
			"status":            status,
			"last_heartbeat_at": heartbeatAt,
		}).Error; err != nil {
		return fmt.Errorf("updating status for %s: %w", agentID, err)
	}
	return nil
}

// ListByEnvironment returns all identities matching the environment scope.
func (r *IdentityRepository) ListByEnvironment(ctx context.Context, envScope string) ([]identity.IdentityConfig, error) {
	var models []AgentIdentityModel
	if err := r.db.WithContext(ctx).
		Where("environment_scope = ?", envScope).
		Order("registered_at DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing identities for env %s: %w", envScope, err)
	}
	result := make([]identity.IdentityConfig, len(models))
	for i := range models {
		id := toIdentityDomain(&models[i])
		if id != nil {
			result[i] = *id
		}
	}
	return result, nil
}

// Remove soft-deletes an agent identity.
func (r *IdentityRepository) Remove(ctx context.Context, agentID string) error {
	if err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Delete(&AgentIdentityModel{}).Error; err != nil {
		return fmt.Errorf("removing agent identity %s: %w", agentID, err)
	}
	return nil
}

// toIdentityDomain converts a GORM model to domain type.
// Note: private key is not stored in DB — only public key is persisted.
func toIdentityDomain(m *AgentIdentityModel) *identity.IdentityConfig {
	var caps []string
	_ = json.Unmarshal(m.Capabilities, &caps)

	return &identity.IdentityConfig{
		AgentID:          m.AgentID,
		Name:             m.Name,
		Version:          m.Version,
		Capabilities:     caps,
		TrustLevel:       identity.TrustLevel(m.TrustLevel),
		EnvironmentScope: m.EnvironmentScope,
		RegisteredAt:     m.RegisteredAt,
	}
}
