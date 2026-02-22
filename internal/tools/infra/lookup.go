// Package infra implements infrastructure management tools for Akili.
// These tools enable the LLM to query infrastructure metadata and execute
// remote commands without ever seeing raw credentials.
package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	infrastore "github.com/jkaninda/akili/internal/infra"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// LookupTool resolves infrastructure node queries (by name, alias, or ID).
// Returns sanitized metadata — CredentialRef is NEVER included in output.
type LookupTool struct {
	store  infrastore.Store
	orgID  uuid.UUID
	logger *slog.Logger
}

// NewLookupTool creates an infrastructure lookup tool.
func NewLookupTool(store infrastore.Store, orgID uuid.UUID, logger *slog.Logger) *LookupTool {
	return &LookupTool{store: store, orgID: orgID, logger: logger}
}

func (t *LookupTool) Name() string { return "infra_lookup" }
func (t *LookupTool) Description() string {
	return "Look up infrastructure nodes by name, alias, or ID. " +
		"Returns connection metadata (host, type, tags) without credentials. " +
		"Use 'list' operation to see all registered nodes."
}
func (t *LookupTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Node name, alias, or UUID to look up",
			},
			"operation": map[string]any{
				"type":        "string",
				"enum":        []string{"lookup", "list"},
				"description": "Operation: 'lookup' for single node, 'list' for all nodes. Default: lookup",
			},
			"node_type": map[string]any{
				"type":        "string",
				"description": "Filter by node type when listing (vm, kubernetes, docker_host, bare_metal)",
			},
		},
		"required": []string{},
	}
}
func (t *LookupTool) RequiredAction() security.Action {
	return security.Action{Name: "infra_lookup", RiskLevel: security.RiskLow}
}
func (t *LookupTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *LookupTool) Validate(params map[string]any) error {
	op := "lookup"
	if v, ok := params["operation"].(string); ok && v != "" {
		op = v
	}
	if op == "lookup" {
		q, _ := params["query"].(string)
		if q == "" {
			return fmt.Errorf("query is required for lookup operation")
		}
	}
	if op != "lookup" && op != "list" {
		return fmt.Errorf("operation must be 'lookup' or 'list', got %q", op)
	}
	return nil
}

func (t *LookupTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	op := "lookup"
	if v, ok := params["operation"].(string); ok && v != "" {
		op = v
	}

	switch op {
	case "list":
		nodeType, _ := params["node_type"].(string)
		nodes, err := t.store.List(ctx, t.orgID, nodeType)
		if err != nil {
			return nil, fmt.Errorf("listing nodes: %w", err)
		}
		sanitized := make([]map[string]any, len(nodes))
		for i := range nodes {
			sanitized[i] = infrastore.SanitizeForLLM(&nodes[i])
		}
		output, _ := json.MarshalIndent(sanitized, "", "  ")
		return &tools.Result{
			Output:  string(output),
			Success: true,
			Metadata: map[string]any{
				"count": len(nodes),
			},
		}, nil

	default: // lookup
		query, _ := params["query"].(string)
		node, err := t.store.Lookup(ctx, t.orgID, query)
		if err != nil {
			return nil, fmt.Errorf("node lookup: %w", err)
		}
		t.logger.InfoContext(ctx, "infra_lookup: node resolved",
			slog.String("node_name", node.Name),
			slog.String("node_type", node.NodeType),
		)
		sanitized := infrastore.SanitizeForLLM(node)
		output, _ := json.MarshalIndent(sanitized, "", "  ")
		return &tools.Result{
			Output:  string(output),
			Success: true,
			Metadata: map[string]any{
				"node_id": node.ID.String(),
			},
		}, nil
	}
}
