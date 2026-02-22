package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	infrastore "github.com/jkaninda/akili/internal/infra"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// ManageTool handles CRUD operations on infrastructure nodes.
// RiskHigh because it modifies the infrastructure registry.
type ManageTool struct {
	store  infrastore.Store
	orgID  uuid.UUID
	logger *slog.Logger
}

// NewManageTool creates an infrastructure management tool.
func NewManageTool(store infrastore.Store, orgID uuid.UUID, logger *slog.Logger) *ManageTool {
	return &ManageTool{store: store, orgID: orgID, logger: logger}
}

func (t *ManageTool) Name() string { return "infra_manage" }
func (t *ManageTool) Description() string {
	return "Manage infrastructure nodes: add, update, or remove registered nodes. " +
		"When adding a node, provide a credential_ref that references where the secret is stored " +
		"(e.g., 'env://VM10_SSH_KEY'). The actual secret is never stored in the node registry."
}
func (t *ManageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "update", "remove"},
				"description": "Operation to perform",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Node canonical name (required for add)",
			},
			"aliases": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Alternative names for the node",
			},
			"node_type": map[string]any{
				"type":        "string",
				"enum":        []string{"vm", "kubernetes", "docker_host", "bare_metal"},
				"description": "Infrastructure type",
			},
			"host": map[string]any{
				"type":        "string",
				"description": "Hostname or IP address",
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "Connection port (0 = default for type)",
			},
			"user": map[string]any{
				"type":        "string",
				"description": "Default connection user",
			},
			"credential_ref": map[string]any{
				"type":        "string",
				"description": "Opaque reference to credentials (e.g., env://KEY_NAME)",
			},
			"tags": map[string]any{
				"type":        "object",
				"description": "Arbitrary metadata tags",
			},
			"mcp_server": map[string]any{
				"type":        "string",
				"description": "MCP server name that handles this node type",
			},
			"node_id": map[string]any{
				"type":        "string",
				"description": "Node UUID (required for update/remove)",
			},
		},
		"required": []string{"operation"},
	}
}
func (t *ManageTool) RequiredAction() security.Action {
	return security.Action{Name: "infra_manage", RiskLevel: security.RiskHigh}
}
func (t *ManageTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *ManageTool) Validate(params map[string]any) error {
	op, _ := params["operation"].(string)
	switch op {
	case "add":
		if name, _ := params["name"].(string); name == "" {
			return fmt.Errorf("name is required for add operation")
		}
		if host, _ := params["host"].(string); host == "" {
			return fmt.Errorf("host is required for add operation")
		}
		if nt, _ := params["node_type"].(string); nt == "" {
			return fmt.Errorf("node_type is required for add operation")
		}
	case "update", "remove":
		if id, _ := params["node_id"].(string); id == "" {
			return fmt.Errorf("node_id is required for %s operation", op)
		}
	default:
		return fmt.Errorf("operation must be 'add', 'update', or 'remove', got %q", op)
	}
	return nil
}

func (t *ManageTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	op, _ := params["operation"].(string)

	switch op {
	case "add":
		return t.executeAdd(ctx, params)
	case "update":
		return t.executeUpdate(ctx, params)
	case "remove":
		return t.executeRemove(ctx, params)
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func (t *ManageTool) executeAdd(ctx context.Context, params map[string]any) (*tools.Result, error) {
	node := &domain.InfraNode{
		ID:        uuid.New(),
		OrgID:     t.orgID,
		Name:      params["name"].(string),
		NodeType:  params["node_type"].(string),
		Host:      params["host"].(string),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if aliases, ok := params["aliases"].([]any); ok {
		for _, a := range aliases {
			if s, ok := a.(string); ok {
				node.Aliases = append(node.Aliases, s)
			}
		}
	}
	if port, ok := params["port"].(float64); ok {
		node.Port = int(port)
	}
	if user, ok := params["user"].(string); ok {
		node.User = user
	}
	if ref, ok := params["credential_ref"].(string); ok {
		node.CredentialRef = ref
	}
	if tags, ok := params["tags"].(map[string]any); ok {
		node.Tags = make(map[string]string, len(tags))
		for k, v := range tags {
			if s, ok := v.(string); ok {
				node.Tags[k] = s
			}
		}
	}
	if mcp, ok := params["mcp_server"].(string); ok {
		node.MCPServer = mcp
	}

	if err := t.store.Create(ctx, node); err != nil {
		return nil, fmt.Errorf("creating node: %w", err)
	}

	t.logger.InfoContext(ctx, "infra_manage: node created",
		slog.String("node_name", node.Name),
		slog.String("node_id", node.ID.String()),
	)

	sanitized := infrastore.SanitizeForLLM(node)
	output, _ := json.MarshalIndent(sanitized, "", "  ")
	return &tools.Result{
		Output:  fmt.Sprintf("Node %q created successfully.\n%s", node.Name, string(output)),
		Success: true,
		Metadata: map[string]any{
			"node_id": node.ID.String(),
		},
	}, nil
}

func (t *ManageTool) executeUpdate(ctx context.Context, params map[string]any) (*tools.Result, error) {
	nodeIDStr, _ := params["node_id"].(string)
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid node_id: %w", err)
	}

	// Load current node.
	node, err := t.store.Lookup(ctx, t.orgID, nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("node not found: %w", err)
	}

	// Apply updates.
	if name, ok := params["name"].(string); ok && name != "" {
		node.Name = name
	}
	if nt, ok := params["node_type"].(string); ok && nt != "" {
		node.NodeType = nt
	}
	if host, ok := params["host"].(string); ok && host != "" {
		node.Host = host
	}
	if port, ok := params["port"].(float64); ok {
		node.Port = int(port)
	}
	if user, ok := params["user"].(string); ok {
		node.User = user
	}
	if ref, ok := params["credential_ref"].(string); ok {
		node.CredentialRef = ref
	}
	if aliases, ok := params["aliases"].([]any); ok {
		node.Aliases = nil
		for _, a := range aliases {
			if s, ok := a.(string); ok {
				node.Aliases = append(node.Aliases, s)
			}
		}
	}
	if tags, ok := params["tags"].(map[string]any); ok {
		node.Tags = make(map[string]string, len(tags))
		for k, v := range tags {
			if s, ok := v.(string); ok {
				node.Tags[k] = s
			}
		}
	}
	if mcp, ok := params["mcp_server"].(string); ok {
		node.MCPServer = mcp
	}

	if err := t.store.Update(ctx, node); err != nil {
		return nil, fmt.Errorf("updating node: %w", err)
	}

	t.logger.InfoContext(ctx, "infra_manage: node updated",
		slog.String("node_id", nodeID.String()),
	)

	sanitized := infrastore.SanitizeForLLM(node)
	output, _ := json.MarshalIndent(sanitized, "", "  ")
	return &tools.Result{
		Output:  fmt.Sprintf("Node %q updated successfully.\n%s", node.Name, string(output)),
		Success: true,
	}, nil
}

func (t *ManageTool) executeRemove(ctx context.Context, params map[string]any) (*tools.Result, error) {
	nodeIDStr, _ := params["node_id"].(string)
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid node_id: %w", err)
	}

	if err := t.store.Delete(ctx, t.orgID, nodeID); err != nil {
		return nil, fmt.Errorf("removing node: %w", err)
	}

	t.logger.InfoContext(ctx, "infra_manage: node removed",
		slog.String("node_id", nodeID.String()),
	)

	return &tools.Result{
		Output:  fmt.Sprintf("Node %s removed successfully.", nodeID),
		Success: true,
	}, nil
}
