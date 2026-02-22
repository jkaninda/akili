// Package anthropic implements the LLM provider interface for the Anthropic Messages API.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jkaninda/akili/internal/llm"
)

const (
	defaultBaseURL  = "https://api.anthropic.com"
	messagesPath    = "/v1/messages"
	apiVersion      = "2023-06-01"
	defaultMaxToken = 4096
)

// Client implements llm.Provider using the Anthropic Messages API.
type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// Option configures the Anthropic client.
type Option func(*Client)

// WithBaseURL overrides the API base URL (useful for testing).
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// NewClient creates an Anthropic provider.
func NewClient(apiKey, model string, logger *slog.Logger, opts ...Option) *Client {
	c := &Client{
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
		logger:     logger,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return "anthropic" }

// SendMessage sends the conversation to the Anthropic Messages API.
func (c *Client) SendMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	apiReq := c.buildRequest(req)

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)

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
		slog.String("provider", "anthropic"),
		slog.String("model", c.model),
		slog.Int("input_tokens", resp.Usage.InputTokens),
		slog.Int("output_tokens", resp.Usage.OutputTokens),
		slog.String("stop_reason", resp.StopReason),
	)

	return resp, nil
}

func (c *Client) buildRequest(req *llm.Request) apiRequest {
	messages := make([]apiMessage, len(req.Messages))
	for i, m := range req.Messages {
		if len(m.ContentBlocks) > 0 {
			// Structured content: convert to API content blocks.
			blocks := make([]apiContentBlock, len(m.ContentBlocks))
			for j, b := range m.ContentBlocks {
				blocks[j] = toAPIContentBlock(b)
			}
			messages[i] = apiMessage{
				Role:    string(m.Role),
				Content: blocks,
			}
		} else {
			// Plain text (backward compat).
			messages[i] = apiMessage{
				Role:    string(m.Role),
				Content: m.Content,
			}
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxToken
	}

	apiReq := apiRequest{
		Model:     c.model,
		System:    req.SystemPrompt,
		Messages:  messages,
		MaxTokens: maxTokens,
	}

	// Add tool definitions if provided.
	for _, t := range req.Tools {
		apiReq.Tools = append(apiReq.Tools, apiTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	return apiReq
}

func (c *Client) toResponse(apiResp *apiResponse) *llm.Response {
	var textContent string
	var blocks []llm.ContentBlock

	for _, block := range apiResp.Content {
		switch block.Type {
		case "text":
			textContent += block.Text
			blocks = append(blocks, llm.TextBlock(block.Text))
		case "tool_use":
			blocks = append(blocks, llm.ToolUseBlock(block.ID, block.Name, block.Input))
		}
	}

	return &llm.Response{
		Content:       textContent,
		ContentBlocks: blocks,
		StopReason:    apiResp.StopReason,
		Usage: llm.Usage{
			InputTokens:  apiResp.Usage.InputTokens,
			OutputTokens: apiResp.Usage.OutputTokens,
		},
	}
}

// toAPIContentBlock converts an llm.ContentBlock to the Anthropic API format.
func toAPIContentBlock(b llm.ContentBlock) apiContentBlock {
	block := apiContentBlock{Type: b.Type}
	switch b.Type {
	case "text":
		block.Text = b.Text
	case "tool_use":
		block.ID = b.ID
		block.Name = b.Name
		block.Input = b.Input
	case "tool_result":
		block.ToolUseID = b.ToolUseID
		block.Content = b.Text
		block.IsError = b.IsError
	}
	return block
}

// --- Anthropic API wire types (unexported) ---

type apiRequest struct {
	Model     string       `json:"model"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens"`
	Tools     []apiTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream,omitempty"`
}

// StreamMessage implements llm.StreamingProvider by using Anthropic's streaming API.
func (c *Client) StreamMessage(ctx context.Context, req *llm.Request, events chan<- llm.StreamEvent) error {
	defer close(events)

	apiReq := c.buildRequest(req)
	apiReq.Stream = true

	body, err := json.Marshal(apiReq)
	if err != nil {
		events <- llm.StreamEvent{Type: "error", Error: fmt.Errorf("marshaling request: %w", err)}
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		events <- llm.StreamEvent{Type: "error", Error: fmt.Errorf("creating HTTP request: %w", err)}
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", c.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		events <- llm.StreamEvent{Type: "error", Error: fmt.Errorf("sending request: %w", err)}
		return err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		err := fmt.Errorf("API error (status %d): %s", httpResp.StatusCode, string(respBody))
		events <- llm.StreamEvent{Type: "error", Error: err}
		return err
	}

	// Parse SSE stream from Anthropic.
	scanner := bufio.NewScanner(httpResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var streamEvent apiStreamEvent
		if err := json.Unmarshal([]byte(data), &streamEvent); err != nil {
			continue
		}

		switch streamEvent.Type {
		case "content_block_start":
			if streamEvent.ContentBlock != nil && streamEvent.ContentBlock.Type == "tool_use" {
				events <- llm.StreamEvent{
					Type: "tool_use_start",
					ToolUse: &llm.ContentBlock{
						Type: "tool_use",
						ID:   streamEvent.ContentBlock.ID,
						Name: streamEvent.ContentBlock.Name,
					},
				}
			}
		case "content_block_delta":
			if streamEvent.Delta != nil {
				switch streamEvent.Delta.Type {
				case "text_delta":
					events <- llm.StreamEvent{Type: "text", Content: streamEvent.Delta.Text}
				case "input_json_delta":
					// Tool input streaming — accumulate but don't emit individual deltas.
				}
			}
		case "message_stop":
			events <- llm.StreamEvent{Type: "done"}
			return nil
		case "error":
			err := fmt.Errorf("stream error: %s", data)
			events <- llm.StreamEvent{Type: "error", Error: err}
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Type: "error", Error: err}
		return err
	}

	events <- llm.StreamEvent{Type: "done"}
	return nil
}

// apiStreamEvent represents a single event in the Anthropic streaming response.
type apiStreamEvent struct {
	Type         string           `json:"type"`
	ContentBlock *apiContentBlock `json:"content_block,omitempty"`
	Delta        *apiStreamDelta  `json:"delta,omitempty"`
	Index        int              `json:"index,omitempty"`
}

// apiStreamDelta represents a delta update in a streaming response.
type apiStreamDelta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type apiTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// apiMessage supports both plain-text and structured content.
// Content is either a string or []apiContentBlock.
type apiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type apiContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type apiResponse struct {
	Content    []apiContentBlock `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      apiUsage          `json:"usage"`
}

type apiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
