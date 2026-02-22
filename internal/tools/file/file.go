// Package file implements file access tools with path restriction and symlink protection.
//
// Two separate tools are registered to match the RBAC model:
//   - file_read: read/list operations (RiskLow, requires "read_files")
//   - file_write: write operations (RiskMedium, requires "write_files")
//
// Security: every path is resolved to its absolute, symlink-free form and
// checked against the configured allowlist before any I/O occurs.
package file

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Config configures file tool restrictions.
type Config struct {
	AllowedPaths     []string // Path prefixes that are allowed. Empty = deny all.
	MaxFileSizeBytes int64    // Maximum file size for read/write. 0 = 10 MB default.
}

const defaultMaxFileSize = 10 << 20 // 10 MB

// --- Shared path validation ---

// safePath resolves a user-supplied path to its absolute, symlink-free form
// and verifies it falls within one of the allowed prefixes.
//
// This prevents:
//   - Path traversal via ../ sequences
//   - Symlink-based escapes (symlink pointing outside allowed dirs)
//   - Relative path tricks
func safePath(raw string, allowed []string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path must not be empty")
	}

	// Resolve to absolute path first (handles relative paths).
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}

	// Resolve symlinks to get the real filesystem path.
	// This prevents symlinks that point outside allowed directories.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// If the path doesn't exist yet (write case), resolve the parent.
		parentResolved, parentErr := filepath.EvalSymlinks(filepath.Dir(abs))
		if parentErr != nil {
			return "", fmt.Errorf("path does not exist and parent is invalid: %w", err)
		}
		resolved = filepath.Join(parentResolved, filepath.Base(abs))
	}

	// Check resolved path against allowlist.
	for _, prefix := range allowed {
		absPrefix, err := filepath.Abs(prefix)
		if err != nil {
			continue
		}
		// Ensure prefix matching is directory-safe:
		// "/tmp" should match "/tmp/foo" but NOT "/tmpevil".
		if strings.HasPrefix(resolved, absPrefix+string(filepath.Separator)) || resolved == absPrefix {
			return resolved, nil
		}
	}

	return "", fmt.Errorf("path %q resolves to %q which is outside allowed directories", raw, resolved)
}

func maxSize(cfg Config) int64 {
	if cfg.MaxFileSizeBytes > 0 {
		return cfg.MaxFileSizeBytes
	}
	return defaultMaxFileSize
}

// requireString extracts a required non-empty string param.
func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string, got %T", key, v)
	}
	if s == "" {
		return "", fmt.Errorf("parameter %s must not be empty", key)
	}
	return s, nil
}

// ---- ReadTool ----

// ReadTool reads files and lists directories within allowed paths.
type ReadTool struct {
	config Config
	logger *slog.Logger
}

// NewReadTool creates a file read tool restricted to the given paths.
func NewReadTool(cfg Config, logger *slog.Logger) *ReadTool {
	return &ReadTool{config: cfg, logger: logger}
}

func (t *ReadTool) Name() string { return "file_read" }
func (t *ReadTool) Description() string {
	return "Read file contents or list directory within allowed paths"
}
func (t *ReadTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string", "description": "Absolute path to the file or directory"},
			"operation": map[string]any{"type": "string", "enum": []string{"read", "list"}, "description": "Operation to perform: 'read' for file contents, 'list' for directory listing. Defaults to 'read'"},
		},
		"required": []string{"path"},
	}
}
func (t *ReadTool) RequiredAction() security.Action {
	return security.Action{Name: "read_files", RiskLevel: security.RiskLow}
}
func (t *ReadTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *ReadTool) Validate(params map[string]any) error {
	path, err := requireString(params, "path")
	if err != nil {
		return err
	}

	op := "read"
	if v, ok := params["operation"].(string); ok && v != "" {
		op = v
	}
	if op != "read" && op != "list" {
		return fmt.Errorf("operation must be \"read\" or \"list\", got %q", op)
	}

	if _, err := safePath(path, t.config.AllowedPaths); err != nil {
		return err
	}
	return nil
}

func (t *ReadTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	path, _ := requireString(params, "path")
	resolved, err := safePath(path, t.config.AllowedPaths)
	if err != nil {
		return nil, err
	}

	op := "read"
	if v, ok := params["operation"].(string); ok && v != "" {
		op = v
	}

	t.logger.InfoContext(ctx, "file_read executing",
		slog.String("operation", op),
		slog.String("path", resolved),
	)

	switch op {
	case "list":
		return t.listDir(resolved)
	default:
		return t.readFile(resolved)
	}
}

func (t *ReadTool) readFile(path string) (*tools.Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, use operation=\"list\"", path)
	}
	if info.Size() > maxSize(t.config) {
		return nil, fmt.Errorf("file size %d exceeds limit %d bytes", info.Size(), maxSize(t.config))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	output := tools.TruncateOutput(string(data), tools.MaxOutputBytes)

	return &tools.Result{
		Output:  output,
		Success: true,
		Metadata: map[string]any{
			"path":       path,
			"size_bytes": info.Size(),
		},
	}, nil
}

func (t *ReadTool) listDir(path string) (*tools.Result, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", path, err)
	}

	var b strings.Builder
	for _, e := range entries {
		info, _ := e.Info()
		mode := "-"
		size := int64(0)
		if info != nil {
			mode = info.Mode().String()
			size = info.Size()
		}
		fmt.Fprintf(&b, "%s %8d %s\n", mode, size, e.Name())
	}

	return &tools.Result{
		Output:  tools.TruncateOutput(b.String(), tools.MaxOutputBytes),
		Success: true,
		Metadata: map[string]any{
			"path":  path,
			"count": len(entries),
		},
	}, nil
}

// ---- WriteTool ----

// WriteTool writes files within allowed paths.
type WriteTool struct {
	config Config
	logger *slog.Logger
}

// NewWriteTool creates a file write tool restricted to the given paths.
func NewWriteTool(cfg Config, logger *slog.Logger) *WriteTool {
	return &WriteTool{config: cfg, logger: logger}
}

func (t *WriteTool) Name() string        { return "file_write" }
func (t *WriteTool) Description() string { return "Write content to a file within allowed paths" }
func (t *WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Absolute path to the file to write"},
			"content": map[string]any{"type": "string", "description": "Content to write to the file"},
		},
		"required": []string{"path", "content"},
	}
}
func (t *WriteTool) RequiredAction() security.Action {
	return security.Action{Name: "write_files", RiskLevel: security.RiskMedium}
}
func (t *WriteTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *WriteTool) Validate(params map[string]any) error {
	path, err := requireString(params, "path")
	if err != nil {
		return err
	}
	content, err := requireString(params, "content")
	if err != nil {
		return err
	}
	if int64(len(content)) > maxSize(t.config) {
		return fmt.Errorf("content size %d exceeds limit %d bytes", len(content), maxSize(t.config))
	}
	if _, err := safePath(path, t.config.AllowedPaths); err != nil {
		return err
	}
	return nil
}

func (t *WriteTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	path, _ := requireString(params, "path")
	content, _ := requireString(params, "content")

	resolved, err := safePath(path, t.config.AllowedPaths)
	if err != nil {
		return nil, err
	}

	t.logger.InfoContext(ctx, "file_write executing",
		slog.String("path", resolved),
		slog.Int("content_size", len(content)),
	)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(resolved), 0750); err != nil {
		return nil, fmt.Errorf("creating parent directory: %w", err)
	}

	if err := os.WriteFile(resolved, []byte(content), fs.FileMode(0640)); err != nil {
		return nil, fmt.Errorf("writing %s: %w", resolved, err)
	}

	return &tools.Result{
		Output:  fmt.Sprintf("wrote %d bytes to %s", len(content), resolved),
		Success: true,
		Metadata: map[string]any{
			"path":       resolved,
			"size_bytes": len(content),
		},
	}, nil
}
