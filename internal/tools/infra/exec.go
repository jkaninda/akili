package infra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	infrastore "github.com/jkaninda/akili/internal/infra"
	"github.com/jkaninda/akili/internal/secrets"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// ExecTool executes commands on remote infrastructure nodes via MCP servers.
//
// Security flow:
//  1. Resolve node from InfrastructureMemory (LLM only provides alias)
//  2. Resolve credentials from SecretProvider (never exposed to LLM)
//  3. Forward enriched request to the node's MCP server
//  4. Sanitize output to prevent credential leakage
//  5. Return sanitized output to LLM
type ExecTool struct {
	store          infrastore.Store
	secretProvider secrets.Provider
	toolRegistry   *tools.Registry
	orgID          uuid.UUID
	logger         *slog.Logger
}

// NewExecTool creates an infrastructure execution tool.
func NewExecTool(
	store infrastore.Store,
	secretProvider secrets.Provider,
	toolRegistry *tools.Registry,
	orgID uuid.UUID,
	logger *slog.Logger,
) *ExecTool {
	return &ExecTool{
		store:          store,
		secretProvider: secretProvider,
		toolRegistry:   toolRegistry,
		orgID:          orgID,
		logger:         logger,
	}
}

func (t *ExecTool) Name() string { return "infra_exec" }
func (t *ExecTool) Description() string {
	return "Execute a command on a remote infrastructure node. " +
		"Specify the node by name or alias (e.g., 'VM10') and the command to run. " +
		"Credentials are resolved automatically — never include passwords or keys in the command. " +
		"For Kubernetes nodes, the command runs via kubectl. For VMs, it runs via SSH."
}
func (t *ExecTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{
				"type":        "string",
				"description": "Node name, alias, or ID to execute on",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Command to execute on the remote node",
			},
			"timeout": map[string]any{
				"type":        "string",
				"description": "Execution timeout (e.g., '30s', '2m'). Default: 30s",
			},
		},
		"required": []string{"node", "command"},
	}
}
func (t *ExecTool) RequiredAction() security.Action {
	return security.Action{Name: "infra_exec", RiskLevel: security.RiskCritical}
}
func (t *ExecTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *ExecTool) Validate(params map[string]any) error {
	if _, err := requireString(params, "node"); err != nil {
		return err
	}
	if _, err := requireString(params, "command"); err != nil {
		return err
	}
	return nil
}

func (t *ExecTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	nodeQuery, _ := requireString(params, "node")
	command, _ := requireString(params, "command")
	timeout, _ := params["timeout"].(string)
	if timeout == "" {
		timeout = "30s"
	}

	// 1. Resolve node from InfrastructureMemory.
	node, err := t.store.Lookup(ctx, t.orgID, nodeQuery)
	if err != nil {
		return nil, fmt.Errorf("resolving node %q: %w", nodeQuery, err)
	}

	t.logger.InfoContext(ctx, "infra_exec: node resolved",
		slog.String("node_name", node.Name),
		slog.String("node_type", node.NodeType),
		slog.String("host", node.Host),
		// NOTE: CredentialRef is intentionally NOT logged.
	)

	// 2. Resolve credentials (NEVER exposed to LLM).
	var credential *secrets.Secret
	if node.CredentialRef != "" {
		credential, err = t.secretProvider.Resolve(ctx, node.CredentialRef)
		if err != nil {
			return nil, fmt.Errorf("resolving credentials for node %q: %w", node.Name, err)
		}
	}

	// 3. Find the MCP tool for this node's server.
	mcpTool := t.findMCPTool(node.MCPServer)
	if mcpTool == nil {
		return nil, fmt.Errorf("MCP server %q has no execute tool registered (expected mcp__%s__execute or mcp__%s__remote_exec)",
			node.MCPServer, node.MCPServer, node.MCPServer)
	}

	// 4. Build enriched MCP request (credentials injected here, NOT visible to LLM).
	mcpParams := map[string]any{
		"host":      node.Host,
		"port":      node.Port,
		"user":      node.User,
		"command":   command,
		"timeout":   timeout,
		"node_type": node.NodeType,
	}
	if credential != nil {
		mcpParams["credential"] = credential.Value
	}

	// 5. Execute via MCP.
	result, err := mcpTool.Execute(ctx, mcpParams)
	if err != nil {
		// Sanitize error message in case it contains credentials.
		errMsg := err.Error()
		if credential != nil {
			errMsg = sanitizeOutput(errMsg, credential.Value)
		}
		return nil, fmt.Errorf("remote execution on %q failed: %s", node.Name, errMsg)
	}

	// 6. Sanitize output (ensure no credential leakage).
	sanitizedOutput := result.Output
	if credential != nil {
		sanitizedOutput = sanitizeOutput(sanitizedOutput, credential.Value)
	}

	return &tools.Result{
		Output:  tools.TruncateOutput(sanitizedOutput, tools.MaxOutputBytes),
		Success: result.Success,
		Metadata: map[string]any{
			"node_name":  node.Name,
			"node_type":  node.NodeType,
			"host":       node.Host,
			"mcp_server": node.MCPServer,
		},
	}, nil
}

// findMCPTool locates the execute tool for the given MCP server name.
func (t *ExecTool) findMCPTool(mcpServer string) tools.Tool {
	// Try common naming conventions.
	candidates := []string{
		fmt.Sprintf("mcp__%s__execute", mcpServer),
		fmt.Sprintf("mcp__%s__remote_exec", mcpServer),
		fmt.Sprintf("mcp__%s__run", mcpServer),
	}
	for _, name := range candidates {
		if tool := t.toolRegistry.Get(name); tool != nil {
			return tool
		}
	}
	return nil
}

// sanitizeOutput replaces any occurrence of the secret value in output with [REDACTED].
func sanitizeOutput(output, secret string) string {
	if secret == "" {
		return output
	}
	return strings.ReplaceAll(output, secret, "[REDACTED]")
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
