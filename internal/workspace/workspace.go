// Package workspace manages the Akili runtime directory structure.
// All runtime state (database, audit logs, sessions, sandbox dirs, etc.)
// is consolidated under a single workspace root, making Akili portable.
//
// Default workspace: ~/.akili/workspace (configurable via config or AKILI_WORKSPACE env var).
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Default workspace location relative to user home directory.
const defaultRelativePath = ".akili/workspace"

// Workspace manages all Akili runtime directories and derived paths.
type Workspace struct {
	Root string

	mu      sync.Mutex
	created map[string]bool // tracks which directories have been ensured
}

// New creates a Workspace rooted at the given path.
// It resolves ~ to the user's home directory and creates the root directory
// with appropriate permissions if it does not exist.
func New(root string) (*Workspace, error) {
	resolved, err := resolvePath(root)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace root %q: %w", root, err)
	}

	w := &Workspace{
		Root:    resolved,
		created: make(map[string]bool),
	}

	if err := w.ensureDir(resolved, 0750); err != nil {
		return nil, fmt.Errorf("creating workspace root: %w", err)
	}

	return w, nil
}

// Default creates a Workspace at ~/.akili/workspace.
func Default() (*Workspace, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("determining home directory: %w", err)
	}
	return New(filepath.Join(home, defaultRelativePath))
}

// --- Top-level directory accessors ---

// SkillsDir returns <root>/skills/. Stores skill definition Markdown files.
func (w *Workspace) SkillsDir() string {
	return w.dir("skills")
}

// SessionsDir returns <root>/sessions/. Stores per-user session transcripts.
func (w *Workspace) SessionsDir() string {
	return w.dir("sessions")
}

// AgentsDir returns <root>/agents/. Stores per-agent runtime state.
func (w *Workspace) AgentsDir() string {
	return w.dir("agents")
}

// SandboxDir returns <root>/sandbox/. Ephemeral sandbox working directories.
func (w *Workspace) SandboxDir() string {
	return w.dir("sandbox")
}

// MCPDir returns <root>/mcp/. MCP server state and logs.
func (w *Workspace) MCPDir() string {
	return w.dir("mcp")
}

// CredentialsDir returns <root>/credentials/ with 0700 permissions.
func (w *Workspace) CredentialsDir() string {
	return w.restrictedDir("credentials")
}

// LogsDir returns <root>/logs/. Application log files.
func (w *Workspace) LogsDir() string {
	return w.dir("logs")
}

// --- Derived paths ---

// ConfigPath returns <root>/config.json.
func (w *Workspace) ConfigPath() string {
	return filepath.Join(w.Root, "config.json")
}

// --- Agent-scoped paths ---

// AgentDir returns <root>/agents/<agentID>/.
func (w *Workspace) AgentDir(agentID string) string {
	p := filepath.Join(w.AgentsDir(), sanitizeName(agentID))
	_ = w.ensureDir(p, 0750)
	return p
}

// AgentAuditPath returns <root>/agents/<agentID>/audit.jsonl.
func (w *Workspace) AgentAuditPath(agentID string) string {
	return filepath.Join(w.AgentDir(agentID), "audit.jsonl")
}

// --- Session paths ---

// SessionDir returns <root>/sessions/<userID>/.
func (w *Workspace) SessionDir(userID string) string {
	p := filepath.Join(w.SessionsDir(), sanitizeName(userID))
	_ = w.ensureDir(p, 0750)
	return p
}

// SessionFile returns <root>/sessions/<userID>/<conversationID>.jsonl.
func (w *Workspace) SessionFile(userID, conversationID string) string {
	return filepath.Join(w.SessionDir(userID), sanitizeName(conversationID)+".jsonl")
}

// --- Cleanup ---

// CleanSandbox removes all contents of the sandbox directory.
func (w *Workspace) CleanSandbox() error {
	dir := filepath.Join(w.Root, "sandbox")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading sandbox dir: %w", err)
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("removing sandbox entry %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// EnsureAll creates all standard workspace directories.
// Call this during onboarding or first startup.
func (w *Workspace) EnsureAll() error {
	// Regular directories (0750).
	dirs := []string{
		w.SkillsDir(),
		w.SessionsDir(),
		w.AgentsDir(),
		w.SandboxDir(),
		w.MCPDir(),
		w.LogsDir(),
	}
	for _, d := range dirs {
		if err := w.ensureDir(d, 0750); err != nil {
			return err
		}
	}
	// Restricted directories (0700).
	_ = w.CredentialsDir()
	return nil
}

// --- Internal helpers ---

// dir returns an absolute path under the workspace root and ensures the directory exists.
func (w *Workspace) dir(name string) string {
	p := filepath.Join(w.Root, name)
	_ = w.ensureDir(p, 0750)
	return p
}

// restrictedDir is like dir but uses 0700 permissions.
func (w *Workspace) restrictedDir(name string) string {
	p := filepath.Join(w.Root, name)
	_ = w.ensureDir(p, 0700)
	return p
}

// ensureDir creates a directory if it doesn't already exist.
// Uses a cache to avoid redundant stat/mkdir calls.
func (w *Workspace) ensureDir(path string, perm os.FileMode) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.created[path] {
		return nil
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("creating directory %s: %w", path, err)
	}
	w.created[path] = true
	return nil
}

// resolvePath expands ~ to the user home directory and returns an absolute path.
func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	return filepath.Abs(path)
}

// sanitizeName replaces path separator characters to prevent directory traversal.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		name = "_"
	}
	return name
}
