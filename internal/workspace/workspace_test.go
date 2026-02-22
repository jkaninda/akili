package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "workspace")

	ws, err := New(root)
	if err != nil {
		t.Fatalf("New(%q): %v", root, err)
	}
	if ws.Root != root {
		t.Errorf("Root = %q, want %q", ws.Root, root)
	}

	// Root directory should exist.
	if _, err := os.Stat(root); err != nil {
		t.Errorf("root dir not created: %v", err)
	}
}

func TestDirectoryAccessors(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"SkillsDir", ws.SkillsDir, "skills"},
		{"SessionsDir", ws.SessionsDir, "sessions"},
		{"AgentsDir", ws.AgentsDir, "agents"},
		{"SandboxDir", ws.SandboxDir, "sandbox"},
		{"MCPDir", ws.MCPDir, "mcp"},
		{"CredentialsDir", ws.CredentialsDir, "credentials"},
		{"LogsDir", ws.LogsDir, "logs"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn()
			expected := filepath.Join(ws.Root, tc.want)
			if got != expected {
				t.Errorf("%s() = %q, want %q", tc.name, got, expected)
			}
			// Directory should exist.
			if _, err := os.Stat(got); err != nil {
				t.Errorf("directory not created: %v", err)
			}
		})
	}
}

func TestCredentialsDirPermissions(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	dir := ws.CredentialsDir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("credentials dir permissions = %o, want 0700", perm)
	}
}

func TestDerivedPaths(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := ws.ConfigPath(), filepath.Join(ws.Root, "config.json"); got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestAgentPaths(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	agentDir := ws.AgentDir("worker-1")
	expected := filepath.Join(ws.Root, "agents", "worker-1")
	if agentDir != expected {
		t.Errorf("AgentDir = %q, want %q", agentDir, expected)
	}
	if _, err := os.Stat(agentDir); err != nil {
		t.Errorf("agent dir not created: %v", err)
	}

	auditPath := ws.AgentAuditPath("worker-1")
	if auditPath != filepath.Join(expected, "audit.jsonl") {
		t.Errorf("AgentAuditPath = %q", auditPath)
	}
}

func TestSessionPaths(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	sessionDir := ws.SessionDir("user-1")
	expected := filepath.Join(ws.Root, "sessions", "user-1")
	if sessionDir != expected {
		t.Errorf("SessionDir = %q, want %q", sessionDir, expected)
	}

	sessionFile := ws.SessionFile("user-1", "conv-abc")
	expectedFile := filepath.Join(expected, "conv-abc.jsonl")
	if sessionFile != expectedFile {
		t.Errorf("SessionFile = %q, want %q", sessionFile, expectedFile)
	}
}

func TestCleanSandbox(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	// Create some sandbox entries.
	sbDir := ws.SandboxDir()
	os.MkdirAll(filepath.Join(sbDir, "exec-1"), 0750)
	os.MkdirAll(filepath.Join(sbDir, "exec-2"), 0750)
	os.WriteFile(filepath.Join(sbDir, "exec-1", "output.txt"), []byte("hello"), 0644)

	if err := ws.CleanSandbox(); err != nil {
		t.Fatalf("CleanSandbox: %v", err)
	}

	entries, _ := os.ReadDir(sbDir)
	if len(entries) != 0 {
		t.Errorf("sandbox dir not empty after clean: %d entries", len(entries))
	}
}

func TestCleanSandboxNoop(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	// Don't create sandbox dir — CleanSandbox should be a no-op.
	os.RemoveAll(filepath.Join(ws.Root, "sandbox"))
	if err := ws.CleanSandbox(); err != nil {
		t.Fatalf("CleanSandbox on missing dir: %v", err)
	}
}

func TestEnsureAll(t *testing.T) {
	tmp := t.TempDir()
	ws, err := New(filepath.Join(tmp, "ws"))
	if err != nil {
		t.Fatal(err)
	}

	if err := ws.EnsureAll(); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"skills", "sessions", "agents", "sandbox", "mcp", "credentials", "logs"} {
		p := filepath.Join(ws.Root, sub)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("directory %q not created: %v", sub, err)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"normal", "normal"},
		{"a/b", "a_b"},
		{"a\\b", "a_b"},
		{"../etc/passwd", "__etc_passwd"},
		{"", "_"},
	}
	for _, tc := range tests {
		got := sanitizeName(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got, err := resolvePath("~/test")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "test")
	if got != want {
		t.Errorf("resolvePath(~/test) = %q, want %q", got, want)
	}
}
