package skillloader

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SkillManifest is the standardized format for community skill packs.
// Extends SkillDefinition with versioning, authorship, and integrity metadata.
type SkillManifest struct {
	// Identity.
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	Author     string `yaml:"author" json:"author"`
	License    string `yaml:"license" json:"license"`
	Homepage   string `yaml:"homepage" json:"homepage"`
	Repository string `yaml:"repository" json:"repository"`

	// Categorization (matches SkillDefinition).
	AgentRole     string   `yaml:"agent_role" json:"agent_role"`
	Category      string   `yaml:"category" json:"category"`
	Tags          []string `yaml:"tags" json:"tags"`
	ToolsRequired []string `yaml:"tools_required" json:"tools_required"`
	RiskLevel     string   `yaml:"risk_level" json:"risk_level"`
	DefaultBudget float64  `yaml:"default_budget" json:"default_budget"`

	// Integrity.
	ContentHash string `yaml:"content_hash" json:"content_hash"`
	Signature   string `yaml:"signature" json:"signature"`
	SignedBy    string `yaml:"signed_by" json:"signed_by"`

	// Permissions (validated against SecurityPolicy).
	RequiredPermissions []string `yaml:"required_permissions" json:"required_permissions"`
	RequestedPaths      []string `yaml:"requested_paths" json:"requested_paths"`
	RequestedDomains    []string `yaml:"requested_domains" json:"requested_domains"`

	// Execution constraints.
	MaxExecutionSeconds int     `yaml:"max_execution_seconds" json:"max_execution_seconds"`
	MaxBudgetUSD        float64 `yaml:"max_budget_usd" json:"max_budget_usd"`
	SandboxRequired     bool    `yaml:"sandbox_required" json:"sandbox_required"`

	// Set by loader (not in YAML).
	Description string `yaml:"-" json:"description"`
	SourceFile  string `yaml:"-" json:"source_file"`
	SkillKey    string `yaml:"-" json:"skill_key"`
}

// ValidateIntegrity checks the content hash matches the body.
func (m *SkillManifest) ValidateIntegrity(body string) error {
	if m.ContentHash == "" {
		return nil // No hash = no integrity check.
	}

	hash := sha256.Sum256([]byte(body))
	computed := "sha256:" + hex.EncodeToString(hash[:])

	// Support both "sha256:..." and raw hex formats.
	expected := m.ContentHash
	if !strings.HasPrefix(expected, "sha256:") {
		expected = "sha256:" + expected
	}

	if computed != expected {
		return fmt.Errorf("content_hash mismatch: expected %s, got %s", m.ContentHash, computed)
	}
	return nil
}

// semverRe matches basic semver (major.minor.patch, optional pre-release/build).
var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[a-zA-Z0-9.]+)?(\+[a-zA-Z0-9.]+)?$`)

// ValidateVersion checks that version follows semver.
func (m *SkillManifest) ValidateVersion() error {
	if m.Version == "" {
		return fmt.Errorf("version is required for community skill packs")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("version %q is not valid semver (expected MAJOR.MINOR.PATCH)", m.Version)
	}

	parts := semverRe.FindStringSubmatch(m.Version)
	for i := 1; i <= 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		if n < 0 {
			return fmt.Errorf("version component %d must be non-negative", i)
		}
	}
	return nil
}

// ToSkillDefinition converts a manifest to the internal SkillDefinition.
func (m *SkillManifest) ToSkillDefinition() *SkillDefinition {
	return &SkillDefinition{
		Name:          m.Name,
		AgentRole:     m.AgentRole,
		Category:      m.Category,
		ToolsRequired: m.ToolsRequired,
		RiskLevel:     m.RiskLevel,
		DefaultBudget: m.DefaultBudget,
		Description:   m.Description,
		SourceFile:    m.SourceFile,
		SkillKey:      m.SkillKey,
	}
}
