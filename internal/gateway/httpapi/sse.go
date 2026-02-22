package httpapi

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/okapi"
)

// SSEEvent represents a server-sent event for streaming responses.
type SSEEvent struct {
	Type    string `json:"type"`              // "text", "tool_start", "tool_result", "done", "error"
	Content string `json:"content,omitempty"` // Text content.
	Tool    string `json:"tool,omitempty"`    // Tool name for tool events.
}

// handleQueryStream handles POST /v1/query/stream with SSE responses.
// Runs the agent and streams the result as server-sent events.
func (g *Gateway) handleQueryStream(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	var req QueryRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("Bad request", err)

	}
	if req.Message == "" {
		return c.AbortBadRequest("message is required")
	}

	correlationID := newCorrelationID()
	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = uuid.New().String()
	}

	// Process through agent (buffered — full result streamed as events).
	resp, err := g.agent.Process(c.Context(), &agent.Input{
		UserID:         userID,
		Message:        req.Message,
		CorrelationID:  correlationID,
		ConversationID: conversationID,
	})

	if err != nil {
		var approvalErr *agent.ErrApprovalPending
		if errors.As(err, &approvalErr) {
			c.SSEvent("error", SSEEvent{Content: fmt.Sprintf("Approval required (id: %s)", approvalErr.ApprovalID)})
			return nil
		}
		c.SSEvent("error", SSEEvent{Content: "processing failed"})
		return nil
	}

	// Stream tool results first, then the final text.
	for _, tr := range resp.ToolResults {
		c.SSEvent("tool_result", SSEEvent{Tool: tr.ToolName})
	}

	if resp.Message != "" {
		c.SSEvent("text", SSEEvent{Content: resp.Message})
	}
	c.SSEvent("done", SSEEvent{Type: "done"})
	return nil
}
