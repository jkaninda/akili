package llm

import "context"

// StreamEvent represents a single event in a streaming LLM response.
type StreamEvent struct {
	Type    string        // "text", "tool_use_start", "tool_result", "done", "error"
	Content string        // Text content for "text" events.
	ToolUse *ContentBlock // Tool use block for "tool_use_start" events.
	Error   error         // Error for "error" events.
}

// StreamingProvider extends Provider with streaming support.
// Providers that don't support streaming can be wrapped with
// NonStreamingAdapter to provide buffered streaming.
type StreamingProvider interface {
	Provider
	// StreamMessage sends a request and streams events to the channel.
	// The channel is closed when the response is complete or an error occurs.
	StreamMessage(ctx context.Context, req *Request, events chan<- StreamEvent) error
}

// NonStreamingAdapter wraps a regular Provider to implement StreamingProvider
// by buffering the full response and sending it as a single event.
type NonStreamingAdapter struct {
	Provider
}

// StreamMessage calls SendMessage and sends the result as buffered events.
func (a *NonStreamingAdapter) StreamMessage(ctx context.Context, req *Request, events chan<- StreamEvent) error {
	defer close(events)

	resp, err := a.SendMessage(ctx, req)
	if err != nil {
		events <- StreamEvent{Type: "error", Error: err}
		return err
	}

	// Send text content.
	if resp.Content != "" {
		events <- StreamEvent{Type: "text", Content: resp.Content}
	}

	// Send tool use blocks.
	for _, block := range resp.ToolUseBlocks() {
		b := block
		events <- StreamEvent{Type: "tool_use_start", ToolUse: &b}
	}

	events <- StreamEvent{Type: "done"}
	return nil
}
