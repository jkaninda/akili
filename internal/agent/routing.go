package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// WSTaskExecutor is the interface the RoutingAgent needs from the WebSocket server.
// This avoids importing the ws package directly (which would create a cycle).
type WSTaskExecutor interface {
	// ExecuteSync sends a task to an available remote agent and waits for the result.
	ExecuteSync(ctx context.Context, userID, message string, budgetUSD float64, timeout time.Duration) (WSTaskResult, error)
	// ConnectedAgentCount returns the number of currently connected remote agents.
	ConnectedAgentCount() int
}

// WSTaskResult is the result from a synchronous WebSocket task execution.
type WSTaskResult struct {
	Success    bool
	Output     string
	TokensUsed int
	Error      string
}

// RoutingAgent implements the Agent interface by routing queries to remote
// WebSocket agents when available, and falling back to the embedded agent
// based on the EmbeddedAgentConfig.
//
// Routing logic:
//  1. If remote agents are connected → dispatch to remote agent via WebSocket.
//  2. If no remote agents AND embedded is enabled/fallback → use embedded agent.
//  3. If no remote agents AND embedded is disabled → return error.
type RoutingAgent struct {
	embedded        Agent
	ws              WSTaskExecutor
	embeddedEnabled bool    // Whether the embedded agent is enabled.
	embeddedFallback bool   // Whether to fall back to embedded when no remote agents.
	budgetUSD       float64 // Budget for remote task execution.
	timeout         time.Duration
	logger          *slog.Logger
}

// RoutingAgentConfig holds configuration for the RoutingAgent.
type RoutingAgentConfig struct {
	EmbeddedEnabled  bool
	EmbeddedFallback bool
	BudgetUSD        float64
	TaskTimeout      time.Duration
}

// NewRoutingAgent creates a RoutingAgent that routes between remote and embedded agents.
// If ws is nil, all requests go to the embedded agent (when enabled).
func NewRoutingAgent(embedded Agent, ws WSTaskExecutor, cfg RoutingAgentConfig, logger *slog.Logger) *RoutingAgent {
	return &RoutingAgent{
		embedded:         embedded,
		ws:               ws,
		embeddedEnabled:  cfg.EmbeddedEnabled,
		embeddedFallback: cfg.EmbeddedFallback,
		budgetUSD:        cfg.BudgetUSD,
		timeout:          cfg.TaskTimeout,
		logger:           logger,
	}
}

// Process routes user messages to remote agents or the embedded agent.
func (r *RoutingAgent) Process(ctx context.Context, input *Input) (*Response, error) {
	// Try remote agents first.
	if r.ws != nil {
		count := r.ws.ConnectedAgentCount()
		if count > 0 {
			r.logger.Info("routing to remote agent",
				slog.String("user_id", input.UserID),
				slog.Int("available_agents", count),
			)

			timeout := r.timeout
			if timeout == 0 {
				timeout = 5 * time.Minute
			}

			result, err := r.ws.ExecuteSync(ctx, input.UserID, input.Message, r.budgetUSD, timeout)
			if err == nil {
				if !result.Success {
					return nil, fmt.Errorf("remote agent error: %s", result.Error)
				}
				return &Response{
					Message:    result.Output,
					TokensUsed: result.TokensUsed,
				}, nil
			}

			r.logger.Warn("remote agent execution failed",
				slog.String("error", err.Error()),
				slog.String("user_id", input.UserID),
			)

			// Fall through to embedded agent if fallback is enabled.
			if !r.embeddedFallback {
				return nil, fmt.Errorf("remote agent failed and embedded fallback is disabled: %w", err)
			}
			r.logger.Info("falling back to embedded agent",
				slog.String("user_id", input.UserID),
			)
		}
	}

	// No remote agents available — check embedded agent config.
	if !r.embeddedEnabled && !r.embeddedFallback {
		return nil, fmt.Errorf("no agents available: embedded agent is disabled and no remote agents are connected")
	}

	return r.embedded.Process(ctx, input)
}

// ExecuteTool delegates to the embedded agent (tool execution is always local).
func (r *RoutingAgent) ExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	return r.embedded.ExecuteTool(ctx, req)
}

// ResumeWithApproval delegates to the embedded agent (approval state is local).
func (r *RoutingAgent) ResumeWithApproval(ctx context.Context, approvalID string) (*ToolResponse, error) {
	return r.embedded.ResumeWithApproval(ctx, approvalID)
}
