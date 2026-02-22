package skillloader

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jkaninda/akili/internal/security"
)

// PackLoader loads community skill packs with full validation.
type PackLoader struct {
	loader         *Loader
	policyEnforcer *security.PolicyEnforcer
	agentID        string
	logger         *slog.Logger
}

// NewPackLoader creates a community skill pack loader.
func NewPackLoader(loader *Loader, policyEnforcer *security.PolicyEnforcer, agentID string, logger *slog.Logger) *PackLoader {
	return &PackLoader{
		loader:         loader,
		policyEnforcer: policyEnforcer,
		agentID:        agentID,
		logger:         logger,
	}
}

// LoadPack loads a community skill pack directory.
// Each .md file must have the extended SkillManifest frontmatter.
func (pl *PackLoader) LoadPack(ctx context.Context, dir string) ([]SkillManifest, *LoadResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading pack directory %s: %w", dir, err)
	}

	result := &LoadResult{}
	var manifests []SkillManifest

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		manifest, body, err := pl.parseManifestFile(path)
		if err != nil {
			pl.logger.WarnContext(ctx, "pack: parse error",
				slog.String("file", path),
				slog.String("error", err.Error()),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		// Validate version.
		if err := manifest.ValidateVersion(); err != nil {
			pl.logger.WarnContext(ctx, "pack: version error",
				slog.String("file", path),
				slog.String("error", err.Error()),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		// Validate integrity.
		if err := manifest.ValidateIntegrity(body); err != nil {
			pl.logger.WarnContext(ctx, "pack: integrity error",
				slog.String("file", path),
				slog.String("error", err.Error()),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		// Standard skill validation (name, role, tools, risk level, budget).
		def := manifest.ToSkillDefinition()
		if err := pl.loader.Validate(def); err != nil {
			pl.logger.WarnContext(ctx, "pack: validation error",
				slog.String("file", path),
				slog.String("skill", manifest.Name),
				slog.String("error", err.Error()),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		// Validate permissions against SecurityPolicy.
		if err := pl.ValidatePermissions(ctx, manifest); err != nil {
			pl.logger.WarnContext(ctx, "pack: permission denied",
				slog.String("file", path),
				slog.String("skill", manifest.Name),
				slog.String("error", err.Error()),
			)
			result.Errors = append(result.Errors, LoadError{File: path, Message: err.Error()})
			continue
		}

		pl.logger.DebugContext(ctx, "community skill loaded",
			slog.String("skill_key", manifest.SkillKey),
			slog.String("name", manifest.Name),
			slog.String("version", manifest.Version),
			slog.String("author", manifest.Author),
		)

		manifests = append(manifests, *manifest)
		result.Loaded++
	}

	return manifests, result, nil
}

// ValidatePermissions checks that the skill's requested permissions
// are allowed under the current SecurityPolicy for the agent.
func (pl *PackLoader) ValidatePermissions(ctx context.Context, manifest *SkillManifest) error {
	if pl.policyEnforcer == nil {
		return nil // No policy = allow all.
	}

	for _, toolName := range manifest.ToolsRequired {
		if err := pl.policyEnforcer.CheckToolAllowed(ctx, pl.agentID, toolName); err != nil {
			return fmt.Errorf("skill %q requires tool %q which is denied by policy: %w",
				manifest.Name, toolName, err)
		}
	}
	for _, domain := range manifest.RequestedDomains {
		if err := pl.policyEnforcer.CheckDomainAllowed(ctx, pl.agentID, domain); err != nil {
			return fmt.Errorf("skill %q requires domain %q which is denied by policy: %w",
				manifest.Name, domain, err)
		}
	}
	for _, path := range manifest.RequestedPaths {
		if err := pl.policyEnforcer.CheckPathAllowed(ctx, pl.agentID, path); err != nil {
			return fmt.Errorf("skill %q requires path %q which is denied by policy: %w",
				manifest.Name, path, err)
		}
	}
	return nil
}

// parseManifestFile reads a Markdown file and parses extended SkillManifest frontmatter.
// Returns the manifest and the raw body for integrity validation.
func (pl *PackLoader) parseManifestFile(path string) (*SkillManifest, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	if !scanner.Scan() {
		return nil, "", fmt.Errorf("empty file")
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return nil, "", fmt.Errorf("missing YAML frontmatter (file must start with ---)")
	}

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
		return nil, "", fmt.Errorf("unclosed YAML frontmatter (missing closing ---)")
	}

	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("reading file: %w", err)
	}

	frontmatter := strings.Join(frontmatterLines, "\n")
	manifest := &SkillManifest{}
	if err := yaml.Unmarshal([]byte(frontmatter), manifest); err != nil {
		return nil, "", fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	manifest.Description = body
	manifest.SourceFile = path
	manifest.SkillKey = filenameStem(path)

	return manifest, body, nil
}
