// Package tools defines the tool interface and registry for Akili.
// Each tool declares its required security action so the agent can enforce
// RBAC and approval checks before execution.
package tools

import (
	"context"
	"sync"

	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/security"
)

// Tool is the interface all Akili tools must implement.
type Tool interface {
	// Name returns the tool's unique identifier (e.g. "shell_exec").
	Name() string

	// Description returns a human-readable description.
	Description() string

	// InputSchema returns a JSON Schema object describing the tool's parameters.
	// This is sent to the LLM as the tool's input_schema for function calling.
	InputSchema() map[string]any

	// RequiredAction returns the security action this tool needs.
	// The agent uses this to perform RBAC and approval checks before execution.
	RequiredAction() security.Action

	// EstimateCost returns an estimated cost in USD for the given parameters.
	// Used by the budget manager for pre-execution checks.
	EstimateCost(params map[string]any) float64

	// Validate checks that params are well-formed before any security checks run.
	// This is called by the orchestrator before permission/approval/budget checks
	// so invalid requests fail fast without consuming security resources.
	Validate(params map[string]any) error

	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params map[string]any) (*Result, error)
}

// Result is the outcome of a tool execution.
type Result struct {
	Output   string         `json:"output"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Success  bool           `json:"success"`
}

// MaxOutputBytes is the default cap for tool output to prevent OOM.
const MaxOutputBytes = 1 << 20 // 1 MB

// contextKey is an unexported type for context keys defined in this package.
type contextKey int

const userIDKey contextKey = iota

// ContextWithUserID returns a new context carrying the user ID.
// Used by the orchestrator to pass user identity to tool Execute methods.
func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext extracts the user ID from context, or "" if not set.
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		return v
	}
	return ""
}

// TruncateOutput caps a string at maxBytes, appending a truncation notice if cut.
func TruncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const suffix = "\n... [output truncated]"
	if maxBytes <= len(suffix) {
		return s[:maxBytes]
	}
	return s[:maxBytes-len(suffix)] + suffix
}

// Registry holds available tools keyed by name.
// Thread-safe for concurrent reads; writes should only happen at startup.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool. Panics on duplicate names (startup config error, not runtime).
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		panic("duplicate tool registration: " + t.Name())
	}
	r.tools[t.Name()] = t
}

// Get returns the tool by name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// All returns all registered tools.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToLLMDefinitions converts all registered tools into LLM tool definitions.
func ToLLMDefinitions(reg *Registry) []llm.ToolDefinition {
	all := reg.All()
	defs := make([]llm.ToolDefinition, len(all))
	for i, t := range all {
		defs[i] = llm.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		}
	}
	return defs
}
