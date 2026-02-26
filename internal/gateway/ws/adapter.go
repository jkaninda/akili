package ws

import (
	"context"
	"time"

	"github.com/jkaninda/akili/internal/agent"
)

// AgentRouterAdapter adapts a ws.Server to the agent.WSTaskExecutor interface.
// This breaks the import cycle: agent → ws would create a cycle, so instead
// cmd/akili wires the adapter which satisfies the interface.
type AgentRouterAdapter struct {
	server *Server
}

// NewAgentRouterAdapter creates an adapter that satisfies agent.WSTaskExecutor.
func NewAgentRouterAdapter(server *Server) *AgentRouterAdapter {
	return &AgentRouterAdapter{server: server}
}

// ExecuteSync delegates to the WebSocket server's synchronous task execution.
func (a *AgentRouterAdapter) ExecuteSync(ctx context.Context, userID, message string, budgetUSD float64, timeout time.Duration) (agent.WSTaskResult, error) {
	result, err := a.server.ExecuteSync(ctx, userID, message, budgetUSD, timeout)
	if err != nil {
		return agent.WSTaskResult{}, err
	}
	return agent.WSTaskResult{
		Success:    result.Success,
		Output:     result.Output,
		TokensUsed: result.TokensUsed,
		Error:      result.Error,
	}, nil
}

// ConnectedAgentCount returns the number of connected remote agents.
func (a *AgentRouterAdapter) ConnectedAgentCount() int {
	return a.server.ConnectedAgentCount()
}
