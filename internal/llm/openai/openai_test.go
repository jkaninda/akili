package openai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jkaninda/akili/internal/llm"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSendMessage_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request structure.
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %q", r.Header.Get("Authorization"))
		}

		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}

		if req.Model != "gpt-4o" {
			t.Errorf("expected model gpt-4o, got %q", req.Model)
		}
		// Should have system + user messages.
		if len(req.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("expected system role, got %q", req.Messages[0].Role)
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("expected user role, got %q", req.Messages[1].Role)
		}

		resp := apiResponse{
			Choices: []apiChoice{{
				Message:      apiChoiceMessage{Role: "assistant", Content: "Hello!"},
				FinishReason: "stop",
			}},
			Usage: apiUsage{PromptTokens: 10, CompletionTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gpt-4o", discardLogger(), WithBaseURL(srv.URL))
	resp, err := client.SendMessage(context.Background(), &llm.Request{
		SystemPrompt: "You are helpful.",
		Messages:     []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("expected content Hello!, got %q", resp.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected stop reason end_turn, got %q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestSendMessage_ToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}

		// Verify tools are sent.
		if len(req.Tools) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(req.Tools))
		}
		if req.Tools[0].Function.Name != "shell_exec" {
			t.Errorf("expected tool shell_exec, got %q", req.Tools[0].Function.Name)
		}

		resp := apiResponse{
			Choices: []apiChoice{{
				Message: apiChoiceMessage{
					Role: "assistant",
					ToolCalls: []apiToolCall{{
						ID:   "call_123",
						Type: "function",
						Function: apiToolCallFunction{
							Name:      "shell_exec",
							Arguments: `{"command":"ls -la"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: apiUsage{PromptTokens: 20, CompletionTokens: 15},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gpt-4o", discardLogger(), WithBaseURL(srv.URL))
	resp, err := client.SendMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "list files"}},
		Tools: []llm.ToolDefinition{{
			Name:        "shell_exec",
			Description: "Execute a shell command",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected stop reason tool_use, got %q", resp.StopReason)
	}
	if !resp.HasToolUse() {
		t.Error("expected HasToolUse() to return true")
	}
	blocks := resp.ToolUseBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 tool use block, got %d", len(blocks))
	}
	if blocks[0].Name != "shell_exec" {
		t.Errorf("expected tool name shell_exec, got %q", blocks[0].Name)
	}
	if blocks[0].ID != "call_123" {
		t.Errorf("expected tool ID call_123, got %q", blocks[0].ID)
	}
}

func TestSendMessage_ToolResultRoundTrip(t *testing.T) {
	// Verify that a conversation with tool results is correctly formatted.
	var capturedReq apiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			Choices: []apiChoice{{
				Message:      apiChoiceMessage{Role: "assistant", Content: "Done."},
				FinishReason: "stop",
			}},
			Usage: apiUsage{PromptTokens: 30, CompletionTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gpt-4o", discardLogger(), WithBaseURL(srv.URL))
	_, err := client.SendMessage(context.Background(), &llm.Request{
		SystemPrompt: "You are helpful.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "list files"},
			{
				Role: llm.RoleAssistant,
				ContentBlocks: []llm.ContentBlock{
					llm.ToolUseBlock("call_1", "shell_exec", map[string]any{"command": "ls"}),
				},
			},
			{
				Role: llm.RoleUser,
				ContentBlocks: []llm.ContentBlock{
					llm.ToolResultBlock("call_1", "file1.txt\nfile2.txt", false),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system + user + assistant (with tool_calls) + tool result = 4 messages.
	if len(capturedReq.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(capturedReq.Messages))
	}

	// Assistant message should have tool_calls.
	assistant := capturedReq.Messages[2]
	if assistant.Role != "assistant" {
		t.Errorf("expected assistant role, got %q", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(assistant.ToolCalls))
	}

	// Tool result message should have role "tool".
	toolMsg := capturedReq.Messages[3]
	if toolMsg.Role != "tool" {
		t.Errorf("expected tool role, got %q", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %q", toolMsg.ToolCallID)
	}
}

func TestSendMessage_NoAuth(t *testing.T) {
	// Ollama scenario: no API key.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no Authorization header, got %q", auth)
		}
		resp := apiResponse{
			Choices: []apiChoice{{
				Message:      apiChoiceMessage{Role: "assistant", Content: "OK"},
				FinishReason: "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("", "llama3.1", discardLogger(), WithBaseURL(srv.URL), WithName("ollama"))
	if client.Name() != "ollama" {
		t.Errorf("expected name ollama, got %q", client.Name())
	}

	resp, err := client.SendMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "OK" {
		t.Errorf("expected content OK, got %q", resp.Content)
	}
}

func TestSendMessage_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer srv.Close()

	client := NewClient("test-key", "gpt-4o", discardLogger(), WithBaseURL(srv.URL))
	_, err := client.SendMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNormalizeFinishReason(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"stop", "end_turn"},
		{"tool_calls", "tool_use"},
		{"length", "max_tokens"},
		{"content_filter", "content_filter"},
	}
	for _, tt := range tests {
		if got := normalizeFinishReason(tt.input); got != tt.want {
			t.Errorf("normalizeFinishReason(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
