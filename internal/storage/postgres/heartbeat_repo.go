package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/heartbeat"
)

// HeartbeatRepository implements heartbeat.HeartbeatStore with PostgreSQL.
type HeartbeatRepository struct {
	db *gorm.DB
}

// NewHeartbeatRepository creates a HeartbeatRepository.
func NewHeartbeatRepository(db *gorm.DB) *HeartbeatRepository {
	return &HeartbeatRepository{db: db}
}

// RecordHeartbeat appends a heartbeat record and updates the agent identity's status.
func (r *HeartbeatRepository) RecordHeartbeat(ctx context.Context, hb *heartbeat.Heartbeat) error {
	skills, _ := json.Marshal(hb.Skills)
	caps, _ := json.Marshal(hb.Capabilities)
	meta, _ := json.Marshal(hb.Metadata)

	model := AgentHeartbeatModel{
		ID:           uuid.New(),
		AgentID:      hb.AgentID,
		Version:      hb.Version,
		Status:       string(hb.Status),
		Skills:       JSONB(skills),
		Capabilities: JSONB(caps),
		ActiveTasks:  hb.ActiveTasks,
		MaxTasks:     hb.MaxTasks,
		Metadata:     JSONB(meta),
		CreatedAt:    hb.Timestamp,
	}

	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("recording heartbeat: %w", err)
	}

	// Update agent identity status and heartbeat timestamp.
	if err := r.db.WithContext(ctx).
		Model(&AgentIdentityModel{}).
		Where("agent_id = ?", hb.AgentID).
		Updates(map[string]any{
			"status":            string(hb.Status),
			"last_heartbeat_at": hb.Timestamp,
		}).Error; err != nil {
		return fmt.Errorf("updating agent identity heartbeat: %w", err)
	}

	return nil
}

// GetLastHeartbeat retrieves the most recent heartbeat for an agent.
func (r *HeartbeatRepository) GetLastHeartbeat(ctx context.Context, agentID string) (*heartbeat.Heartbeat, error) {
	var model AgentHeartbeatModel
	if err := r.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("created_at DESC").
		First(&model).Error; err != nil {
		return nil, fmt.Errorf("getting last heartbeat for %s: %w", agentID, err)
	}
	return toHeartbeatDomain(&model), nil
}

// ListOnlineAgents returns heartbeats from agents still within the stale threshold.
func (r *HeartbeatRepository) ListOnlineAgents(ctx context.Context, staleThreshold time.Duration) ([]heartbeat.Heartbeat, error) {
	cutoff := time.Now().UTC().Add(-staleThreshold)
	var models []AgentIdentityModel
	if err := r.db.WithContext(ctx).
		Where("status IN (?) AND last_heartbeat_at > ?", []string{"online", "busy"}, cutoff).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("listing online agents: %w", err)
	}

	result := make([]heartbeat.Heartbeat, 0, len(models))
	for _, m := range models {
		var caps []string
		_ = json.Unmarshal(m.Capabilities, &caps)
		result = append(result, heartbeat.Heartbeat{
			AgentID:      m.AgentID,
			Version:      m.Version,
			Status:       heartbeat.Status(m.Status),
			Capabilities: caps,
		})
	}
	return result, nil
}

// MarkStale updates agents whose last heartbeat exceeds the threshold to "stale" status.
func (r *HeartbeatRepository) MarkStale(ctx context.Context, staleThreshold time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-staleThreshold)
	result := r.db.WithContext(ctx).
		Model(&AgentIdentityModel{}).
		Where("status IN (?) AND last_heartbeat_at < ?", []string{"online", "busy"}, cutoff).
		Update("status", "stale")
	if result.Error != nil {
		return 0, fmt.Errorf("marking stale agents: %w", result.Error)
	}
	return int(result.RowsAffected), nil
}

func toHeartbeatDomain(m *AgentHeartbeatModel) *heartbeat.Heartbeat {
	var skills []string
	_ = json.Unmarshal(m.Skills, &skills)
	var caps []string
	_ = json.Unmarshal(m.Capabilities, &caps)
	var meta map[string]string
	_ = json.Unmarshal(m.Metadata, &meta)

	return &heartbeat.Heartbeat{
		AgentID:      m.AgentID,
		Version:      m.Version,
		Status:       heartbeat.Status(m.Status),
		Skills:       skills,
		Capabilities: caps,
		ActiveTasks:  m.ActiveTasks,
		MaxTasks:     m.MaxTasks,
		Metadata:     meta,
		Timestamp:    m.CreatedAt,
	}
}
