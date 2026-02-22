package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/jkaninda/akili/internal/agent"
)

// roleAgentWrapper adapts an agent.Orchestrator to the RoleAgent interface.
// Each wrapper holds its own orchestrator instance with role-specific
// system prompt and tool registry, preserving conversation history isolation.
type roleAgentWrapper struct {
	role  AgentRole
	inner *agent.Orchestrator
}

func (r *roleAgentWrapper) Role() AgentRole {
	return r.role
}

func (r *roleAgentWrapper) Process(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	// Build the instruction with context from prior agent messages.
	prompt := r.buildPrompt(input)

	agentInput := &agent.Input{
		UserID:        input.UserID,
		Message:       prompt,
		CorrelationID: input.CorrelationID,
	}

	resp, err := r.inner.Process(ctx, agentInput)
	if err != nil {
		return nil, fmt.Errorf("agent %s process: %w", r.role, err)
	}

	return &AgentOutput{
		Response:   resp.Message,
		TokensUsed: resp.TokensUsed,
	}, nil
}

// buildPrompt combines the task instruction with relevant context from
// prior agent messages in the workflow.
func (r *roleAgentWrapper) buildPrompt(input *AgentInput) string {
	var b strings.Builder

	// Include relevant prior messages as context.
	if len(input.Messages) > 0 {
		b.WriteString("## Context from workflow\n\n")
		for _, msg := range input.Messages {
			fmt.Fprintf(&b, "[%s → %s] (%s): %s\n\n",
				msg.FromRole, msg.ToRole, msg.MessageType, msg.Content)
		}
		b.WriteString("---\n\n")
	}

	b.WriteString("## Task\n\n")
	b.WriteString(input.Instruction)

	return b.String()
}

// Compile-time check.
var _ RoleAgent = (*roleAgentWrapper)(nil)
