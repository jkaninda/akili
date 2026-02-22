package orchestrator

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// --- InMemorySkillStore ---

func TestInMemorySkillStore_GetUpsert(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	// Get non-existent skill should fail.
	_, err := store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	if err == nil {
		t.Fatal("expected error for non-existent skill")
	}

	// Upsert a skill.
	skill := &AgentSkill{
		ID:               uuid.New(),
		OrgID:            orgID,
		AgentRole:        RoleExecutor,
		SkillKey:         "executor",
		SuccessCount:     5,
		FailureCount:     1,
		AvgDurationMS:    1500.0,
		AvgCostUSD:       0.05,
		ReliabilityScore: 5.0 / 6.0,
		MaturityLevel:    SkillBasic,
		CreatedAt:        time.Now().UTC(),
		LastUpdatedAt:    time.Now().UTC(),
	}
	if err := store.UpsertSkill(ctx, skill); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Get should succeed.
	got, err := store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SuccessCount != 5 {
		t.Errorf("success_count = %d, want 5", got.SuccessCount)
	}
	if got.SkillKey != "executor" {
		t.Errorf("skill_key = %q, want executor", got.SkillKey)
	}

	// Update via upsert.
	skill.SuccessCount = 10
	if err := store.UpsertSkill(ctx, skill); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	got, _ = store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	if got.SuccessCount != 10 {
		t.Errorf("success_count after update = %d, want 10", got.SuccessCount)
	}
}

func TestInMemorySkillStore_ListByRole(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	// Add skills for two roles.
	for _, role := range []AgentRole{RoleExecutor, RoleResearcher} {
		_ = store.UpsertSkill(ctx, &AgentSkill{
			ID:        uuid.New(),
			OrgID:     orgID,
			AgentRole: role,
			SkillKey:  string(role),
		})
	}

	// List executor skills.
	skills, err := store.ListSkillsByRole(ctx, orgID, RoleExecutor)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("expected 1 executor skill, got %d", len(skills))
	}
}

func TestInMemorySkillStore_OrgIsolation(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	org1 := uuid.New()
	org2 := uuid.New()

	_ = store.UpsertSkill(ctx, &AgentSkill{
		ID: uuid.New(), OrgID: org1, AgentRole: RoleExecutor, SkillKey: "executor",
	})
	_ = store.UpsertSkill(ctx, &AgentSkill{
		ID: uuid.New(), OrgID: org2, AgentRole: RoleExecutor, SkillKey: "executor",
	})

	// Each org should only see its own skill.
	_, err := store.GetSkill(ctx, org1, RoleExecutor, "executor")
	if err != nil {
		t.Fatalf("org1 get: %v", err)
	}
	_, err = store.GetSkill(ctx, org2, RoleExecutor, "executor")
	if err != nil {
		t.Fatalf("org2 get: %v", err)
	}

	skills1, _ := store.ListSkillsByRole(ctx, org1, RoleExecutor)
	skills2, _ := store.ListSkillsByRole(ctx, org2, RoleExecutor)
	if len(skills1) != 1 || len(skills2) != 1 {
		t.Errorf("expected 1 skill per org, got %d and %d", len(skills1), len(skills2))
	}
}

// --- Maturity Computation ---

func TestComputeMaturity(t *testing.T) {
	tests := []struct {
		completions int
		reliability float64
		want        SkillMaturity
	}{
		{0, 0, SkillBasic},
		{5, 1.0, SkillBasic},            // <10 completions.
		{10, 0.70, SkillProven},          // Exactly threshold.
		{10, 0.69, SkillBasic},           // Just below threshold.
		{50, 0.85, SkillTrusted},         // Exactly threshold.
		{50, 0.84, SkillProven},          // Below trusted but above proven.
		{100, 0.95, SkillOptimized},      // Exactly threshold.
		{100, 0.94, SkillTrusted},        // Below optimized.
		{200, 1.0, SkillOptimized},       // Well above threshold.
	}

	for _, tt := range tests {
		got := ComputeMaturity(tt.completions, tt.reliability)
		if got != tt.want {
			t.Errorf("ComputeMaturity(%d, %.2f) = %q, want %q",
				tt.completions, tt.reliability, got, tt.want)
		}
	}
}

func TestMaturityMonotonic(t *testing.T) {
	// Verify maturity ranking is strictly ordered.
	levels := []SkillMaturity{SkillBasic, SkillProven, SkillTrusted, SkillOptimized}
	for i := 1; i < len(levels); i++ {
		if maturityRank(levels[i]) <= maturityRank(levels[i-1]) {
			t.Errorf("maturityRank(%q) <= maturityRank(%q)", levels[i], levels[i-1])
		}
	}
}

// --- SkillTracker ---

func TestSkillTracker_RecordCompletion_FirstSuccess(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	tracker := NewSkillTracker(store, nil, nil)

	task := &Task{
		ID:        uuid.New(),
		OrgID:     orgID,
		AgentRole: RoleExecutor,
	}

	tracker.RecordCompletion(ctx, orgID, task, 2000.0, 0.10, true)

	skill, err := store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}

	if skill.SuccessCount != 1 {
		t.Errorf("success_count = %d, want 1", skill.SuccessCount)
	}
	if skill.FailureCount != 0 {
		t.Errorf("failure_count = %d, want 0", skill.FailureCount)
	}
	if skill.ReliabilityScore != 1.0 {
		t.Errorf("reliability = %f, want 1.0", skill.ReliabilityScore)
	}
	if skill.AvgDurationMS != 2000.0 {
		t.Errorf("avg_duration = %f, want 2000.0", skill.AvgDurationMS)
	}
	if skill.AvgCostUSD != 0.10 {
		t.Errorf("avg_cost = %f, want 0.10", skill.AvgCostUSD)
	}
	if skill.MaturityLevel != SkillBasic {
		t.Errorf("maturity = %q, want basic", skill.MaturityLevel)
	}
}

func TestSkillTracker_RecordCompletion_RollingAverage(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	tracker := NewSkillTracker(store, nil, nil)

	task := &Task{
		ID:        uuid.New(),
		OrgID:     orgID,
		AgentRole: RoleResearcher,
	}

	// First completion: raw value.
	tracker.RecordCompletion(ctx, orgID, task, 1000.0, 0.05, true)

	// Second completion: EMA applied.
	tracker.RecordCompletion(ctx, orgID, task, 2000.0, 0.10, true)

	skill, _ := store.GetSkill(ctx, orgID, RoleResearcher, "researcher")

	// EMA: 0.1 * 2000 + 0.9 * 1000 = 200 + 900 = 1100
	expectedDuration := 0.1*2000 + 0.9*1000
	if math.Abs(skill.AvgDurationMS-expectedDuration) > 0.01 {
		t.Errorf("avg_duration = %f, want %f", skill.AvgDurationMS, expectedDuration)
	}

	// EMA: 0.1 * 0.10 + 0.9 * 0.05 = 0.01 + 0.045 = 0.055
	expectedCost := 0.1*0.10 + 0.9*0.05
	if math.Abs(skill.AvgCostUSD-expectedCost) > 0.0001 {
		t.Errorf("avg_cost = %f, want %f", skill.AvgCostUSD, expectedCost)
	}
}

func TestSkillTracker_RecordCompletion_Failure(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	tracker := NewSkillTracker(store, nil, nil)

	task := &Task{
		ID:        uuid.New(),
		OrgID:     orgID,
		AgentRole: RoleExecutor,
	}

	// One success, one failure.
	tracker.RecordCompletion(ctx, orgID, task, 1000.0, 0.05, true)
	tracker.RecordCompletion(ctx, orgID, task, 0, 0, false)

	skill, _ := store.GetSkill(ctx, orgID, RoleExecutor, "executor")

	if skill.SuccessCount != 1 {
		t.Errorf("success_count = %d, want 1", skill.SuccessCount)
	}
	if skill.FailureCount != 1 {
		t.Errorf("failure_count = %d, want 1", skill.FailureCount)
	}
	if skill.ReliabilityScore != 0.5 {
		t.Errorf("reliability = %f, want 0.5", skill.ReliabilityScore)
	}
	// Duration should remain from the success (failures don't update averages).
	if skill.AvgDurationMS != 1000.0 {
		t.Errorf("avg_duration = %f, want 1000.0 (unchanged from success)", skill.AvgDurationMS)
	}
}

func TestSkillTracker_MaturityAdvancesMonotonically(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	tracker := NewSkillTracker(store, nil, nil)

	task := &Task{
		ID:        uuid.New(),
		OrgID:     orgID,
		AgentRole: RoleExecutor,
	}

	// Record 10 successes to reach "proven".
	for i := 0; i < 10; i++ {
		tracker.RecordCompletion(ctx, orgID, task, 1000.0, 0.01, true)
	}
	skill, _ := store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	if skill.MaturityLevel != SkillProven {
		t.Errorf("after 10 successes: maturity = %q, want proven", skill.MaturityLevel)
	}

	// Record 5 failures. Reliability drops below proven threshold but
	// maturity should NOT regress.
	for i := 0; i < 5; i++ {
		tracker.RecordCompletion(ctx, orgID, task, 0, 0, false)
	}
	skill, _ = store.GetSkill(ctx, orgID, RoleExecutor, "executor")
	// reliability = 10/15 ≈ 0.667, which is below 0.70 threshold
	if skill.ReliabilityScore >= 0.70 {
		t.Fatalf("expected reliability < 0.70, got %f", skill.ReliabilityScore)
	}
	if skill.MaturityLevel != SkillProven {
		t.Errorf("after failures: maturity = %q, want proven (no regression)", skill.MaturityLevel)
	}
}

func TestSkillTracker_NilSafe(t *testing.T) {
	ctx := context.Background()
	var tracker *SkillTracker
	// Should not panic.
	tracker.RecordCompletion(ctx, uuid.New(), &Task{}, 0, 0, true)
}

func TestNewSkillTracker_NilStore(t *testing.T) {
	tracker := NewSkillTracker(nil, nil, nil)
	if tracker != nil {
		t.Error("expected nil tracker when store is nil")
	}
}

// --- SkillScorer ---

func TestSkillScorer_NilSafe(t *testing.T) {
	ctx := context.Background()
	var scorer *SkillScorer
	scores := scorer.ScoreTasks(ctx, uuid.New(), []Task{{ID: uuid.New()}})
	if scores != nil {
		t.Error("expected nil scores from nil scorer")
	}
}

func TestNewSkillScorer_NilStore(t *testing.T) {
	scorer := NewSkillScorer(nil, DefaultSkillWeights())
	if scorer != nil {
		t.Error("expected nil scorer when store is nil")
	}
}

func TestSkillScorer_NeutralScoreForUnknownSkills(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	scorer := NewSkillScorer(store, DefaultSkillWeights())

	tasks := []Task{
		{ID: uuid.New(), AgentRole: RoleExecutor},
		{ID: uuid.New(), AgentRole: RoleResearcher},
	}

	scores := scorer.ScoreTasks(ctx, uuid.New(), tasks)
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}

	for _, s := range scores {
		if s.Score != 0.5 {
			t.Errorf("task %s score = %f, want 0.5 (neutral)", s.TaskID, s.Score)
		}
	}
}

func TestSkillScorer_HigherReliabilityScoresHigher(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	// Create two skills: one reliable, one not.
	_ = store.UpsertSkill(ctx, &AgentSkill{
		ID: uuid.New(), OrgID: orgID, AgentRole: RoleExecutor,
		SkillKey: "executor", SuccessCount: 90, FailureCount: 10,
		AvgDurationMS: 1000, AvgCostUSD: 0.05,
		ReliabilityScore: 0.90, MaturityLevel: SkillTrusted,
	})
	_ = store.UpsertSkill(ctx, &AgentSkill{
		ID: uuid.New(), OrgID: orgID, AgentRole: RoleResearcher,
		SkillKey: "researcher", SuccessCount: 5, FailureCount: 5,
		AvgDurationMS: 2000, AvgCostUSD: 0.10,
		ReliabilityScore: 0.50, MaturityLevel: SkillBasic,
	})

	scorer := NewSkillScorer(store, DefaultSkillWeights())

	tasks := []Task{
		{ID: uuid.New(), AgentRole: RoleExecutor},
		{ID: uuid.New(), AgentRole: RoleResearcher},
	}

	scores := scorer.ScoreTasks(ctx, orgID, tasks)

	var executorScore, researcherScore float64
	for _, s := range scores {
		if s.TaskID == tasks[0].ID {
			executorScore = s.Score
		}
		if s.TaskID == tasks[1].ID {
			researcherScore = s.Score
		}
	}

	if executorScore <= researcherScore {
		t.Errorf("executor score (%f) should be > researcher score (%f)",
			executorScore, researcherScore)
	}
}

func TestSkillScorer_EmptyTasks(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	scorer := NewSkillScorer(store, DefaultSkillWeights())

	scores := scorer.ScoreTasks(ctx, uuid.New(), nil)
	if scores != nil {
		t.Error("expected nil scores for empty tasks")
	}
}

// --- SkillMetrics ---

func TestSkillMetrics_Created(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewSkillMetrics(reg)
	if m == nil {
		t.Fatal("expected non-nil SkillMetrics")
	}

	// Initialize label values to make metrics visible in Gather().
	m.SkillCompletions.WithLabelValues("executor", "executor", "success").Inc()
	m.SkillReliability.WithLabelValues("executor", "executor", "basic").Set(0.9)
	m.SkillAvgDuration.WithLabelValues("executor", "executor").Set(100)
	m.SkillAvgCost.WithLabelValues("executor", "executor").Set(0.05)
	m.SkillMaturityLevel.WithLabelValues("executor", "executor").Set(0)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, expected := range []string{
		"akili_skill_reliability",
		"akili_skill_avg_duration_ms",
		"akili_skill_avg_cost_usd",
		"akili_skill_completions_total",
		"akili_skill_maturity_level",
	} {
		if !names[expected] {
			t.Errorf("metric %q not found", expected)
		}
	}
}

func TestSkillMetrics_NilRegistry(t *testing.T) {
	m := NewSkillMetrics(nil)
	if m != nil {
		t.Fatal("expected nil SkillMetrics for nil registry")
	}
}

// --- SkillTracker with Prometheus ---

func TestSkillTracker_UpdatesPrometheusMetrics(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySkillStore()
	orgID := uuid.New()

	reg := prometheus.NewRegistry()
	metrics := NewSkillMetrics(reg)
	tracker := NewSkillTracker(store, metrics, nil)

	task := &Task{
		ID:        uuid.New(),
		OrgID:     orgID,
		AgentRole: RoleExecutor,
	}

	tracker.RecordCompletion(ctx, orgID, task, 1500.0, 0.08, true)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Verify at least one skill metric was recorded.
	found := false
	for _, f := range families {
		if f.GetName() == "akili_skill_completions_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected akili_skill_completions_total metric after RecordCompletion")
	}
}
