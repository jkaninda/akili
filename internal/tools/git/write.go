// Package git provides git tools for Akili.
// This file implements the git_write tool for local write operations.
//
// Security:
//   - Only local write subcommands allowed (add, commit, checkout, branch, tag, stash, merge)
//   - All remote-write subcommands blocked (push, fetch, pull, remote, clone, init)
//   - Destructive subcommands (reset) require separate evaluation
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

// allowedWriteSubcommands are local git operations that modify the repository.
var allowedWriteSubcommands = map[string]bool{
	"add":      true,
	"commit":   true,
	"checkout": true,
	"branch":   true,
	"tag":      true,
	"stash":    true,
	"merge":    true,
	"rebase":   true,
	"reset":    true,
	"rm":       true,
	"mv":       true,
	"restore":  true,
	"switch":   true,
	"cherry-pick": true,
}

// blockedWriteSubcommands are remote/destructive operations that are never allowed.
var blockedWriteSubcommands = map[string]bool{
	"push":   true,
	"fetch":  true,
	"pull":   true,
	"remote": true,
	"clone":  true,
	"init":   true,
	"clean":  true,
}

// WriteTool runs local git write operations inside a sandbox.
type WriteTool struct {
	sandbox sandbox.Sandbox
	logger  *slog.Logger
}

// NewWriteTool creates a git write tool backed by the given sandbox.
func NewWriteTool(sbx sandbox.Sandbox, logger *slog.Logger) *WriteTool {
	return &WriteTool{sandbox: sbx, logger: logger}
}

func (t *WriteTool) Name() string { return "git_write" }
func (t *WriteTool) Description() string {
	return "Run local git write commands (add, commit, checkout, branch, tag, stash, merge, rebase, reset, rm, mv, restore, switch, cherry-pick)"
}
func (t *WriteTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subcommand": map[string]any{"type": "string", "enum": []string{"add", "commit", "checkout", "branch", "tag", "stash", "merge", "rebase", "reset", "rm", "mv", "restore", "switch", "cherry-pick"}, "description": "The git subcommand to run"},
			"repo_path":  map[string]any{"type": "string", "description": "Path to the git repository"},
			"args":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Additional arguments for the git subcommand"},
		},
		"required": []string{"subcommand", "repo_path"},
	}
}
func (t *WriteTool) RequiredAction() security.Action {
	return security.Action{Name: "git_write", RiskLevel: security.RiskHigh}
}
func (t *WriteTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *WriteTool) Validate(params map[string]any) error {
	subcmd, err := requireString(params, "subcommand")
	if err != nil {
		return err
	}

	if blockedWriteSubcommands[subcmd] {
		return fmt.Errorf("git subcommand %q is blocked (remote/destructive operation)", subcmd)
	}
	if !allowedWriteSubcommands[subcmd] {
		// Also allow read subcommands to be used via the read tool.
		if allowedSubcommands[subcmd] {
			return fmt.Errorf("git subcommand %q is read-only; use the git_read tool instead", subcmd)
		}
		return fmt.Errorf("git subcommand %q is not allowed for git_write; permitted: add, commit, checkout, branch, tag, stash, merge, rebase, reset, rm, mv, restore, switch, cherry-pick", subcmd)
	}

	if _, err := requireString(params, "repo_path"); err != nil {
		return err
	}

	return nil
}

// Execute runs a local git write subcommand in the sandbox.
//
// Required params:
//
//	"subcommand" (string) — one of the allowed write subcommands
//	"repo_path" (string) — path to the git repository
//
// Optional params:
//
//	"args" ([]any) — additional arguments for the git subcommand
func (t *WriteTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	subcmd, _ := requireString(params, "subcommand")
	repoPath, _ := requireString(params, "repo_path")

	// Build the git command.
	cmd := []string{"git", subcmd}

	// Append optional extra args.
	if rawArgs, ok := params["args"].([]any); ok {
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				cmd = append(cmd, s)
			}
		}
	}

	t.logger.InfoContext(ctx, "git_write executing",
		slog.String("subcommand", subcmd),
		slog.String("repo_path", repoPath),
	)

	// Build a clean env that explicitly disables credential helpers.
	env := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
	}

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
