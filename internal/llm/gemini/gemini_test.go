package gemini

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
		// Verify API key header.
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key test-key, got %q", r.Header.Get("x-goog-api-key"))
		}

		// Verify URL contains the model.
		if r.URL.Path != "/v1beta/models/gemini-2.0-flash:generateContent" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}

		// Should have system instruction.
		if req.SystemInstruction == nil {
			t.Fatal("expected system instruction")
		}

		// Should have 1 user content.
		if len(req.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(req.Contents))
		}
		if req.Contents[0].Role != "user" {
			t.Errorf("expected user role, got %q", req.Contents[0].Role)
		}

		resp := apiResponse{
			Candidates: []apiCandidate{{
				Content:      apiContent{Role: "model", Parts: []apiPart{{Text: "Hello!"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &apiUsage{PromptTokenCount: 10, CandidatesTokenCount: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gemini-2.0-flash", discardLogger(), WithBaseURL(srv.URL))
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

func TestSendMessage_FunctionCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}

		// Verify tools are sent.
		if len(req.Tools) != 1 || len(req.Tools[0].FunctionDeclarations) != 1 {
			t.Fatalf("expected 1 tool declaration, got %+v", req.Tools)
		}
		if req.Tools[0].FunctionDeclarations[0].Name != "shell_exec" {
			t.Errorf("expected tool shell_exec, got %q", req.Tools[0].FunctionDeclarations[0].Name)
		}

		resp := apiResponse{
			Candidates: []apiCandidate{{
				Content: apiContent{
					Role: "model",
					Parts: []apiPart{{
						FunctionCall: &apiFunctionCall{
							Name: "shell_exec",
							Args: map[string]any{"command": "ls -la"},
						},
					}},
				},
				FinishReason: "STOP",
			}},
			UsageMetadata: &apiUsage{PromptTokenCount: 20, CandidatesTokenCount: 15},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gemini-2.0-flash", discardLogger(), WithBaseURL(srv.URL))
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
	if blocks[0].ID != "gemini-call-0" {
		t.Errorf("expected synthetic ID gemini-call-0, got %q", blocks[0].ID)
	}
}

func TestSendMessage_FunctionResultRoundTrip(t *testing.T) {
	var capturedReq apiRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		resp := apiResponse{
			Candidates: []apiCandidate{{
				Content:      apiContent{Role: "model", Parts: []apiPart{{Text: "Done."}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &apiUsage{PromptTokenCount: 30, CandidatesTokenCount: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient("test-key", "gemini-2.0-flash", discardLogger(), WithBaseURL(srv.URL))
	_, err := client.SendMessage(context.Background(), &llm.Request{
		SystemPrompt: "You are helpful.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "list files"},
			{
				Role: llm.RoleAssistant,
				ContentBlocks: []llm.ContentBlock{
					llm.ToolUseBlock("gemini-call-0", "shell_exec", map[string]any{"command": "ls"}),
				},
			},
			{
				Role: llm.RoleUser,
				ContentBlocks: []llm.ContentBlock{
					llm.ToolResultBlock("gemini-call-0", "file1.txt\nfile2.txt", false),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// user + model (function call) + user (function response) = 3 contents.
	if len(capturedReq.Contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(capturedReq.Contents))
	}

	// Model content should have functionCall part.
	modelContent := capturedReq.Contents[1]
	if modelContent.Role != "model" {
		t.Errorf("expected model role, got %q", modelContent.Role)
	}
	if len(modelContent.Parts) != 1 || modelContent.Parts[0].FunctionCall == nil {
		t.Fatal("expected functionCall part in model content")
	}
	if modelContent.Parts[0].FunctionCall.Name != "shell_exec" {
		t.Errorf("expected function name shell_exec, got %q", modelContent.Parts[0].FunctionCall.Name)
	}

	// User content should have functionResponse part.
	resultContent := capturedReq.Contents[2]
	if resultContent.Role != "user" {
		t.Errorf("expected user role, got %q", resultContent.Role)
	}
	if len(resultContent.Parts) != 1 || resultContent.Parts[0].FunctionResponse == nil {
		t.Fatal("expected functionResponse part in result content")
	}
	if resultContent.Parts[0].FunctionResponse.Name != "shell_exec" {
		t.Errorf("expected function name shell_exec, got %q", resultContent.Parts[0].FunctionResponse.Name)
	}
}

func TestSendMessage_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"API key invalid"}}`))
	}))
	defer srv.Close()

	client := NewClient("bad-key", "gemini-2.0-flash", discardLogger(), WithBaseURL(srv.URL))
	_, err := client.SendMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNormalizeFinishReason(t *testing.T) {
	tests := []struct {
		reason       string
		hasToolCalls bool
		want         string
	}{
		{"STOP", false, "end_turn"},
		{"STOP", true, "tool_use"},
		{"MAX_TOKENS", false, "max_tokens"},
		{"SAFETY", false, "SAFETY"},
	}
	for _, tt := range tests {
		if got := normalizeFinishReason(tt.reason, tt.hasToolCalls); got != tt.want {
			t.Errorf("normalizeFinishReason(%q, %v) = %q, want %q", tt.reason, tt.hasToolCalls, got, tt.want)
		}
	}
}

func TestBuildToolIDMap(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{
			Role: llm.RoleAssistant,
			ContentBlocks: []llm.ContentBlock{
				llm.ToolUseBlock("id-1", "shell_exec", nil),
				llm.ToolUseBlock("id-2", "file_read", nil),
			},
		},
		{Role: llm.RoleUser, Content: "result"},
	}
	m := buildToolIDMap(messages)
	if m["id-1"] != "shell_exec" {
		t.Errorf("expected shell_exec for id-1, got %q", m["id-1"])
	}
	if m["id-2"] != "file_read" {
		t.Errorf("expected file_read for id-2, got %q", m["id-2"])
	}
}
