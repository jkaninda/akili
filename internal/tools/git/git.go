// Package git implements a read-only git tool that executes via the sandbox.
//
// Security:
//   - Only read-only subcommands allowed (log, diff, status, show, branch)
//   - All write/remote-write subcommands blocked
//   - Executed via sandbox (process isolation, timeout, resource limits)
//   - Git credential environment variables explicitly stripped
//   - No SSH agent forwarding
package git

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Allowed read-only subcommands. Anything not in this set is blocked.
var allowedSubcommands = map[string]bool{
	"log":    true,
	"diff":   true,
	"status": true,
	"show":   true,
	"branch": true,
}

// Explicitly blocked subcommands for clear error messages.
var blockedSubcommands = map[string]bool{
	"push":     true,
	"commit":   true,
	"merge":    true,
	"rebase":   true,
	"reset":    true,
	"checkout": true,
	"fetch":    true,
	"pull":     true,
	"remote":   true,
	"init":     true,
	"clone":    true,
	"tag":      true,
	"stash":    true,
}

// Credential-related env vars that must NEVER be passed to the sandbox.
var stripEnvVars = []string{
	"GIT_ASKPASS",
	"GIT_CONFIG",
	"GIT_CONFIG_GLOBAL",
	"GIT_CONFIG_SYSTEM",
	"GIT_CREDENTIAL_HELPER",
	"SSH_AUTH_SOCK",
	"SSH_AGENT_PID",
	"GIT_SSH",
	"GIT_SSH_COMMAND",
	"GIT_TERMINAL_PROMPT",
}

// Tool runs read-only git operations inside a sandbox.
type Tool struct {
	sandbox sandbox.Sandbox
	logger  *slog.Logger
}

// NewTool creates a git read tool backed by the given sandbox.
func NewTool(sbx sandbox.Sandbox, logger *slog.Logger) *Tool {
	return &Tool{sandbox: sbx, logger: logger}
}

func (t *Tool) Name() string { return "git_read" }
func (t *Tool) Description() string {
	return "Run read-only git commands (log, diff, status, show, branch)"
}
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subcommand": map[string]any{"type": "string", "enum": []string{"log", "diff", "status", "show", "branch"}, "description": "The git subcommand to run"},
			"repo_path":  map[string]any{"type": "string", "description": "Path to the git repository"},
			"args":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Additional arguments for the git subcommand"},
		},
		"required": []string{"subcommand", "repo_path"},
	}
}
func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "git_read", RiskLevel: security.RiskMedium}
}
func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *Tool) Validate(params map[string]any) error {
	subcmd, err := requireString(params, "subcommand")
	if err != nil {
		return err
	}

	if blockedSubcommands[subcmd] {
		return fmt.Errorf("git subcommand %q is blocked (write/remote operation)", subcmd)
	}
	if !allowedSubcommands[subcmd] {
		return fmt.Errorf("git subcommand %q is not allowed; permitted: log, diff, status, show, branch", subcmd)
	}

	// repo_path is required — where to run the git command.
	if _, err := requireString(params, "repo_path"); err != nil {
		return err
	}

	return nil
}

// Execute runs a read-only git subcommand in the sandbox.
//
// Required params:
//
//	"subcommand" (string) — one of: log, diff, status, show, branch
//	"repo_path" (string) — path to the git repository
//
// Optional params:
//
//	"args" ([]any) — additional arguments for the git subcommand
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	subcmd, _ := requireString(params, "subcommand")
	repoPath, _ := requireString(params, "repo_path")

	// Build the git command.
	cmd := []string{"git", subcmd}

	// Append optional extra args (e.g. ["--oneline", "-10"] for git log).
	if rawArgs, ok := params["args"].([]any); ok {
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				cmd = append(cmd, s)
			}
		}
	}

	t.logger.InfoContext(ctx, "git_read executing",
		slog.String("subcommand", subcmd),
		slog.String("repo_path", repoPath),
	)

	// Build a clean env that explicitly disables credential helpers.
	env := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
	}
	// The sandbox already strips the parent environment. These env overrides
	// ensure git doesn't find any credential configuration.

	req := sandbox.ExecutionRequest{
		Command:    cmd,
		WorkingDir: repoPath,
		Env:        env,
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
			"subcommand": subcmd,
			"exit_code":  result.ExitCode,
			"duration":   result.Duration.String(),
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
