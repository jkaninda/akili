package skillloader

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/orchestrator"
)

// SeedResult summarizes a skill seeding operation.
type SeedResult struct {
	Seeded  int
	Skipped int
}

// SeedSkills upserts skill definitions into the store as basic-maturity skills.
// Existing skills (with performance data) are skipped to preserve learned metrics.
// Thread-safe via SkillStore.UpsertSkill (postgres: INSERT ON CONFLICT, in-memory: mutex).
func SeedSkills(
	ctx context.Context,
	store orchestrator.SkillStore,
	orgID uuid.UUID,
	defs []SkillDefinition,
	metrics *orchestrator.SkillMetrics,
	logger *slog.Logger,
) *SeedResult {
	correlationID := newCorrelationID()
	result := &SeedResult{}

	for i := range defs {
		def := &defs[i]
		role := orchestrator.AgentRole(def.AgentRole)

		// Check if skill already exists (preserve existing performance data).
		existing, err := store.GetSkill(ctx, orgID, role, def.SkillKey)
		if err == nil && existing != nil {
			logger.Info("skill already exists, skipping",
				slog.String("skill_key", def.SkillKey),
				slog.String("agent_role", def.AgentRole),
				slog.String("correlation_id", correlationID),
			)
			result.Skipped++
			continue
		}

		// Create new skill with basic maturity and zero performance counters.
		now := time.Now().UTC()
		skill := &orchestrator.AgentSkill{
			ID:            uuid.New(),
			OrgID:         orgID,
			AgentRole:     role,
			SkillKey:      def.SkillKey,
			MaturityLevel: orchestrator.SkillBasic,
			CreatedAt:     now,
			LastUpdatedAt: now,

			// Static definition metadata.
			Name:          def.Name,
			Category:      def.Category,
			ToolsRequired: def.ToolsRequired,
			RiskLevel:     def.RiskLevel,
			DefaultBudget: def.DefaultBudget,
			Description:   def.Description,
		}

		if err := store.UpsertSkill(ctx, skill); err != nil {
			logger.Warn("failed to seed skill",
				slog.String("skill_key", def.SkillKey),
				slog.String("error", err.Error()),
				slog.String("correlation_id", correlationID),
			)
			continue
		}

		logger.Info("skill seeded",
			slog.String("skill_key", def.SkillKey),
			slog.String("agent_role", def.AgentRole),
			slog.String("name", def.Name),
			slog.String("category", def.Category),
			slog.String("risk_level", def.RiskLevel),
			slog.Float64("default_budget", def.DefaultBudget),
			slog.String("maturity", string(orchestrator.SkillBasic)),
			slog.String("correlation_id", correlationID),
		)

		if metrics != nil {
			metrics.SkillDefsLoaded.WithLabelValues(def.AgentRole).Inc()
		}

		result.Seeded++
	}

	logger.Info("skill seeding complete",
		slog.Int("seeded", result.Seeded),
		slog.Int("skipped", result.Skipped),
		slog.String("correlation_id", correlationID),
	)

	return result
}
