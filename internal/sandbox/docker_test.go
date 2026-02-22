package sandbox

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// testImage is the Docker image used for integration tests.
const testImage = "jkaninda/akili-runtime:latest"

// skipIfNoDocker skips the test if Docker is unavailable.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available, skipping integration test")
	}
}

// skipIfNoImage skips the test if the runtime image isn't built.
func skipIfNoImage(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "images", "-q", testImage).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("docker image %s not found, skipping (build with: docker build -t %s -f docker/Dockerfile.runtime .)", testImage, testImage)
	}
}

func newTestDockerSandbox(t *testing.T) *DockerSandbox {
	t.Helper()
	skipIfNoDocker(t)
	skipIfNoImage(t)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewDockerSandbox(DockerConfig{
		Image:          testImage,
		DefaultTimeout: 30 * time.Second,
		MemoryMB:       64,
		CPUCores:       0.5,
		PIDsLimit:      32,
		NetworkAllowed: false,
	}, logger)
}

func TestDockerSandbox_BasicExecution(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	if got := strings.TrimSpace(result.Stdout); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

func TestDockerSandbox_NonZeroExit(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"sh", "-c", "exit 42"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("exit code = %d, want 42", result.ExitCode)
	}
}

func TestDockerSandbox_Timeout(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	_, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"sleep", "60"},
		Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want timeout error", err.Error())
	}
}

func TestDockerSandbox_MemoryLimit(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	// Allocate more memory than the 64MB limit. Python's bytearray is
	// a simple way to consume memory. The container should be OOM-killed (exit 137).
	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"python3", "-c", "x = bytearray(128 * 1024 * 1024)"},
	})
	if err != nil {
		// OOM kill might surface as an error depending on timing.
		t.Logf("got error (acceptable for OOM): %v", err)
		return
	}
	// Exit code 137 = killed by SIGKILL (OOM killer).
	if result.ExitCode != 137 {
		t.Errorf("exit code = %d, want 137 (OOM killed)", result.ExitCode)
	}
}

func TestDockerSandbox_ReadOnlyFS(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"sh", "-c", "touch /etc/test 2>&1; echo $?"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The touch should fail because root FS is read-only.
	// The echo $? should print a non-zero exit code.
	if strings.TrimSpace(result.Stdout) == "0" {
		t.Error("touch /etc/test should have failed on read-only filesystem")
	}
}

func TestDockerSandbox_NoNetwork(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"sh", "-c", "wget -q -O- http://1.1.1.1 2>&1 || echo NETWORK_BLOCKED"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		// Timeout or error is acceptable — no network means no connection.
		t.Logf("got error (acceptable for no network): %v", err)
		return
	}
	output := result.Stdout + result.Stderr
	if !strings.Contains(output, "NETWORK_BLOCKED") && !strings.Contains(output, "Network is unreachable") && !strings.Contains(output, "bad address") {
		t.Errorf("expected network failure, got: %s", output)
	}
}

func TestDockerSandbox_NonRoot(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"id", "-u"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "65534" {
		t.Errorf("uid = %q, want %q (non-root)", got, "65534")
	}
}

func TestDockerSandbox_ContainerCleanup(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	// Run a command and capture the container name from logs.
	// We can verify cleanup by checking docker ps -a after.
	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"hostname"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The hostname inside the container is the container ID (short form).
	// But with --name, it might be the name. Either way, check that no
	// akili-sbx containers are left running.
	_ = result

	out, err := exec.Command("docker", "ps", "-a", "--filter", "name=akili-sbx", "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps failed: %v", err)
	}
	if names := strings.TrimSpace(string(out)); names != "" {
		t.Errorf("found leftover containers: %s", names)
	}
}

func TestDockerSandbox_EnvPropagation(t *testing.T) {
	sbx := newTestDockerSandbox(t)
	ctx := context.Background()

	result, err := sbx.Execute(ctx, ExecutionRequest{
		Command: []string{"sh", "-c", "echo $MY_VAR"},
		Env:     map[string]string{"MY_VAR": "test_value"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(result.Stdout); got != "test_value" {
		t.Errorf("env MY_VAR = %q, want %q", got, "test_value")
	}
}
