// Package shell implements the sandboxed shell execution tool.
// All commands run through the sandbox — never directly on the host.
package shell

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Tool executes shell commands inside a sandbox.
type Tool struct {
	sandbox sandbox.Sandbox
	logger  *slog.Logger
}

// NewTool creates a shell tool that delegates all execution to the given sandbox.
func NewTool(sbx sandbox.Sandbox, logger *slog.Logger) *Tool {
	return &Tool{
		sandbox: sbx,
		logger:  logger,
	}
}

func (t *Tool) Name() string       { return "shell_exec" }
func (t *Tool) Description() string { return "Execute a shell command in a sandboxed environment" }
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command":     map[string]any{"type": "string", "description": "The shell command to execute"},
			"timeout":     map[string]any{"type": "string", "description": "Duration string (e.g. '10s', '1m'), overrides default timeout"},
			"working_dir": map[string]any{"type": "string", "description": "Working directory override"},
		},
		"required": []string{"command"},
	}
}
func (t *Tool) RequiredAction() security.Action {
	return security.Action{
		Name:      "shell_exec",
		RiskLevel: security.RiskHigh,
	}
}

// EstimateCost returns 0 — shell commands don't have a monetary cost.
// LLM token costs are tracked separately by the agent.
func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

// Validate checks that required params are present and well-formed.
func (t *Tool) Validate(params map[string]any) error {
	if _, err := requireString(params, "command"); err != nil {
		return err
	}
	if timeout, ok := params["timeout"].(string); ok && timeout != "" {
		if _, err := time.ParseDuration(timeout); err != nil {
			return fmt.Errorf("invalid timeout %q: %w", timeout, err)
		}
	}
	return nil
}

// Execute runs the command through the sandbox.
//
// Required params:
//
//	"command" (string) — the shell command to execute
//
// Optional params:
//
//	"timeout" (string) — duration string (e.g. "10s", "1m"), overrides default
//	"working_dir" (string) — working directory override
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	command, err := requireString(params, "command")
	if err != nil {
		return nil, err
	}

	req := sandbox.ExecutionRequest{
		// The command runs inside sh -c, which the sandbox further wraps
		// with ulimit enforcement. This double-wrapping is intentional:
		// the outer sh applies resource limits, the inner sh interprets
		// the user's command string (pipes, redirects, etc.).
		Command: []string{"sh", "-c", command},
	}

	if timeout, ok := params["timeout"].(string); ok && timeout != "" {
		d, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, fmt.Errorf("invalid timeout %q: %w", timeout, err)
		}
		req.Timeout = d
	}

	if dir, ok := params["working_dir"].(string); ok {
		req.WorkingDir = dir
	}

	t.logger.InfoContext(ctx, "shell tool executing",
		slog.String("command", command),
	)

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
		Output:  output,
		Success: result.ExitCode == 0,
		Metadata: map[string]any{
			"exit_code": result.ExitCode,
			"duration":  result.Duration.String(),
		},
	}, nil
}

// requireString extracts a required string parameter.
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
