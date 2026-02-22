// Package sandbox provides isolated execution environments for tool commands.
// All external commands run through a sandbox — never directly on the host.
package sandbox

import (
	"context"
	"time"
)

// Sandbox executes commands in an isolated environment.
type Sandbox interface {
	Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)
}

// ExecutionRequest defines what to run and under what constraints.
type ExecutionRequest struct {
	// Command is the program and arguments to execute (e.g. ["ls", "-la"]).
	Command []string

	// WorkingDir overrides the working directory. Empty = use isolated temp dir.
	WorkingDir string

	// Env adds extra environment variables to the sanitized base set.
	// These are merged on top of the sandbox's minimal safe environment.
	Env map[string]string

	// Timeout overrides the sandbox default. Zero = use default.
	Timeout time.Duration

	// Limits overrides resource limits. Zero values = use sandbox defaults.
	Limits ResourceLimits
}

// ResourceLimits constrains the sandboxed process.
type ResourceLimits struct {
	MaxCPUSeconds int // CPU time limit (ulimit -t).
	MaxMemoryMB   int // Virtual memory limit in MB (ulimit -v).
}

// ExecutionResult captures the outcome of a sandboxed command.
type ExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}
