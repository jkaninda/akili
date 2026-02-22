package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SkillMaturity represents how well-established an agent's skill performance is.
type SkillMaturity string

const (
	SkillBasic     SkillMaturity = "basic"     // <10 completions.
	SkillProven    SkillMaturity = "proven"    // ≥10 completions, reliability ≥0.70.
	SkillTrusted   SkillMaturity = "trusted"   // ≥50 completions, reliability ≥0.85.
	SkillOptimized SkillMaturity = "optimized" // ≥100 completions, reliability ≥0.95.
)

// maturityRank returns a numeric rank for ordering (higher = more mature).
func maturityRank(m SkillMaturity) int {
	switch m {
	case SkillBasic:
		return 0
	case SkillProven:
		return 1
	case SkillTrusted:
		return 2
	case SkillOptimized:
		return 3
	default:
		return 0
	}
}

// maturityBonus returns the scoring bonus for a maturity level.
func maturityBonus(m SkillMaturity) float64 {
	switch m {
	case SkillBasic:
		return 0.0
	case SkillProven:
		return 0.25
	case SkillTrusted:
		return 0.50
	case SkillOptimized:
		return 1.0
	default:
		return 0.0
	}
}

// AgentSkill tracks performance metrics for an agent role's skill.
type AgentSkill struct {
	ID               uuid.UUID
	OrgID            uuid.UUID
	AgentRole        AgentRole
	SkillKey         string // e.g. "executor:shell", "researcher:web_fetch"
	SuccessCount     int
	FailureCount     int
	AvgDurationMS    float64 // Rolling average (EMA).
	AvgCostUSD       float64 // Rolling average (EMA).
	ReliabilityScore float64 // success / (success + failure).
	MaturityLevel    SkillMaturity
	LastUpdatedAt    time.Time
	CreatedAt        time.Time

	// Static definition metadata (loaded from skill Markdown files).
	Name          string
	Category      string
	ToolsRequired []string
	RiskLevel     string
	DefaultBudget float64
	Description   string
}

// totalCompletions returns success + failure count.
func (s *AgentSkill) totalCompletions() int {
	return s.SuccessCount + s.FailureCount
}

// SkillStore persists agent skill performance data.
type SkillStore interface {
	GetSkill(ctx context.Context, orgID uuid.UUID, agentRole AgentRole, skillKey string) (*AgentSkill, error)
	UpsertSkill(ctx context.Context, skill *AgentSkill) error
	ListSkillsByRole(ctx context.Context, orgID uuid.UUID, agentRole AgentRole) ([]AgentSkill, error)
}

// emaAlpha is the smoothing factor for exponential moving average.
// Lower values give more weight to historical data (more stable).
const emaAlpha = 0.1

// SkillTracker updates skill metrics after each task completion.
type SkillTracker struct {
	store   SkillStore
	metrics *SkillMetrics
	logger  *slog.Logger
	mu      sync.Mutex
}

// NewSkillTracker creates a SkillTracker. Returns nil if store is nil.
func NewSkillTracker(store SkillStore, metrics *SkillMetrics, logger *slog.Logger) *SkillTracker {
	if store == nil {
		return nil
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &SkillTracker{
		store:   store,
		metrics: metrics,
		logger:  logger,
	}
}

// RecordCompletion updates skill metrics after a task completes.
func (t *SkillTracker) RecordCompletion(ctx context.Context, orgID uuid.UUID, task *Task, durationMS float64, costUSD float64, success bool) {
	if t == nil {
		return
	}

	skillKey := skillKeyForTask(task)

	t.mu.Lock()
	defer t.mu.Unlock()

	skill, err := t.store.GetSkill(ctx, orgID, task.AgentRole, skillKey)
	if err != nil {
		// New skill.
		skill = &AgentSkill{
			ID:            uuid.New(),
			OrgID:         orgID,
			AgentRole:     task.AgentRole,
			SkillKey:      skillKey,
			MaturityLevel: SkillBasic,
			CreatedAt:     time.Now().UTC(),
		}
	}

	oldReliability := skill.ReliabilityScore
	oldMaturity := skill.MaturityLevel

	// Update counts.
	if success {
		skill.SuccessCount++
	} else {
		skill.FailureCount++
	}

	// Update rolling averages (only on success for duration/cost).
	if success {
		if skill.totalCompletions() == 1 {
			// First completion: use raw value.
			skill.AvgDurationMS = durationMS
			skill.AvgCostUSD = costUSD
		} else {
			// EMA: new_avg = alpha * new_value + (1 - alpha) * old_avg
			skill.AvgDurationMS = emaAlpha*durationMS + (1-emaAlpha)*skill.AvgDurationMS
			skill.AvgCostUSD = emaAlpha*costUSD + (1-emaAlpha)*skill.AvgCostUSD
		}
	}

	// Update reliability score.
	total := skill.totalCompletions()
	if total > 0 {
		skill.ReliabilityScore = float64(skill.SuccessCount) / float64(total)
	}

	// Update maturity level (monotonic: can only advance).
	newMaturity := computeMaturity(total, skill.ReliabilityScore)
	if maturityRank(newMaturity) > maturityRank(skill.MaturityLevel) {
		skill.MaturityLevel = newMaturity
	}

	skill.LastUpdatedAt = time.Now().UTC()

	if err := t.store.UpsertSkill(ctx, skill); err != nil {
		t.logger.WarnContext(ctx, "failed to upsert skill",
			slog.String("skill_key", skillKey),
			slog.String("error", err.Error()),
		)
		return
	}

	// Audit log.
	t.logger.InfoContext(ctx, "skill updated",
		slog.String("agent_role", string(task.AgentRole)),
		slog.String("skill_key", skillKey),
		slog.Float64("old_reliability", oldReliability),
		slog.Float64("new_reliability", skill.ReliabilityScore),
		slog.String("old_maturity", string(oldMaturity)),
		slog.String("new_maturity", string(skill.MaturityLevel)),
		slog.Int("success_count", skill.SuccessCount),
		slog.Int("failure_count", skill.FailureCount),
		slog.Bool("success", success),
	)

	// Update Prometheus metrics.
	if t.metrics != nil {
		t.metrics.SkillReliability.WithLabelValues(
			string(task.AgentRole), skillKey, string(skill.MaturityLevel),
		).Set(skill.ReliabilityScore)
		t.metrics.SkillAvgDuration.WithLabelValues(
			string(task.AgentRole), skillKey,
		).Set(skill.AvgDurationMS)
		t.metrics.SkillAvgCost.WithLabelValues(
			string(task.AgentRole), skillKey,
		).Set(skill.AvgCostUSD)
		status := "success"
		if !success {
			status = "failure"
		}
		t.metrics.SkillCompletions.WithLabelValues(
			string(task.AgentRole), skillKey, status,
		).Inc()
		t.metrics.SkillMaturityLevel.WithLabelValues(
			string(task.AgentRole), skillKey,
		).Set(float64(maturityRank(skill.MaturityLevel)))
	}
}

// computeMaturity determines the maturity level from completions and reliability.
func computeMaturity(totalCompletions int, reliability float64) SkillMaturity {
	if totalCompletions >= 100 && reliability >= 0.95 {
		return SkillOptimized
	}
	if totalCompletions >= 50 && reliability >= 0.85 {
		return SkillTrusted
	}
	if totalCompletions >= 10 && reliability >= 0.70 {
		return SkillProven
	}
	return SkillBasic
}

// skillKeyForTask derives the skill key from a task.
// Format: "role" (defaults to role name when no specific tool info).
func skillKeyForTask(task *Task) string {
	return string(task.AgentRole)
}

// SkillWeights configures the relative importance of scoring factors.
type SkillWeights struct {
	Reliability float64 // Default: 0.40
	Speed       float64 // Default: 0.25
	Cost        float64 // Default: 0.20
	Maturity    float64 // Default: 0.15
}

// DefaultSkillWeights returns the default scoring weights.
func DefaultSkillWeights() SkillWeights {
	return SkillWeights{
		Reliability: 0.40,
		Speed:       0.25,
		Cost:        0.20,
		Maturity:    0.15,
	}
}

// SkillScore pairs a task ID with its computed skill score.
type SkillScore struct {
	TaskID uuid.UUID
	Score  float64 // Higher = should be scheduled first.
}

// SkillScorer computes weighted scores for task scheduling.
type SkillScorer struct {
	store   SkillStore
	weights SkillWeights
}

// NewSkillScorer creates a SkillScorer. Returns nil if store is nil.
func NewSkillScorer(store SkillStore, weights SkillWeights) *SkillScorer {
	if store == nil {
		return nil
	}
	return &SkillScorer{
		store:   store,
		weights: weights,
	}
}

// ScoreTasks computes skill-based scores for a batch of tasks.
// Tasks with no skill data get a neutral score of 0.5.
func (s *SkillScorer) ScoreTasks(ctx context.Context, orgID uuid.UUID, tasks []Task) []SkillScore {
	if s == nil || len(tasks) == 0 {
		return nil
	}

	// Gather skill data for each task.
	type taskSkill struct {
		task  Task
		skill *AgentSkill // nil = no data.
	}
	items := make([]taskSkill, len(tasks))
	var maxDuration, maxCost float64
	for i, t := range tasks {
		key := skillKeyForTask(&t)
		skill, err := s.store.GetSkill(ctx, orgID, t.AgentRole, key)
		if err == nil && skill != nil {
			items[i] = taskSkill{task: t, skill: skill}
			if skill.AvgDurationMS > maxDuration {
				maxDuration = skill.AvgDurationMS
			}
			if skill.AvgCostUSD > maxCost {
				maxCost = skill.AvgCostUSD
			}
		} else {
			items[i] = taskSkill{task: t}
		}
	}

	scores := make([]SkillScore, len(tasks))
	for i, item := range items {
		if item.skill == nil {
			scores[i] = SkillScore{TaskID: item.task.ID, Score: 0.5}
			continue
		}

		sk := item.skill

		// Normalize duration and cost to [0,1] (lower is better → invert).
		normalizedDuration := 0.0
		if maxDuration > 0 {
			normalizedDuration = sk.AvgDurationMS / maxDuration
		}
		normalizedCost := 0.0
		if maxCost > 0 {
			normalizedCost = sk.AvgCostUSD / maxCost
		}

		score := s.weights.Reliability*sk.ReliabilityScore +
			s.weights.Speed*(1-normalizedDuration) +
			s.weights.Cost*(1-normalizedCost) +
			s.weights.Maturity*maturityBonus(sk.MaturityLevel)

		scores[i] = SkillScore{TaskID: item.task.ID, Score: score}
	}

	return scores
}

// SkillKeyForTask is the exported version for external callers.
func SkillKeyForTask(task *Task) string {
	return skillKeyForTask(task)
}

// ComputeMaturity is the exported version for testing.
func ComputeMaturity(totalCompletions int, reliability float64) SkillMaturity {
	return computeMaturity(totalCompletions, reliability)
}
