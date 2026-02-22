// Package slack implements a Slack gateway for Akili using slash commands
// and interactive messages for approval workflows.
//
// Security:
//   - Every request verified via HMAC-SHA256 signature (Slack signing secret)
//   - Replay protection: rejects requests with timestamps older than 5 minutes
//   - User mapping: Slack user IDs mapped to Akili user IDs (default-deny for unmapped)
//   - Signing secret and bot token from environment variables, never config files
//   - All requests logged with correlation IDs
package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/ratelimit"
)

const (
	maxSlackRequestSize = 256 << 10 // 256 KB — Slack payloads are small
	signatureMaxAge     = 5 * time.Minute
)

// Config configures the Slack gateway.
type Config struct {
	SigningSecret   string            // HMAC signing secret (from SLACK_SIGNING_SECRET env var).
	BotToken        string            // Bot token xoxb-... (from SLACK_BOT_TOKEN env var).
	ListenAddr      string            // Webhook listen address, e.g. ":3000".
	UserMapping     map[string]string // Slack user ID → Akili user ID. Unmapped = deny.
	ApprovalChannel string            // Channel ID for approval notifications (optional).
}

// Gateway is the Slack gateway.
type Gateway struct {
	config      Config
	agent       agent.Agent
	approvalMgr approval.ApprovalManager
	limiter     *ratelimit.Limiter
	logger      *slog.Logger
	server      *http.Server
	httpClient  *http.Client
}

// NewGateway creates a Slack gateway.
func NewGateway(cfg Config, a agent.Agent, am approval.ApprovalManager, rl *ratelimit.Limiter, logger *slog.Logger) *Gateway {
	return &Gateway{
		config:      cfg,
		agent:       a,
		approvalMgr: am,
		limiter:     rl,
		logger:      logger,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Start launches the webhook HTTP server and blocks until it exits.
func (g *Gateway) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /slack/commands", g.handleSlashCommand)
	mux.HandleFunc("POST /slack/interactive", g.handleInteraction)

	g.server = &http.Server{
		Addr:              g.config.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	g.logger.Info("slack gateway starting", slog.String("addr", g.config.ListenAddr))

	err := g.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop gracefully shuts down the Slack webhook server.
func (g *Gateway) Stop(ctx context.Context) error {
	if g.server == nil {
		return nil
	}
	g.logger.Info("slack gateway stopping")
	return g.server.Shutdown(ctx)
}

// --- Slash Commands ---

func (g *Gateway) handleSlashCommand(w http.ResponseWriter, r *http.Request) {
	// Read and verify.
	body, err := g.readAndVerify(r)
	if err != nil {
		g.logger.Warn("slack signature verification failed", slog.String("error", err.Error()))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse form data.
	values, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	slackUserID := values.Get("user_id")
	text := values.Get("text")
	channelID := values.Get("channel_id")

	// Map Slack user to Akili user (default-deny).
	userID, ok := g.config.UserMapping[slackUserID]
	if !ok {
		g.logger.Warn("unmapped slack user denied",
			slog.String("slack_user_id", slackUserID),
		)
		writeSlackResponse(w, "You are not authorized to use Akili.")
		return
	}

	// Rate limit.
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			writeSlackResponse(w, "Rate limit exceeded. Please wait before trying again.")
			return
		}
	}

	correlationID := newCorrelationID()

	g.logger.Info("slack command",
		slog.String("user_id", userID),
		slog.String("slack_user_id", slackUserID),
		slog.String("correlation_id", correlationID),
		slog.String("channel_id", channelID),
	)

	// Derive deterministic conversation ID from (userID, channelID).
	conversationID := uuid.NewSHA1(uuid.NameSpaceDNS,
		[]byte(fmt.Sprintf("slack:%s:%s", slackUserID, channelID))).String()

	// Process through agent.
	resp, err := g.agent.Process(r.Context(), &agent.Input{
		UserID:         userID,
		Message:        text,
		CorrelationID:  correlationID,
		ConversationID: conversationID,
	})
	if err != nil {
		var approvalErr *agent.ErrApprovalPending
		if errors.As(err, &approvalErr) {
			// Post approval message with buttons.
			g.postApprovalMessage(r.Context(), channelID, approvalErr)
			writeSlackResponse(w, fmt.Sprintf("Approval required for %s. Check the channel for approval buttons.", approvalErr.ActionName))
			return
		}

		g.logger.Error("agent processing failed",
			slog.String("correlation_id", correlationID),
			slog.String("error", err.Error()),
		)
		writeSlackResponse(w, "Error processing your request.")
		return
	}

	// Check for approval requests in response.
	for _, ar := range resp.ApprovalRequests {
		g.postApprovalMessage(r.Context(), channelID, &agent.ErrApprovalPending{
			ApprovalID: ar.ApprovalID,
			ToolName:   ar.ToolName,
			ActionName: ar.ActionName,
			RiskLevel:  ar.RiskLevel,
			UserID:     ar.UserID,
		})
	}

	writeSlackResponse(w, resp.Message)
}

// --- Interactive Messages (Approval Buttons) ---

func (g *Gateway) handleInteraction(w http.ResponseWriter, r *http.Request) {
	body, err := g.readAndVerify(r)
	if err != nil {
		g.logger.Warn("slack interaction signature failed", slog.String("error", err.Error()))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Slack sends interactive payloads as form-encoded with a "payload" field.
	values, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var payload interactionPayload
	if err := json.Unmarshal([]byte(values.Get("payload")), &payload); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if len(payload.Actions) == 0 {
		http.Error(w, "no actions", http.StatusBadRequest)
		return
	}

	action := payload.Actions[0]
	slackUserID := payload.User.ID

	// Map approver.
	approverID, ok := g.config.UserMapping[slackUserID]
	if !ok {
		g.logger.Warn("unmapped slack approver denied",
			slog.String("slack_user_id", slackUserID),
		)
		writeSlackResponse(w, "You are not authorized to approve actions.")
		return
	}

	// Parse action value: "approve:<id>" or "deny:<id>"
	parts := strings.SplitN(action.Value, ":", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid action value", http.StatusBadRequest)
		return
	}

	decision := parts[0]
	approvalID := parts[1]

	g.logger.Info("slack interaction",
		slog.String("approver_id", approverID),
		slog.String("approval_id", approvalID),
		slog.String("decision", decision),
	)

	if g.approvalMgr == nil {
		writeSlackResponse(w, "Approval manager not configured.")
		return
	}

	if decision == "deny" {
		if err := g.approvalMgr.Deny(r.Context(), approvalID, approverID); err != nil {
			writeSlackResponse(w, fmt.Sprintf("Deny failed: %v", err))
			return
		}
		writeSlackResponse(w, fmt.Sprintf("Denied by %s.", approverID))
		return
	}

	// Approve and resume.
	if err := g.approvalMgr.Approve(r.Context(), approvalID, approverID); err != nil {
		writeSlackResponse(w, fmt.Sprintf("Approval failed: %v", err))
		return
	}

	result, err := g.agent.ResumeWithApproval(r.Context(), approvalID)
	if err != nil {
		writeSlackResponse(w, fmt.Sprintf("Execution after approval failed: %v", err))
		return
	}

	writeSlackResponse(w, fmt.Sprintf("Approved by %s.\n\nResult:\n%s", approverID, result.Output))
}

// --- Signature Verification ---

// readAndVerify reads the request body and verifies the Slack HMAC-SHA256 signature.
// This prevents request forgery and replay attacks.
func (g *Gateway) readAndVerify(r *http.Request) ([]byte, error) {
	// Read body.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSlackRequestSize))
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}
	defer r.Body.Close()

	// Get timestamp and signature from headers.
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	signature := r.Header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return nil, fmt.Errorf("missing signature headers")
	}

	// Replay protection: reject requests older than 5 minutes.
	ts, err := parseUnixTimestamp(timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}
	if time.Since(ts) > signatureMaxAge {
		return nil, fmt.Errorf("request too old (%v ago)", time.Since(ts))
	}

	// Compute expected signature: v0=hmac_sha256(secret, "v0:{timestamp}:{body}")
	baseString := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(g.config.SigningSecret))
	mac.Write([]byte(baseString))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison.
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return nil, fmt.Errorf("signature mismatch")
	}

	return body, nil
}

// --- Slack API ---

// postApprovalMessage posts a message with Approve/Deny buttons to a channel.
func (g *Gateway) postApprovalMessage(ctx context.Context, channelID string, approvalErr *agent.ErrApprovalPending) {
	targetChannel := channelID
	if g.config.ApprovalChannel != "" {
		targetChannel = g.config.ApprovalChannel
	}

	message := map[string]any{
		"channel": targetChannel,
		"text":    fmt.Sprintf("Approval required for *%s* (risk: %s)", approvalErr.ActionName, approvalErr.RiskLevel),
		"attachments": []map[string]any{
			{
				"text":        fmt.Sprintf("Tool: %s\nRequested by: %s\nApproval ID: %s", approvalErr.ToolName, approvalErr.UserID, approvalErr.ApprovalID),
				"callback_id": "akili_approval",
				"actions": []map[string]any{
					{
						"name":  "decision",
						"text":  "Approve",
						"type":  "button",
						"value": "approve:" + approvalErr.ApprovalID,
						"style": "primary",
					},
					{
						"name":  "decision",
						"text":  "Deny",
						"type":  "button",
						"value": "deny:" + approvalErr.ApprovalID,
						"style": "danger",
					},
				},
			},
		},
	}

	body, err := json.Marshal(message)
	if err != nil {
		g.logger.Error("marshaling slack message", slog.String("error", err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		g.logger.Error("creating slack request", slog.String("error", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.config.BotToken)

	resp, err := g.httpClient.Do(req)
	if err != nil {
		g.logger.Error("posting slack message", slog.String("error", err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Error("slack api error", slog.Int("status", resp.StatusCode))
	}
}

// --- Types ---

type interactionPayload struct {
	Actions []interactionAction `json:"actions"`
	User    slackUser           `json:"user"`
}

type interactionAction struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type slackUser struct {
	ID string `json:"id"`
}

// --- Helpers ---

func writeSlackResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"response_type": "ephemeral",
		"text":          text,
	})
}

func parseUnixTimestamp(s string) (time.Time, error) {
	var ts int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return time.Time{}, fmt.Errorf("non-numeric timestamp: %q", s)
		}
		ts = ts*10 + int64(c-'0')
		if ts > math.MaxInt64/10 {
			return time.Time{}, fmt.Errorf("timestamp overflow: %q", s)
		}
	}
	return time.Unix(ts, 0), nil
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
