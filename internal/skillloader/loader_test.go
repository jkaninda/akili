package skillloader

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// stubTool implements tools.Tool for testing.
type stubTool struct {
	name string
}

func (t *stubTool) Name() string                                                       { return t.name }
func (t *stubTool) Description() string                                                { return "stub" }
func (t *stubTool) InputSchema() map[string]any                                        { return map[string]any{"type": "object"} }
func (t *stubTool) RequiredAction() security.Action                                    { return security.Action{} }
func (t *stubTool) EstimateCost(_ map[string]any) float64                              { return 0 }
func (t *stubTool) Validate(_ map[string]any) error                                    { return nil }
func (t *stubTool) Execute(_ context.Context, _ map[string]any) (*tools.Result, error) { return nil, nil }

func testRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	reg.Register(&stubTool{name: "shell_exec"})
	reg.Register(&stubTool{name: "file_read"})
	reg.Register(&stubTool{name: "file_write"})
	reg.Register(&stubTool{name: "web_fetch"})
	reg.Register(&stubTool{name: "git_read"})
	reg.Register(&stubTool{name: "code_exec"})
	return reg
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- ParseFile ---

func TestParseFile_Valid(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def, err := loader.ParseFile("testdata/shell_exec.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if def.Name != "Shell Command Execution" {
		t.Errorf("Name = %q, want %q", def.Name, "Shell Command Execution")
	}
	if def.AgentRole != "executor" {
		t.Errorf("AgentRole = %q, want %q", def.AgentRole, "executor")
	}
	if def.Category != "infrastructure" {
		t.Errorf("Category = %q, want %q", def.Category, "infrastructure")
	}
	if len(def.ToolsRequired) != 1 || def.ToolsRequired[0] != "shell_exec" {
		t.Errorf("ToolsRequired = %v, want [shell_exec]", def.ToolsRequired)
	}
	if def.RiskLevel != "high" {
		t.Errorf("RiskLevel = %q, want %q", def.RiskLevel, "high")
	}
	if def.DefaultBudget != 0.50 {
		t.Errorf("DefaultBudget = %v, want 0.50", def.DefaultBudget)
	}
	if !strings.Contains(def.Description, "sandboxed environment") {
		t.Errorf("Description = %q, want to contain %q", def.Description, "sandboxed environment")
	}
	if def.SkillKey != "shell_exec" {
		t.Errorf("SkillKey = %q, want %q", def.SkillKey, "shell_exec")
	}
	if def.SourceFile != "testdata/shell_exec.md" {
		t.Errorf("SourceFile = %q, want %q", def.SourceFile, "testdata/shell_exec.md")
	}
}

func TestParseFile_NoFrontmatter(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	_, err := loader.ParseFile("testdata/invalid_no_frontmatter.md")
	if err == nil {
		t.Fatal("expected error for file without frontmatter")
	}
	if !strings.Contains(err.Error(), "missing YAML frontmatter") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "missing YAML frontmatter")
	}
}

func TestParseFile_EmptyFile(t *testing.T) {
	// Create a temp empty file.
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(testRegistry(), discardLogger())
	_, err := loader.ParseFile(path)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestParseFile_UnclosedFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unclosed.md")
	content := "---\nname: Test\nagent_role: executor\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	loader := NewLoader(testRegistry(), discardLogger())
	_, err := loader.ParseFile(path)
	if err == nil {
		t.Fatal("expected error for unclosed frontmatter")
	}
	if !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "unclosed")
	}
}

// --- Validate ---

func TestValidate_Valid(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test Skill",
		AgentRole:     "executor",
		Category:      "test",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	if err := loader.Validate(def); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_EmptyName(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		AgentRole:     "executor",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected 'name is required' error, got %v", err)
	}
}

func TestValidate_InvalidRole(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "hacker",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "invalid agent_role") {
		t.Errorf("expected 'invalid agent_role' error, got %v", err)
	}
}

func TestValidate_UnknownTool(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{"nonexistent_tool"},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' error, got %v", err)
	}
}

func TestValidate_EmptyTools(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "tools_required") {
		t.Errorf("expected 'tools_required' error, got %v", err)
	}
}

func TestValidate_InvalidRiskLevel(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "extreme",
		DefaultBudget: 1.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "invalid risk_level") {
		t.Errorf("expected 'invalid risk_level' error, got %v", err)
	}
}

func TestValidate_ZeroBudget(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "low",
		DefaultBudget: 0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "default_budget must be > 0") {
		t.Errorf("expected 'default_budget' error, got %v", err)
	}
}

func TestValidate_NegativeBudget(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{"shell_exec"},
		RiskLevel:     "low",
		DefaultBudget: -5.0,
	}
	err := loader.Validate(def)
	if err == nil || !strings.Contains(err.Error(), "default_budget must be > 0") {
		t.Errorf("expected 'default_budget' error, got %v", err)
	}
}

func TestValidate_NilRegistry(t *testing.T) {
	loader := NewLoader(nil, discardLogger())
	def := &SkillDefinition{
		Name:          "Test",
		AgentRole:     "executor",
		ToolsRequired: []string{"anything"},
		RiskLevel:     "low",
		DefaultBudget: 1.0,
	}
	// With nil registry, tool validation is skipped.
	if err := loader.Validate(def); err != nil {
		t.Errorf("unexpected error with nil registry: %v", err)
	}
}

func TestValidate_AllRiskLevels(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	for _, level := range []string{"low", "medium", "high", "critical"} {
		def := &SkillDefinition{
			Name:          "Test",
			AgentRole:     "executor",
			ToolsRequired: []string{"shell_exec"},
			RiskLevel:     level,
			DefaultBudget: 1.0,
		}
		if err := loader.Validate(def); err != nil {
			t.Errorf("risk_level %q rejected: %v", level, err)
		}
	}
}

// --- LoadDir ---

func TestLoadDir_MixedFiles(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	defs, result, err := loader.LoadDir("testdata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// testdata has 2 valid files and 4 invalid files.
	if result.Loaded != 2 {
		t.Errorf("Loaded = %d, want 2", result.Loaded)
	}
	if len(result.Errors) != 4 {
		t.Errorf("Errors = %d, want 4", len(result.Errors))
	}
	if len(defs) != 2 {
		t.Errorf("defs = %d, want 2", len(defs))
	}

	// Verify the valid defs are present.
	keys := make(map[string]bool)
	for _, d := range defs {
		keys[d.SkillKey] = true
	}
	if !keys["shell_exec"] {
		t.Error("missing shell_exec skill")
	}
	if !keys["file_read"] {
		t.Error("missing file_read skill")
	}
}

func TestLoadDir_NonexistentDir(t *testing.T) {
	loader := NewLoader(testRegistry(), discardLogger())
	_, _, err := loader.LoadDir("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestLoadDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	loader := NewLoader(testRegistry(), discardLogger())
	defs, result, err := loader.LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Loaded != 0 {
		t.Errorf("Loaded = %d, want 0", result.Loaded)
	}
	if len(defs) != 0 {
		t.Errorf("defs = %d, want 0", len(defs))
	}
}

func TestLoadDir_IgnoresNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	// Write a .txt file — should be ignored.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// Write a subdirectory — should be ignored.
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(testRegistry(), discardLogger())
	defs, result, err := loader.LoadDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Loaded != 0 || len(result.Errors) != 0 {
		t.Errorf("Loaded=%d, Errors=%d; want 0, 0", result.Loaded, len(result.Errors))
	}
	if len(defs) != 0 {
		t.Errorf("defs = %d, want 0", len(defs))
	}
}

// --- SeedSkills ---

func TestSeedSkills_InsertsNewSkills(t *testing.T) {
	ctx := context.Background()
	store := orchestrator.NewInMemorySkillStore()
	orgID := uuid.New()

	defs := []SkillDefinition{
		{
			Name:          "Shell Exec",
			AgentRole:     "executor",
			Category:      "infra",
			ToolsRequired: []string{"shell_exec"},
			RiskLevel:     "high",
			DefaultBudget: 0.50,
			Description:   "Execute shell commands",
			SkillKey:      "shell_exec",
		},
		{
			Name:          "File Read",
			AgentRole:     "researcher",
			Category:      "data",
			ToolsRequired: []string{"file_read"},
			RiskLevel:     "low",
			DefaultBudget: 0.10,
			Description:   "Read files",
			SkillKey:      "file_read",
		},
	}

	result := SeedSkills(ctx, store, orgID, defs, nil, discardLogger())
	if result.Seeded != 2 {
		t.Errorf("Seeded = %d, want 2", result.Seeded)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", result.Skipped)
	}

	// Verify skills exist in store with correct metadata.
	skill, err := store.GetSkill(ctx, orgID, "executor", "shell_exec")
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if skill.Name != "Shell Exec" {
		t.Errorf("Name = %q, want %q", skill.Name, "Shell Exec")
	}
	if skill.Category != "infra" {
		t.Errorf("Category = %q, want %q", skill.Category, "infra")
	}
	if skill.RiskLevel != "high" {
		t.Errorf("RiskLevel = %q, want %q", skill.RiskLevel, "high")
	}
	if skill.DefaultBudget != 0.50 {
		t.Errorf("DefaultBudget = %v, want 0.50", skill.DefaultBudget)
	}
	if skill.MaturityLevel != orchestrator.SkillBasic {
		t.Errorf("MaturityLevel = %q, want %q", skill.MaturityLevel, orchestrator.SkillBasic)
	}
	if skill.SuccessCount != 0 || skill.FailureCount != 0 {
		t.Errorf("Counts = (%d, %d), want (0, 0)", skill.SuccessCount, skill.FailureCount)
	}
}

func TestSeedSkills_SkipsExistingSkills(t *testing.T) {
	ctx := context.Background()
	store := orchestrator.NewInMemorySkillStore()
	orgID := uuid.New()

	// Pre-seed a skill with performance data.
	existing := &orchestrator.AgentSkill{
		ID:               uuid.New(),
		OrgID:            orgID,
		AgentRole:        "executor",
		SkillKey:         "shell_exec",
		SuccessCount:     50,
		FailureCount:     5,
		ReliabilityScore: 0.91,
		MaturityLevel:    orchestrator.SkillProven,
	}
	if err := store.UpsertSkill(ctx, existing); err != nil {
		t.Fatal(err)
	}

	defs := []SkillDefinition{
		{
			Name:          "Shell Exec",
			AgentRole:     "executor",
			ToolsRequired: []string{"shell_exec"},
			RiskLevel:     "high",
			DefaultBudget: 0.50,
			SkillKey:      "shell_exec",
		},
	}

	result := SeedSkills(ctx, store, orgID, defs, nil, discardLogger())
	if result.Seeded != 0 {
		t.Errorf("Seeded = %d, want 0 (should skip existing)", result.Seeded)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}

	// Verify existing performance data is preserved.
	skill, err := store.GetSkill(ctx, orgID, "executor", "shell_exec")
	if err != nil {
		t.Fatal(err)
	}
	if skill.SuccessCount != 50 {
		t.Errorf("SuccessCount = %d, want 50 (should be preserved)", skill.SuccessCount)
	}
	if skill.MaturityLevel != orchestrator.SkillProven {
		t.Errorf("MaturityLevel = %q, want %q (should be preserved)", skill.MaturityLevel, orchestrator.SkillProven)
	}
}

func TestSeedSkills_UpdatesMetrics(t *testing.T) {
	ctx := context.Background()
	store := orchestrator.NewInMemorySkillStore()
	orgID := uuid.New()
	reg := prometheus.NewRegistry()
	metrics := orchestrator.NewSkillMetrics(reg)

	defs := []SkillDefinition{
		{
			Name:          "Shell Exec",
			AgentRole:     "executor",
			ToolsRequired: []string{"shell_exec"},
			RiskLevel:     "high",
			DefaultBudget: 0.50,
			SkillKey:      "shell_exec",
		},
	}

	SeedSkills(ctx, store, orgID, defs, metrics, discardLogger())

	// Verify metric was incremented.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	found := false
	for _, f := range families {
		if f.GetName() == "akili_skill_definitions_loaded_total" {
			found = true
			break
		}
	}
	if !found {
		t.Error("akili_skill_definitions_loaded_total metric not found after seeding")
	}
}

// --- filenameStem ---

func TestFilenameStem(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"testdata/shell_exec.md", "shell_exec"},
		{"/path/to/file_read.md", "file_read"},
		{"simple.md", "simple"},
		{"multi.ext.md", "multi.ext"},
	}
	for _, tc := range cases {
		got := filenameStem(tc.path)
		if got != tc.want {
			t.Errorf("filenameStem(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// --- newCorrelationID ---

func TestNewCorrelationID(t *testing.T) {
	id := newCorrelationID()
	if len(id) != 16 { // 8 bytes = 16 hex chars
		t.Errorf("correlation ID length = %d, want 16", len(id))
	}
	// Should be different each time.
	id2 := newCorrelationID()
	if id == id2 {
		t.Error("two correlation IDs should not be equal")
	}
}
