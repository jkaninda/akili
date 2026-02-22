// Package gemini implements the LLM provider interface for the Google Gemini API.
package gemini

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
	defaultBaseURL   = "https://generativelanguage.googleapis.com"
	defaultMaxTokens = 4096
)

// Client implements llm.Provider using the Google Gemini API.
type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// Option configures the Gemini client.
type Option func(*Client)

// WithBaseURL overrides the API base URL.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = url }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// NewClient creates a Gemini provider.
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

func (c *Client) Name() string { return "gemini" }

// SendMessage sends the conversation to the Gemini generateContent API.
func (c *Client) SendMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	apiReq := c.buildRequest(req)

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.baseURL, c.model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.apiKey)

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
		slog.String("provider", "gemini"),
		slog.String("model", c.model),
		slog.Int("input_tokens", resp.Usage.InputTokens),
		slog.Int("output_tokens", resp.Usage.OutputTokens),
		slog.String("stop_reason", resp.StopReason),
	)

	return resp, nil
}

func (c *Client) buildRequest(req *llm.Request) apiRequest {
	// Build an ID-to-name mapping from all assistant messages so we can
	// resolve tool_result blocks back to function names.
	idToName := buildToolIDMap(req.Messages)

	var contents []apiContent
	for _, m := range req.Messages {
		contents = append(contents, toGeminiContent(m, idToName)...)
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	apiReq := apiRequest{
		Contents:         contents,
		GenerationConfig: &apiGenerationConfig{MaxOutputTokens: maxTokens},
	}

	if req.SystemPrompt != "" {
		apiReq.SystemInstruction = &apiContent{
			Parts: []apiPart{{Text: req.SystemPrompt}},
		}
	}

	if len(req.Tools) > 0 {
		var decls []apiFunctionDeclaration
		for _, t := range req.Tools {
			decls = append(decls, apiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
		apiReq.Tools = []apiToolDeclaration{{FunctionDeclarations: decls}}
	}

	return apiReq
}

// buildToolIDMap scans assistant messages for tool_use blocks and returns
// a mapping from synthetic tool use ID to function name.
func buildToolIDMap(messages []llm.Message) map[string]string {
	m := make(map[string]string)
	for _, msg := range messages {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, b := range msg.ContentBlocks {
			if b.Type == "tool_use" && b.ID != "" {
				m[b.ID] = b.Name
			}
		}
	}
	return m
}

// toGeminiContent converts an llm.Message to Gemini API content entries.
func toGeminiContent(m llm.Message, idToName map[string]string) []apiContent {
	role := "user"
	if m.Role == llm.RoleAssistant {
		role = "model"
	}

	if len(m.ContentBlocks) == 0 {
		return []apiContent{{
			Role:  role,
			Parts: []apiPart{{Text: m.Content}},
		}}
	}

	var parts []apiPart
	for _, b := range m.ContentBlocks {
		switch b.Type {
		case "text":
			parts = append(parts, apiPart{Text: b.Text})
		case "tool_use":
			parts = append(parts, apiPart{
				FunctionCall: &apiFunctionCall{
					Name: b.Name,
					Args: b.Input,
				},
			})
		case "tool_result":
			name := idToName[b.ToolUseID]
			parts = append(parts, apiPart{
				FunctionResponse: &apiFunctionResponse{
					Name: name,
					Response: map[string]any{
						"content": b.Text,
					},
				},
			})
		}
	}

	return []apiContent{{Role: role, Parts: parts}}
}

func (c *Client) toResponse(apiResp *apiResponse) *llm.Response {
	if len(apiResp.Candidates) == 0 {
		return &llm.Response{
			Usage: extractUsage(apiResp),
		}
	}

	candidate := apiResp.Candidates[0]
	var textContent string
	var blocks []llm.ContentBlock
	var hasToolCalls bool
	callIdx := 0

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			textContent += part.Text
			blocks = append(blocks, llm.TextBlock(part.Text))
		}
		if part.FunctionCall != nil {
			hasToolCalls = true
			id := fmt.Sprintf("gemini-call-%d", callIdx)
			callIdx++
			blocks = append(blocks, llm.ToolUseBlock(id, part.FunctionCall.Name, part.FunctionCall.Args))
		}
	}

	stopReason := normalizeFinishReason(candidate.FinishReason, hasToolCalls)

	return &llm.Response{
		Content:       textContent,
		ContentBlocks: blocks,
		StopReason:    stopReason,
		Usage:         extractUsage(apiResp),
	}
}

func extractUsage(apiResp *apiResponse) llm.Usage {
	if apiResp.UsageMetadata == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:  apiResp.UsageMetadata.PromptTokenCount,
		OutputTokens: apiResp.UsageMetadata.CandidatesTokenCount,
	}
}

func normalizeFinishReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_use"
	}
	switch reason {
	case "STOP":
		return "end_turn"
	case "MAX_TOKENS":
		return "max_tokens"
	default:
		return reason
	}
}

// --- Gemini API wire types (unexported) ---

type apiRequest struct {
	Contents         []apiContent         `json:"contents"`
	SystemInstruction *apiContent          `json:"system_instruction,omitempty"`
	Tools            []apiToolDeclaration `json:"tools,omitempty"`
	GenerationConfig *apiGenerationConfig `json:"generation_config,omitempty"`
}

type apiContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []apiPart `json:"parts"`
}

type apiPart struct {
	Text             string               `json:"text,omitempty"`
	FunctionCall     *apiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *apiFunctionResponse `json:"functionResponse,omitempty"`
}

type apiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type apiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type apiToolDeclaration struct {
	FunctionDeclarations []apiFunctionDeclaration `json:"function_declarations"`
}

type apiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type apiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type apiResponse struct {
	Candidates    []apiCandidate `json:"candidates"`
	UsageMetadata *apiUsage      `json:"usageMetadata,omitempty"`
}

type apiCandidate struct {
	Content      apiContent `json:"content"`
	FinishReason string     `json:"finishReason"`
}

type apiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}
