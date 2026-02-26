package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	goutils "github.com/jkaninda/go-utils"
)

// Exit codes for the query command.
const (
	ExitSuccess          = 0
	ExitFailure          = 1
	ExitPolicyDenied     = 2
	ExitAgentUnavailable = 3
)

var (
	queryMessage    string
	queryGatewayURL string
	queryAPIKey     string
	queryStream     bool
	queryTimeout    int
	queryConvID     string
	queryAutonomous bool
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Send a one-shot query to the gateway",
	Long: `Send a message to the Akili gateway for processing.
The query is routed through the gateway's policy engine and dispatched
to an available agent. All tasks pass through full policy validation.

Examples:
  akili query -m "check if the pod nginx is running"
  akili query -m "list all pods in namespace production" --stream
  akili query -m "restart the nginx deployment" --autonomous

Exit codes:
  0  success
  1  execution failure
  2  policy denied or approval required
  3  gateway or agent unavailable`,
	RunE: runQuery,
}

func init() {
	queryCmd.Flags().StringVarP(&queryMessage, "message", "m", "", "message to send (required)")
	queryCmd.Flags().StringVar(&queryGatewayURL, "gateway-url", "http://localhost:8080", "gateway HTTP API URL")
	queryCmd.Flags().StringVar(&queryAPIKey, "api-key", "", "API key for gateway authentication (or AKILI_API_KEY env)")
	queryCmd.Flags().BoolVar(&queryStream, "stream", false, "stream response via SSE")
	queryCmd.Flags().IntVar(&queryTimeout, "timeout", 300, "timeout in seconds")
	queryCmd.Flags().StringVar(&queryConvID, "conversation-id", "", "conversation ID for multi-turn context")
	queryCmd.Flags().BoolVar(&queryAutonomous, "autonomous", false, "enable autonomous mode for this query")

	_ = queryCmd.MarkFlagRequired("message")
}

func runQuery(_ *cobra.Command, _ []string) error {
	if queryMessage == "" {
		return fmt.Errorf("message is required: use -m flag")
	}

	// Resolve API key from flag or env.
	apiKey := goutils.Env("AKILI_API_KEY", queryAPIKey)
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: API key required (use --api-key or set AKILI_API_KEY)")
		os.Exit(ExitPolicyDenied)
	}

	// Resolve gateway URL from flag or env.
	gatewayURL := goutils.Env("AKILI_GATEWAY_URL", queryGatewayURL)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(queryTimeout)*time.Second)
	defer cancel()

	if queryStream {
		return runQuerySSE(ctx, gatewayURL, apiKey)
	}
	return runQueryHTTP(ctx, gatewayURL, apiKey)
}

// runQueryHTTP sends a synchronous query and prints the response.
func runQueryHTTP(ctx context.Context, gatewayURL, apiKey string) error {
	reqBody, _ := json.Marshal(map[string]any{
		"message":         queryMessage,
		"conversation_id": queryConvID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL+"/v1/query", bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitFailure)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot reach gateway at %s: %v\n", gatewayURL, err)
		os.Exit(ExitAgentUnavailable)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var result struct {
			Message        string `json:"message"`
			CorrelationID  string `json:"correlation_id"`
			ConversationID string `json:"conversation_id"`
			TokensUsed     int    `json:"tokens_used"`
		}
		_ = json.Unmarshal(respBody, &result)
		fmt.Println(result.Message)
		fmt.Fprintf(os.Stderr, "\n[correlation_id=%s conversation_id=%s tokens=%d]\n",
			result.CorrelationID, result.ConversationID, result.TokensUsed)
		os.Exit(ExitSuccess)

	case http.StatusAccepted:
		var pending struct {
			ApprovalID string `json:"approval_id"`
			Tool       string `json:"tool"`
			RiskLevel  string `json:"risk_level"`
			Message    string `json:"message"`
		}
		_ = json.Unmarshal(respBody, &pending)
		fmt.Fprintf(os.Stderr, "Approval required: %s\n", pending.Message)
		fmt.Fprintf(os.Stderr, "  approval_id: %s\n  tool: %s\n  risk: %s\n",
			pending.ApprovalID, pending.Tool, pending.RiskLevel)
		os.Exit(ExitPolicyDenied)

	case http.StatusUnauthorized:
		fmt.Fprintln(os.Stderr, "Error: unauthorized (check API key)")
		os.Exit(ExitPolicyDenied)

	case http.StatusTooManyRequests:
		fmt.Fprintln(os.Stderr, "Error: rate limited — try again later")
		os.Exit(ExitPolicyDenied)

	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		fmt.Fprintf(os.Stderr, "Error: gateway unavailable (%d)\n", resp.StatusCode)
		os.Exit(ExitAgentUnavailable)

	default:
		fmt.Fprintf(os.Stderr, "Error: gateway returned %d: %s\n", resp.StatusCode, string(respBody))
		os.Exit(ExitFailure)
	}

	return nil
}

// runQuerySSE sends a streaming query and prints events as they arrive.
func runQuerySSE(ctx context.Context, gatewayURL, apiKey string) error {
	reqBody, _ := json.Marshal(map[string]any{
		"message":         queryMessage,
		"conversation_id": queryConvID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL+"/v1/query/stream", bytes.NewReader(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(ExitFailure)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot reach gateway at %s: %v\n", gatewayURL, err)
		os.Exit(ExitAgentUnavailable)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Fprintln(os.Stderr, "Error: unauthorized (check API key)")
		os.Exit(ExitPolicyDenied)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: gateway returned %d: %s\n", resp.StatusCode, string(body))
		os.Exit(ExitFailure)
	}

	// Parse SSE stream.
	scanner := bufio.NewScanner(resp.Body)
	exitCode := ExitSuccess

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}

		var event struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Tool    string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "text":
			fmt.Print(event.Content)
		case "tool_result":
			fmt.Fprintf(os.Stderr, "[tool: %s]\n", event.Tool)
		case "error":
			fmt.Fprintf(os.Stderr, "Error: %s\n", event.Content)
			exitCode = ExitFailure
		case "done":
			fmt.Println()
			os.Exit(exitCode)
		}
	}

	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "Error: stream interrupted: %v\n", err)
		os.Exit(ExitFailure)
	}

	return nil
}
