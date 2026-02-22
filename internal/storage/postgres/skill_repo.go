package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jkaninda/akili/internal/orchestrator"
)

// SkillRepository implements orchestrator.SkillStore with PostgreSQL.
type SkillRepository struct {
	db *gorm.DB
}

// NewSkillRepository creates a SkillRepository.
func NewSkillRepository(db *gorm.DB) *SkillRepository {
	return &SkillRepository{db: db}
}

func (r *SkillRepository) GetSkill(ctx context.Context, orgID uuid.UUID, agentRole orchestrator.AgentRole, skillKey string) (*orchestrator.AgentSkill, error) {
	var model AgentSkillModel
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND agent_role = ? AND skill_key = ?", orgID, string(agentRole), skillKey).
		First(&model).Error
	if err != nil {
		return nil, fmt.Errorf("getting skill %s/%s: %w", agentRole, skillKey, err)
	}
	return toSkillDomain(&model), nil
}

func (r *SkillRepository) UpsertSkill(ctx context.Context, skill *orchestrator.AgentSkill) error {
	model := toSkillModel(skill)
	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "org_id"},
				{Name: "agent_role"},
				{Name: "skill_key"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"success_count", "failure_count",
				"avg_duration_ms", "avg_cost_usd",
				"reliability_score", "maturity_level",
				"last_updated_at",
				"name", "category", "tools_required",
				"risk_level", "default_budget", "description",
			}),
		}).
		Create(&model)
	if result.Error != nil {
		return fmt.Errorf("upserting skill %s/%s: %w", skill.AgentRole, skill.SkillKey, result.Error)
	}
	return nil
}

func (r *SkillRepository) ListSkillsByRole(ctx context.Context, orgID uuid.UUID, agentRole orchestrator.AgentRole) ([]orchestrator.AgentSkill, error) {
	var models []AgentSkillModel
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND agent_role = ?", orgID, string(agentRole)).
		Order("skill_key ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("listing skills for role %s: %w", agentRole, err)
	}
	skills := make([]orchestrator.AgentSkill, len(models))
	for i := range models {
		skills[i] = *toSkillDomain(&models[i])
	}
	return skills, nil
}

func toSkillModel(s *orchestrator.AgentSkill) AgentSkillModel {
	toolsJSON := "[]"
	if len(s.ToolsRequired) > 0 {
		if data, err := json.Marshal(s.ToolsRequired); err == nil {
			toolsJSON = string(data)
		}
	}
	return AgentSkillModel{
		ID:               s.ID,
		OrgID:            s.OrgID,
		AgentRole:        string(s.AgentRole),
		SkillKey:         s.SkillKey,
		SuccessCount:     s.SuccessCount,
		FailureCount:     s.FailureCount,
		AvgDurationMS:    s.AvgDurationMS,
		AvgCostUSD:       s.AvgCostUSD,
		ReliabilityScore: s.ReliabilityScore,
		MaturityLevel:    string(s.MaturityLevel),
		CreatedAt:        s.CreatedAt,
		LastUpdatedAt:    s.LastUpdatedAt,
		Name:             s.Name,
		Category:         s.Category,
		ToolsRequired:    toolsJSON,
		RiskLevel:        s.RiskLevel,
		DefaultBudget:    s.DefaultBudget,
		Description:      s.Description,
	}
}

func toSkillDomain(m *AgentSkillModel) *orchestrator.AgentSkill {
	var toolsRequired []string
	if m.ToolsRequired != "" && m.ToolsRequired != "[]" {
		_ = json.Unmarshal([]byte(m.ToolsRequired), &toolsRequired)
	}
	return &orchestrator.AgentSkill{
		ID:               m.ID,
		OrgID:            m.OrgID,
		AgentRole:        orchestrator.AgentRole(m.AgentRole),
		SkillKey:         m.SkillKey,
		SuccessCount:     m.SuccessCount,
		FailureCount:     m.FailureCount,
		AvgDurationMS:    m.AvgDurationMS,
		AvgCostUSD:       m.AvgCostUSD,
		ReliabilityScore: m.ReliabilityScore,
		MaturityLevel:    orchestrator.SkillMaturity(m.MaturityLevel),
		CreatedAt:        m.CreatedAt,
		LastUpdatedAt:    m.LastUpdatedAt,
		Name:             m.Name,
		Category:         m.Category,
		ToolsRequired:    toolsRequired,
		RiskLevel:        m.RiskLevel,
		DefaultBudget:    m.DefaultBudget,
		Description:      m.Description,
	}
}

// EnsureSkillIndex creates the unique index on (org_id, agent_role, skill_key).
func (r *SkillRepository) EnsureSkillIndex() error {
	return r.db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_skills_org_role_key ON agent_skills (org_id, agent_role, skill_key)",
	).Error
}

// Compile-time check.
var _ orchestrator.SkillStore = (*SkillRepository)(nil)
