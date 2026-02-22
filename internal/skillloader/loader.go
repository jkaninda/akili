// Package skillloader parses agent skill definitions from Markdown files
// with YAML frontmatter and seeds them into the skill store.
package skillloader

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/tools"
)

// validRiskLevels enumerates the accepted risk_level values.
var validRiskLevels = map[string]bool{
	"low":      true,
	"medium":   true,
	"high":     true,
	"critical": true,
}

// SkillDefinition represents a skill parsed from a Markdown file.
type SkillDefinition struct {
	Name          string   `yaml:"name"`
	AgentRole     string   `yaml:"agent_role"`
	Category      string   `yaml:"category"`
	ToolsRequired []string `yaml:"tools_required"`
	RiskLevel     string   `yaml:"risk_level"`
	DefaultBudget float64  `yaml:"default_budget"`
	Description   string   `yaml:"-"` // Parsed from Markdown body.
	SourceFile    string   `yaml:"-"` // Original file path for audit.
	SkillKey      string   `yaml:"-"` // Derived from filename stem.
}

// LoadResult summarizes a directory load operation.
type LoadResult struct {
	Loaded int
	Errors []LoadError
}

// LoadError records a per-file parse or validation error.
type LoadError struct {
	File    string
	Message string
}

// Loader parses and validates Markdown skill definitions.
type Loader struct {
	toolRegistry *tools.Registry
	logger       *slog.Logger
}

// NewLoader creates a Loader. toolRegistry is used to validate tools_required.
func NewLoader(toolRegistry *tools.Registry, logger *slog.Logger) *Loader {
	return &Loader{
		toolRegistry: toolRegistry,
		logger:       logger,
	}
}

// LoadDir scans dir for *.md files, parses and validates each.
// Returns valid definitions and a result summary. Returns an error only
// if the directory itself cannot be read.
func (l *Loader) LoadDir(dir string) ([]SkillDefinition, *LoadResult, error) {
	correlationID := newCorrelationID()

	l.logger.Info("loading skill definitions",
		slog.String("dir", dir),
		slog.String("correlation_id", correlationID),
	)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading skills directory %s: %w", dir, err)
	}

	result := &LoadResult{}
	var defs []SkillDefinition

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		def, err := l.ParseFile(path)
		if err != nil {
			l.logger.Warn("skill parse error",
				slog.String("file", path),
				slog.String("error", err.Error()),
				slog.String("correlation_id", correlationID),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		if err := l.Validate(def); err != nil {
			l.logger.Warn("skill validation error",
				slog.String("file", path),
				slog.String("skill", def.Name),
				slog.String("error", err.Error()),
				slog.String("correlation_id", correlationID),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		l.logger.Info("skill definition loaded",
			slog.String("skill_key", def.SkillKey),
			slog.String("agent_role", def.AgentRole),
			slog.String("name", def.Name),
			slog.String("category", def.Category),
			slog.String("risk_level", def.RiskLevel),
			slog.Float64("default_budget", def.DefaultBudget),
			slog.String("correlation_id", correlationID),
		)

		defs = append(defs, *def)
		result.Loaded++
	}

	l.logger.Info("skill definitions load complete",
		slog.Int("loaded", result.Loaded),
		slog.Int("errors", len(result.Errors)),
		slog.String("correlation_id", correlationID),
	)

	return defs, result, nil
}

// ParseFile reads a Markdown file, extracts YAML frontmatter and body.
func (l *Loader) ParseFile(path string) (*SkillDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Expect first line to be "---".
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty file")
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return nil, fmt.Errorf("missing YAML frontmatter (file must start with ---)")
	}

	// Read until closing "---".
	var frontmatterLines []string
	foundClose := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundClose = true
			break
		}
		frontmatterLines = append(frontmatterLines, line)
	}
	if !foundClose {
		return nil, fmt.Errorf("unclosed YAML frontmatter (missing closing ---)")
	}

	// Read remaining body as description.
	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	// Parse YAML frontmatter.
	frontmatter := strings.Join(frontmatterLines, "\n")
	def := &SkillDefinition{}
	if err := yaml.Unmarshal([]byte(frontmatter), def); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	def.Description = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	def.SourceFile = path
	def.SkillKey = filenameStem(path)

	return def, nil
}

// Validate checks that a skill definition has all required fields and valid values.
func (l *Loader) Validate(def *SkillDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("name is required")
	}

	if def.AgentRole == "" {
		return fmt.Errorf("agent_role is required")
	}
	if err := orchestrator.ValidateRole(orchestrator.AgentRole(def.AgentRole)); err != nil {
		return fmt.Errorf("invalid agent_role %q: %w", def.AgentRole, err)
	}

	if len(def.ToolsRequired) == 0 {
		return fmt.Errorf("tools_required must list at least one tool")
	}
	if l.toolRegistry != nil {
		for _, toolName := range def.ToolsRequired {
			if l.toolRegistry.Get(toolName) == nil {
				return fmt.Errorf("unknown tool %q in tools_required", toolName)
			}
		}
	}

	if !validRiskLevels[def.RiskLevel] {
		return fmt.Errorf("invalid risk_level %q (must be low, medium, high, or critical)", def.RiskLevel)
	}

	if def.DefaultBudget <= 0 {
		return fmt.Errorf("default_budget must be > 0 (got %v)", def.DefaultBudget)
	}

	return nil
}

// filenameStem returns the filename without extension.
func filenameStem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// newCorrelationID generates an 8-byte random hex correlation ID.
func newCorrelationID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}
