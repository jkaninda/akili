// Package llm defines the provider-agnostic interface for LLM interactions.
package llm

import "context"

// Provider is the abstraction over any LLM backend (Anthropic, OpenAI, etc.).
type Provider interface {
	// SendMessage sends a conversation to the LLM and returns its response.
	SendMessage(ctx context.Context, req *Request) (*Response, error)
	// Name returns the provider identifier (e.g. "anthropic").
	Name() string
}

// Request represents a full conversation sent to the LLM.
type Request struct {
	SystemPrompt string
	Messages     []Message
	MaxTokens    int
	Tools        []ToolDefinition // nil = no tool use
}

// ToolDefinition describes a tool the LLM can invoke.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Message is a single turn in the conversation.
// Either Content (plain text) or ContentBlocks (structured) should be set, not both.
type Message struct {
	Role          Role
	Content       string         // Plain text (backward compat). Empty when ContentBlocks is used.
	ContentBlocks []ContentBlock // Structured content. Nil when Content is used.
}

// TextContent returns the concatenated text from all text blocks,
// or the plain Content field if no blocks are present.
func (m *Message) TextContent() string {
	if len(m.ContentBlocks) == 0 {
		return m.Content
	}
	var s string
	for _, b := range m.ContentBlocks {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

// ContentBlock is a tagged union representing a piece of message content.
// The Type field determines which other fields are meaningful.
type ContentBlock struct {
	Type string `json:"type"` // "text", "tool_use", "tool_result"

	// text block fields
	Text string `json:"text,omitempty"`

	// tool_use block fields
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// tool_result block fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// TextBlock creates a text content block.
func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// ToolUseBlock creates a tool_use content block.
func ToolUseBlock(id, name string, input map[string]any) ContentBlock {
	return ContentBlock{Type: "tool_use", ID: id, Name: name, Input: input}
}

// ToolResultBlock creates a tool_result content block.
func ToolResultBlock(toolUseID, content string, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: toolUseID, Text: content, IsError: isError}
}

// Role identifies who sent a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Response is what the LLM returns.
type Response struct {
	Content       string         // Concatenated text content (backward compat).
	ContentBlocks []ContentBlock // Full structured response including tool_use blocks.
	Usage         Usage
	StopReason    string // "end_turn", "tool_use", "max_tokens"
}

// HasToolUse returns true if the LLM is requesting tool execution.
func (r *Response) HasToolUse() bool {
	return r.StopReason == "tool_use"
}

// ToolUseBlocks returns only the tool_use content blocks from the response.
func (r *Response) ToolUseBlocks() []ContentBlock {
	var blocks []ContentBlock
	for _, b := range r.ContentBlocks {
		if b.Type == "tool_use" {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// Usage tracks token consumption for cost accounting.
type Usage struct {
	InputTokens  int
	OutputTokens int
}
