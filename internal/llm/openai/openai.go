// Package openai implements the LLM provider interface for the OpenAI Chat Completions API.
// It also serves as the Ollama provider since Ollama exposes an OpenAI-compatible API.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/jkaninda/akili/internal/llm"
)

const (
	defaultBaseURL   = "https://api.openai.com"
	completionsPath  = "/v1/chat/completions"
	defaultMaxTokens = 4096
)

// Client implements llm.Provider using the OpenAI Chat Completions API.
type Client struct {
	apiKey     string
	model      string
	baseURL    string
	name       string
	httpClient *http.Client
	logger     *slog.Logger
}

// Option configures the OpenAI client.
type Option func(*Client)

// WithBaseURL overrides the API base URL.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithName overrides the provider name (e.g. "ollama").
func WithName(name string) Option {
	return func(c *Client) { c.name = name }
}

// NewClient creates an OpenAI-compatible provider.
// For Ollama, use WithBaseURL("http://localhost:11434") and WithName("ollama").
func NewClient(apiKey, model string, logger *slog.Logger, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultBaseURL,
		name:       "openai",
		httpClient: http.DefaultClient,
		logger:     logger,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return c.name }

// SendMessage sends the conversation to the OpenAI Chat Completions API.
func (c *Client) SendMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	apiReq := c.buildRequest(req)

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+completionsPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", httpResp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	resp := c.toResponse(&apiResp)

	c.logger.DebugContext(ctx, "llm request completed",
		slog.String("provider", c.name),
		slog.String("model", c.model),
		slog.Int("input_tokens", resp.Usage.InputTokens),
		slog.Int("output_tokens", resp.Usage.OutputTokens),
		slog.String("stop_reason", resp.StopReason),
	)

	return resp, nil
}

func (c *Client) buildRequest(req *llm.Request) apiRequest {
	var messages []apiMessage

	// System prompt becomes a system message.
	if req.SystemPrompt != "" {
		messages = append(messages, apiMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		if len(m.ContentBlocks) > 0 {
			messages = append(messages, convertStructuredMessage(m)...)
		} else {
			messages = append(messages, apiMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	apiReq := apiRequest{
		Model:     c.model,
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	for _, t := range req.Tools {
		apiReq.Tools = append(apiReq.Tools, apiTool{
			Type: "function",
			Function: apiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return apiReq
}

// convertStructuredMessage converts an llm.Message with ContentBlocks into OpenAI API messages.
// Assistant messages with tool_use blocks become a single message with tool_calls.
// User messages with tool_result blocks become separate "tool" role messages.
func convertStructuredMessage(m llm.Message) []apiMessage {
	if m.Role == llm.RoleAssistant {
		// Collect text and tool calls from content blocks.
		var text string
		var toolCalls []apiToolCall
		for _, b := range m.ContentBlocks {
			switch b.Type {
			case "text":
				text += b.Text
			case "tool_use":
				inputJSON, _ := json.Marshal(b.Input)
				toolCalls = append(toolCalls, apiToolCall{
					ID:   b.ID,
					Type: "function",
					Function: apiToolCallFunction{
						Name:      b.Name,
						Arguments: string(inputJSON),
					},
				})
			}
		}
		msg := apiMessage{
			Role:    "assistant",
			Content: text,
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		return []apiMessage{msg}
	}

	// User messages: split tool_result blocks into separate "tool" messages,
	// and collect any text blocks into a single user message.
	var msgs []apiMessage
	var text string
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool_result":
			msgs = append(msgs, apiMessage{
				Role:       "tool",
				Content:    b.Text,
				ToolCallID: b.ToolUseID,
			})
		}
	}
	if text != "" {
		// Prepend text message before tool results.
		msgs = append([]apiMessage{{Role: "user", Content: text}}, msgs...)
	}
	return msgs
}

func (c *Client) toResponse(apiResp *apiResponse) *llm.Response {
	if len(apiResp.Choices) == 0 {
		return &llm.Response{
			Usage: llm.Usage{
				InputTokens:  apiResp.Usage.PromptTokens,
				OutputTokens: apiResp.Usage.CompletionTokens,
			},
		}
	}

	choice := apiResp.Choices[0]
	var textContent string
	var blocks []llm.ContentBlock

	if choice.Message.Content != "" {
		textContent = choice.Message.Content
		blocks = append(blocks, llm.TextBlock(choice.Message.Content))
	}

	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		blocks = append(blocks, llm.ToolUseBlock(tc.ID, tc.Function.Name, input))
	}

	// Normalize stop reasons to canonical values.
	stopReason := normalizeFinishReason(choice.FinishReason)

	return &llm.Response{
		Content:       textContent,
		ContentBlocks: blocks,
		StopReason:    stopReason,
		Usage: llm.Usage{
			InputTokens:  apiResp.Usage.PromptTokens,
			OutputTokens: apiResp.Usage.CompletionTokens,
		},
	}
}

func normalizeFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return reason
	}
}

// --- OpenAI API wire types (unexported) ---

type apiRequest struct {
	Model     string       `json:"model"`
	Messages  []apiMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens"`
	Tools     []apiTool    `json:"tools,omitempty"`
}

type apiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type apiTool struct {
	Type     string      `json:"type"`
	Function apiFunction `json:"function"`
}

type apiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type apiToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function apiToolCallFunction `json:"function"`
}

type apiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type apiResponse struct {
	Choices []apiChoice `json:"choices"`
	Usage   apiUsage    `json:"usage"`
}

type apiChoice struct {
	Message      apiChoiceMessage `json:"message"`
	FinishReason string           `json:"finish_reason"`
}

type apiChoiceMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []apiToolCall `json:"tool_calls,omitempty"`
}

type apiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
