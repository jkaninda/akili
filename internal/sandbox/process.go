package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	// maxOutputBytes caps stdout/stderr to prevent OOM from chatty commands.
	maxOutputBytes = 1 << 20 // 1 MB

	defaultTimeout    = 30 * time.Second
	defaultCPUSeconds = 60
	defaultMemoryMB   = 512
)

// ProcessConfig configures the process-based sandbox.
type ProcessConfig struct {
	DefaultTimeout time.Duration
	DefaultLimits  ResourceLimits
}

// ProcessSandbox executes commands as isolated OS processes.
//
// Security guarantees:
//   - Each execution gets its own temp directory (removed after)
//   - Process runs in its own process group (Setpgid)
//   - Entire process group killed on timeout/cancel
//   - No environment inheritance from parent — only a minimal safe set
//   - Resource limits enforced via ulimit
//   - stdout/stderr capped to prevent OOM
type ProcessSandbox struct {
	defaultTimeout time.Duration
	defaultLimits  ResourceLimits
	logger         *slog.Logger
}

// NewProcessSandbox creates a process-based sandbox.
func NewProcessSandbox(cfg ProcessConfig, logger *slog.Logger) *ProcessSandbox {
	timeout := cfg.DefaultTimeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	limits := cfg.DefaultLimits
	if limits.MaxCPUSeconds == 0 {
		limits.MaxCPUSeconds = defaultCPUSeconds
	}
	if limits.MaxMemoryMB == 0 {
		limits.MaxMemoryMB = defaultMemoryMB
	}

	return &ProcessSandbox{
		defaultTimeout: timeout,
		defaultLimits:  limits,
		logger:         logger,
	}
}

// Execute runs a command in an isolated process environment.
func (s *ProcessSandbox) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	if len(req.Command) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	// 1. Apply timeout.
	timeout := req.Timeout
	if timeout == 0 {
		timeout = s.defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 2. Create isolated temp directory.
	tmpDir, err := os.MkdirTemp("", "akili-sandbox-*")
	if err != nil {
		return nil, fmt.Errorf("creating sandbox temp dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			s.logger.Warn("failed to remove sandbox temp dir",
				slog.String("dir", tmpDir),
				slog.String("error", rmErr.Error()),
			)
		}
	}()

	// 3. Resolve resource limits.
	limits := s.resolveLimits(req.Limits)

	// 4. Build the command with ulimit resource enforcement.
	//
	// The command is wrapped: sh -c 'ulimit -v KB 2>/dev/null; ulimit -t SEC 2>/dev/null; exec "$@"' _ cmd args...
	//
	// Using exec "$@" with positional parameters prevents shell injection —
	// the user's command is never interpolated into the shell string.
	memKB := limits.MaxMemoryMB * 1024
	shellScript := fmt.Sprintf(
		"ulimit -v %d 2>/dev/null; ulimit -t %d 2>/dev/null; exec \"$@\"",
		memKB, limits.MaxCPUSeconds,
	)
	args := make([]string, 0, 3+len(req.Command))
	args = append(args, "-c", shellScript, "_") // "_" is the $0 placeholder
	args = append(args, req.Command...)

	cmd := exec.CommandContext(ctx, "/bin/sh", args...)

	// 5. Working directory: use caller's override or the isolated temp dir.
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	} else {
		cmd.Dir = tmpDir
	}

	// 6. Process group isolation — the child runs in its own group.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Kill the entire process group on context cancellation (timeout/cancel).
	// This ensures child processes spawned by the command are also terminated.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID = kill the entire process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// 7. Sanitized environment — NO inheritance from the host process.
	cmd.Env = s.buildEnv(tmpDir, req.Env)

	// 8. Capture stdout/stderr with size cap.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, remaining: maxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, remaining: maxOutputBytes}

	// 9. Execute and measure duration.
	s.logger.Info("sandbox executing",
		slog.Any("command", req.Command),
		slog.String("dir", cmd.Dir),
		slog.Int("memory_limit_mb", limits.MaxMemoryMB),
		slog.Int("cpu_limit_sec", limits.MaxCPUSeconds),
		slog.Duration("timeout", timeout),
	)

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	// 10. Interpret the result.
	exitCode := 0
	if runErr != nil {
		// Check for timeout first.
		if ctx.Err() != nil {
			s.logger.Warn("sandbox execution timed out",
				slog.Duration("timeout", timeout),
				slog.Duration("duration", duration),
			)
			return nil, fmt.Errorf("execution timed out after %s", timeout)
		}

		// Non-zero exit code is not an error — it's a result.
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("execution failed: %w", runErr)
		}
	}

	s.logger.Info("sandbox execution completed",
		slog.Int("exit_code", exitCode),
		slog.Duration("duration", duration),
		slog.Int("stdout_bytes", stdoutBuf.Len()),
		slog.Int("stderr_bytes", stderrBuf.Len()),
	)

	return &ExecutionResult{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// resolveLimits merges request-level overrides with sandbox defaults.
func (s *ProcessSandbox) resolveLimits(req ResourceLimits) ResourceLimits {
	limits := s.defaultLimits
	if req.MaxCPUSeconds > 0 {
		limits.MaxCPUSeconds = req.MaxCPUSeconds
	}
	if req.MaxMemoryMB > 0 {
		limits.MaxMemoryMB = req.MaxMemoryMB
	}
	return limits
}

// buildEnv constructs a minimal, safe environment.
// The parent process's environment is NEVER inherited — this prevents
// API keys, credentials, and other secrets from leaking into sandboxed commands.
func (s *ProcessSandbox) buildEnv(tmpDir string, extra map[string]string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + tmpDir,
		"TMPDIR=" + tmpDir,
		"LANG=en_US.UTF-8",
		"TERM=dumb",
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// limitedWriter wraps a writer and stops writing after a byte limit.
// Excess data is silently discarded (not an error — just capped).
type limitedWriter struct {
	w         io.Writer
	remaining int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return len(p), nil // Silently discard.
	}
	if len(p) > lw.remaining {
		p = p[:lw.remaining]
	}
	n, err := lw.w.Write(p)
	lw.remaining -= n
	return n, err
}
