// Package code implements sandboxed code execution.
//
// Security:
//   - All code runs inside the process sandbox (isolation, timeout, resource limits)
//   - No network access (sandbox default)
//   - Only configured languages allowed
//   - Code written to temp file inside sandbox, never on the host
//   - Output truncated to prevent OOM
package code

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// interpreters maps language names to their interpreter commands.
var interpreters = map[string]string{
	"python3": "python3",
	"python":  "python3",
	"sh":      "sh",
	"bash":    "bash",
}

// Config configures the code execution tool.
type Config struct {
	AllowedLanguages []string // Languages that can be executed. Empty = deny all.
}

// Tool executes code snippets inside a sandbox.
type Tool struct {
	config  Config
	sandbox sandbox.Sandbox
	logger  *slog.Logger
	allowed map[string]bool // pre-computed set from AllowedLanguages
}

// NewTool creates a sandboxed code execution tool.
func NewTool(cfg Config, sbx sandbox.Sandbox, logger *slog.Logger) *Tool {
	allowed := make(map[string]bool, len(cfg.AllowedLanguages))
	for _, lang := range cfg.AllowedLanguages {
		allowed[lang] = true
	}
	return &Tool{
		config:  cfg,
		sandbox: sbx,
		logger:  logger,
		allowed: allowed,
	}
}

func (t *Tool) Name() string        { return "code_exec" }
func (t *Tool) Description() string { return "Execute code in a sandboxed environment (no network)" }
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{"type": "string", "description": "Programming language (e.g. 'python3', 'sh', 'bash')"},
			"code":     map[string]any{"type": "string", "description": "The source code to execute"},
		},
		"required": []string{"language", "code"},
	}
}
func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "code_exec", RiskLevel: security.RiskHigh}
}
func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *Tool) Validate(params map[string]any) error {
	lang, err := requireString(params, "language")
	if err != nil {
		return err
	}
	if !t.allowed[lang] {
		return fmt.Errorf("language %q is not allowed; permitted: %v", lang, t.config.AllowedLanguages)
	}
	if _, ok := interpreters[lang]; !ok {
		return fmt.Errorf("no interpreter configured for language %q", lang)
	}
	if _, err := requireString(params, "code"); err != nil {
		return err
	}
	return nil
}

// Execute writes the code to a temp file inside the sandbox and runs it.
//
// Required params:
//
//	"language" (string) — one of the allowed languages (e.g. "python3", "sh")
//	"code" (string) — the source code to execute
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	lang, _ := requireString(params, "language")
	code, _ := requireString(params, "code")

	interpreter := interpreters[lang]

	t.logger.InfoContext(ctx, "code_exec executing",
		slog.String("language", lang),
		slog.Int("code_size", len(code)),
	)

	// Strategy: pipe the code via stdin to the interpreter.
	// This avoids writing a temp file and the associated cleanup.
	// The sandbox already provides an isolated temp dir as HOME.
	//
	// We use: sh -c 'echo "$CODE" | interpreter'
	// But that's shell-injection-prone. Instead, use the sandbox command
	// to run the interpreter directly, and pass code via a here-string
	// approach using the shell wrapper the sandbox already applies.
	//
	// Safest: write to a file via the command itself.
	// printf '%s' "$1" > /tmp/code && interpreter /tmp/code
	// Using positional params to avoid injection.
	shellScript := fmt.Sprintf(`printf '%%s' "$1" > "$HOME/script" && %s "$HOME/script"`, interpreter)

	req := sandbox.ExecutionRequest{
		Command: []string{"sh", "-c", shellScript, "_", code},
	}

	result, err := t.sandbox.Execute(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution: %w", err)
	}

	output := result.Stdout
	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += result.Stderr
	}

	return &tools.Result{
		Output:  tools.TruncateOutput(output, tools.MaxOutputBytes),
		Success: result.ExitCode == 0,
		Metadata: map[string]any{
			"language":  lang,
			"exit_code": result.ExitCode,
			"duration":  result.Duration.String(),
		},
	}, nil
}

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
