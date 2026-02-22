// Package mcp provides an MCP (Model Context Protocol) client bridge that
// discovers tools from external MCP servers and adapts them into Akili's
// tools.Tool interface. All MCP tools flow through the same RBAC, approval,
// audit, and budget pipeline as native tools.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// --- MCPTool: adapts a single MCP tool into tools.Tool ---

// MCPTool wraps a tool discovered from an MCP server.
type MCPTool struct {
	namespacedName string              // "mcp__<server>__<tool>" — unique across all servers.
	description    string              // From MCP server, prefixed with [MCP:<server>].
	inputSchema    map[string]any      // JSON Schema from the MCP tool definition.
	action         security.Action     // Server-level permission + risk level.
	client         mcpclient.MCPClient // MCP client connection.
	originalName   string              // Tool name as the MCP server knows it.
	serverName     string              // Server name for metadata.
	logger         *slog.Logger
}

func (t *MCPTool) Name() string                          { return t.namespacedName }
func (t *MCPTool) Description() string                   { return t.description }
func (t *MCPTool) InputSchema() map[string]any           { return t.inputSchema }
func (t *MCPTool) RequiredAction() security.Action       { return t.action }
func (t *MCPTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *MCPTool) Validate(params map[string]any) error {
	required, _ := t.inputSchema["required"].([]any)
	for _, r := range required {
		key, ok := r.(string)
		if !ok {
			continue
		}
		if _, exists := params[key]; !exists {
			return fmt.Errorf("missing required parameter: %s", key)
		}
	}
	return nil
}

func (t *MCPTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	t.logger.InfoContext(ctx, "mcp tool executing",
		slog.String("server", t.serverName),
		slog.String("tool", t.originalName),
	)

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = t.originalName
	callReq.Params.Arguments = params

	callResult, err := t.client.CallTool(ctx, callReq)
	if err != nil {
		return nil, fmt.Errorf("MCP call to %s/%s failed: %w", t.serverName, t.originalName, err)
	}

	output := formatMCPContent(callResult.Content)

	return &tools.Result{
		Output:  tools.TruncateOutput(output, tools.MaxOutputBytes),
		Success: !callResult.IsError,
		Metadata: map[string]any{
			"mcp_server":    t.serverName,
			"mcp_tool":      t.originalName,
			"content_items": len(callResult.Content),
		},
	}, nil
}

// formatMCPContent converts MCP content items to a single string.
func formatMCPContent(content []mcp.Content) string {
	var sb strings.Builder
	for i, c := range content {
		if i > 0 {
			sb.WriteString("\n")
		}
		if tc, ok := mcp.AsTextContent(c); ok {
			sb.WriteString(tc.Text)
		} else {
			// For non-text content (image, audio, resource), serialize as JSON.
			data, _ := json.Marshal(c)
			sb.WriteString(string(data))
		}
	}
	return sb.String()
}

// --- MCPBridge: manages MCP client lifecycle ---

// Bridge manages the lifecycle of MCP client connections and produces
// MCPTool instances for the tool registry.
type Bridge struct {
	clients []mcpclient.MCPClient
	logger  *slog.Logger
}

// NewBridge creates a bridge that will manage MCP server connections.
func NewBridge(logger *slog.Logger) *Bridge {
	return &Bridge{logger: logger}
}

// ConnectAndDiscover connects to one MCP server, performs the initialization
// handshake, discovers tools, and returns MCPTool adapters ready for registration.
func (b *Bridge) ConnectAndDiscover(ctx context.Context, cfg config.MCPServerConfig) ([]*MCPTool, error) {
	c, err := b.createClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating MCP client for %q: %w", cfg.Name, err)
	}

	// Initialize handshake.
	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "akili",
		Version: "0.0.1",
	}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	if _, err := c.Initialize(ctx, initReq); err != nil {
		return nil, fmt.Errorf("MCP initialize for %q: %w", cfg.Name, err)
	}

	b.clients = append(b.clients, c)

	// Discover tools.
	listResp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("MCP list tools for %q: %w", cfg.Name, err)
	}

	// Resolve security settings.
	permission := cfg.Permission
	if permission == "" {
		permission = "mcp__" + cfg.Name
	}
	riskLevel := security.RiskMedium
	if cfg.RiskLevel != "" {
		riskLevel = security.ParseRiskLevel(cfg.RiskLevel)
	}
	action := security.Action{
		Name:      permission,
		RiskLevel: riskLevel,
	}

	// Create MCPTool adapters.
	mcpTools := make([]*MCPTool, 0, len(listResp.Tools))
	for _, t := range listResp.Tools {
		namespacedName := fmt.Sprintf("mcp__%s__%s", cfg.Name, t.Name)
		schema := convertInputSchema(t.InputSchema)

		mcpTools = append(mcpTools, &MCPTool{
			namespacedName: namespacedName,
			description:    fmt.Sprintf("[MCP:%s] %s", cfg.Name, t.Description),
			inputSchema:    schema,
			action:         action,
			client:         c,
			originalName:   t.Name,
			serverName:     cfg.Name,
			logger:         b.logger,
		})
	}

	b.logger.Info("MCP server connected",
		slog.String("server", cfg.Name),
		slog.String("transport", cfg.Transport),
		slog.Int("tools_discovered", len(mcpTools)),
		slog.String("permission", permission),
		slog.String("risk_level", riskLevel.String()),
	)

	return mcpTools, nil
}

// Close shuts down all MCP client connections.
func (b *Bridge) Close() {
	for _, c := range b.clients {
		if err := c.Close(); err != nil {
			b.logger.Error("closing MCP client", slog.String("error", err.Error()))
		}
	}
}

// createClient creates the appropriate MCP client based on transport type.
func (b *Bridge) createClient(cfg config.MCPServerConfig) (*mcpclient.Client, error) {
	switch cfg.Transport {
	case "stdio":
		env := expandEnvMap(cfg.Env)
		return mcpclient.NewStdioMCPClient(cfg.Command, env, cfg.Args...)

	case "sse":
		var opts []transport.ClientOption
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(expandEnvToMap(cfg.Headers)))
		}
		return mcpclient.NewSSEMCPClient(cfg.URL, opts...)

	case "streamable_http":
		var opts []transport.StreamableHTTPCOption
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(expandEnvToMap(cfg.Headers)))
		}
		return mcpclient.NewStreamableHttpClient(cfg.URL, opts...)

	default:
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}
}

// convertInputSchema converts the MCP ToolInputSchema to the map[string]any
// format that Akili tools use.
func convertInputSchema(schema mcp.ToolInputSchema) map[string]any {
	result := map[string]any{
		"type": schema.Type,
	}
	if schema.Properties != nil {
		result["properties"] = schema.Properties
	}
	if len(schema.Required) > 0 {
		reqAny := make([]any, len(schema.Required))
		for i, r := range schema.Required {
			reqAny[i] = r
		}
		result["required"] = reqAny
	}
	return result
}

// expandEnvMap converts a map of key→value to a []string of "KEY=expanded_value".
func expandEnvMap(m map[string]string) []string {
	env := make([]string, 0, len(m))
	for k, v := range m {
		env = append(env, k+"="+os.ExpandEnv(v))
	}
	return env
}

// expandEnvToMap returns a new map with values expanded via os.ExpandEnv.
func expandEnvToMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = os.ExpandEnv(v)
	}
	return out
}
