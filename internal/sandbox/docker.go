package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"time"
)

const (
	defaultDockerPIDsLimit = 64
	defaultDockerCPUCores  = 1.0
	defaultDockerImage     = "akili-runtime:latest"
)

// DockerConfig configures the Docker-based sandbox.
type DockerConfig struct {
	Image          string        // Container image (e.g. "akili-runtime:latest").
	DefaultTimeout time.Duration // Wall-clock timeout per execution.
	MemoryMB       int           // --memory hard limit.
	CPUCores       float64       // --cpus rate limit (e.g. 0.5 = half a core).
	PIDsLimit      int           // --pids-limit (prevents fork bombs).
	NetworkAllowed bool          // false = --network=none (no network stack at all).
}

// DockerSandbox executes commands inside ephemeral Docker containers.
//
// Security guarantees:
//   - Each execution gets its own container (--rm, plus deferred docker rm -f safety net)
//   - ALL Linux capabilities dropped (--cap-drop=ALL)
//   - Read-only root filesystem (--read-only) with tmpfs for writable dirs
//   - Privilege escalation blocked (--security-opt=no-new-privileges)
//   - Non-root user (--user=65534:65534)
//   - No host PID namespace, no docker socket mount, no privileged mode
//   - Network disabled by default (--network=none)
//   - Memory hard limit with no swap (OOM kill on exceed)
//   - PIDs limit prevents fork bombs
//   - CPU rate limited
//   - stdout/stderr capped to prevent OOM on the host
//   - Container always cleaned up, even on timeout/crash
type DockerSandbox struct {
	config DockerConfig
	logger *slog.Logger
}

// NewDockerSandbox creates a Docker-based sandbox.
func NewDockerSandbox(cfg DockerConfig, logger *slog.Logger) *DockerSandbox {
	if cfg.Image == "" {
		cfg.Image = defaultDockerImage
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = defaultTimeout
	}
	if cfg.MemoryMB == 0 {
		cfg.MemoryMB = defaultMemoryMB
	}
	if cfg.CPUCores <= 0 {
		cfg.CPUCores = defaultDockerCPUCores
	}
	if cfg.PIDsLimit <= 0 {
		cfg.PIDsLimit = defaultDockerPIDsLimit
	}
	return &DockerSandbox{
		config: cfg,
		logger: logger,
	}
}

// Execute runs a command inside an ephemeral Docker container with full hardening.
func (s *DockerSandbox) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	if len(req.Command) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	// 1. Apply timeout.
	timeout := req.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 2. Generate unique container name.
	containerName, err := generateContainerName()
	if err != nil {
		return nil, fmt.Errorf("generating container name: %w", err)
	}

	// 3. Resolve resource limits.
	memoryMB := s.config.MemoryMB
	if req.Limits.MaxMemoryMB > 0 {
		memoryMB = req.Limits.MaxMemoryMB
	}

	// 4. Build docker run command with all security flags.
	args := s.buildDockerArgs(containerName, memoryMB, req)
	args = append(args, req.Command...)

	cmd := exec.CommandContext(ctx, "docker", args...)

	// Kill the docker process on context cancellation.
	// Docker will also stop the container since the client disconnects.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}

	// 5. Capture stdout/stderr with size cap.
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdoutBuf, remaining: maxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderrBuf, remaining: maxOutputBytes}

	// 6. Execute and measure.
	s.logger.Info("docker sandbox executing",
		slog.String("container", containerName),
		slog.String("image", s.config.Image),
		slog.Any("command", req.Command),
		slog.Int("memory_mb", memoryMB),
		slog.Float64("cpu_cores", s.config.CPUCores),
		slog.Duration("timeout", timeout),
	)

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	// 7. Safety net: force remove the container in case --rm didn't fire
	// (e.g., OOM kill, daemon restart, context cancel race).
	s.forceRemoveContainer(containerName)

	// 8. Interpret result.
	exitCode := 0
	if runErr != nil {
		if ctx.Err() != nil {
			s.logger.Warn("docker sandbox timed out",
				slog.String("container", containerName),
				slog.Duration("timeout", timeout),
				slog.Duration("duration", duration),
			)
			return nil, fmt.Errorf("execution timed out after %s", timeout)
		}

		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker execution failed: %w", runErr)
		}
	}

	s.logger.Info("docker sandbox completed",
		slog.String("container", containerName),
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

// buildDockerArgs constructs the full docker run argument list with all
// security hardening flags. The command itself is NOT included — caller appends it.
func (s *DockerSandbox) buildDockerArgs(name string, memoryMB int, req ExecutionRequest) []string {
	memoryFlag := strconv.Itoa(memoryMB) + "m"
	cpuFlag := strconv.FormatFloat(s.config.CPUCores, 'f', 2, 64)
	pidsFlag := strconv.Itoa(s.config.PIDsLimit)

	args := []string{
		"run", "--rm",
		"--name", name,

		// --- Security hardening ---
		"--cap-drop=ALL",                   // Drop all 38+ Linux capabilities.
		"--security-opt=no-new-privileges", // Block setuid/setgid escalation.
		"--read-only",                      // Read-only root filesystem.
		"--user=65534:65534",               // Non-root (nobody).

		// --- Resource limits ---
		"--memory=" + memoryFlag,      // Hard memory limit.
		"--memory-swap=" + memoryFlag, // Same as memory = disable swap (OOM kill).
		"--cpus=" + cpuFlag,           // CPU rate limit.
		"--pids-limit=" + pidsFlag,    // Fork bomb protection.

		// --- Writable tmpfs for working directories ---
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
		"--tmpfs", "/home/sandbox:rw,noexec,nosuid,size=64m",

		// --- Sanitized environment (no host inheritance) ---
		"--env", "HOME=/home/sandbox",
		"--env", "PATH=/usr/local/bin:/usr/bin:/bin",
		"--env", "LANG=en_US.UTF-8",
		"--env", "TERM=dumb",
	}

	// Network policy: disabled by default (no network stack at all).
	if s.config.NetworkAllowed {
		args = append(args, "--network=bridge")
	} else {
		args = append(args, "--network=none")
	}

	// Working directory.
	if req.WorkingDir != "" {
		args = append(args, "--workdir", req.WorkingDir)
	} else {
		args = append(args, "--workdir", "/home/sandbox")
	}

	// Extra environment variables from the request.
	for k, v := range req.Env {
		args = append(args, "--env", k+"="+v)
	}

	// Image (must come after all flags, before command).
	args = append(args, s.config.Image)

	return args
}

// forceRemoveContainer attempts to remove a container by name.
// This is a safety net — if --rm didn't fire due to OOM kill, daemon
// restart, or context cancel race, this ensures no container leakage.
// Errors are logged but not returned (best-effort cleanup).
func (s *DockerSandbox) forceRemoveContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
	if err != nil {
		// "No such container" is expected when --rm already cleaned up.
		if !bytes.Contains(out, []byte("No such container")) {
			s.logger.Warn("docker rm -f failed",
				slog.String("container", name),
				slog.String("error", err.Error()),
				slog.String("output", string(out)),
			)
		}
	}
}

// generateContainerName returns a unique container name: akili-sbx-<16 hex chars>.
func generateContainerName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "akili-sbx-" + hex.EncodeToString(b), nil
}
