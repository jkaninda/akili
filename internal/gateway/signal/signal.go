// Package signal implements a Signal gateway for Akili using
// the signal-cli-rest-api for message send/receive.
//
// Security:
//   - Phone number allowlist: only explicitly listed numbers can interact (default-deny)
//   - User mapping: Signal phone numbers mapped to Akili user IDs
//   - signal-cli-rest-api should be deployed on a private network
//   - Per-user rate limiting
//   - All requests logged with correlation IDs
package signal

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/ratelimit"
)

const (
	defaultPollInterval = 2 * time.Second
	maxReceiveSize      = 256 << 10 // 256 KB
	signalSafeMaxLen    = 4000
)

// Config configures the Signal gateway.
type Config struct {
	APIURL              string            // Base URL of signal-cli-rest-api.
	SenderNumber        string            // Registered Signal number.
	AllowedUsers        []string          // Signal phone numbers allowed. Empty = deny all.
	UserMapping         map[string]string // Signal phone number → Akili user ID.
	PollIntervalSeconds int               // Receive poll interval. 0 = 2s default.
}

// Gateway is the Signal gateway.
type Gateway struct {
	config      Config
	agent       agent.Agent
	approvalMgr approval.ApprovalManager
	limiter     *ratelimit.Limiter
	logger      *slog.Logger
	httpClient  *http.Client
	cancel      context.CancelFunc
	allowed     map[string]bool // pre-computed allowlist
}

// NewGateway creates a Signal gateway.
func NewGateway(cfg Config, a agent.Agent, am approval.ApprovalManager, rl *ratelimit.Limiter, logger *slog.Logger) *Gateway {
	allowed := make(map[string]bool, len(cfg.AllowedUsers))
	for _, phone := range cfg.AllowedUsers {
		allowed[phone] = true
	}
	return &Gateway{
		config:      cfg,
		agent:       a,
		approvalMgr: am,
		limiter:     rl,
		logger:      logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		allowed: allowed,
	}
}

// Start launches the polling loop and blocks until context is canceled.
func (g *Gateway) Start(ctx context.Context) error {
	ctx, g.cancel = context.WithCancel(ctx)
	return g.startPolling(ctx)
}

// Stop gracefully shuts down the gateway.
func (g *Gateway) Stop(_ context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	g.logger.Info("signal gateway stopping")
	return nil
}

// --- Polling ---

func (g *Gateway) startPolling(ctx context.Context) error {
	interval := g.config.pollInterval()
	g.logger.Info("signal gateway starting polling",
		slog.String("api_url", g.config.APIURL),
		slog.String("sender", g.config.SenderNumber),
		slog.Duration("interval", interval),
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			messages, err := g.receiveMessages(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				g.logger.Error("signal receive failed", slog.String("error", err.Error()))
				continue
			}
			for _, msg := range messages {
				g.processMessage(ctx, &msg)
			}
		}
	}
}

func (g *Gateway) receiveMessages(ctx context.Context) ([]Envelope, error) {
	url := fmt.Sprintf("%s/v1/receive/%s", strings.TrimRight(g.config.APIURL, "/"), g.config.SenderNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("signal API returned %d: %s", resp.StatusCode, string(body))
	}

	var envelopes []Envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReceiveSize)).Decode(&envelopes); err != nil {
		return nil, fmt.Errorf("decoding envelopes: %w", err)
	}
	return envelopes, nil
}

// --- Message Processing ---

func (g *Gateway) processMessage(ctx context.Context, env *Envelope) {
	// Only process data messages with body text.
	if env.Envelope.DataMessage == nil || env.Envelope.DataMessage.Message == "" {
		return
	}

	senderPhone := env.Envelope.Source
	if senderPhone == "" {
		return
	}

	// Allowlist check (default-deny).
	if !g.allowed[senderPhone] {
		g.logger.Warn("signal user not in allowlist",
			slog.String("phone", senderPhone),
		)
		g.sendMessage(ctx, senderPhone, "", "You are not authorized to use Akili.")
		return
	}

	// Map to Akili user ID.
	userID, ok := g.config.UserMapping[senderPhone]
	if !ok {
		g.logger.Warn("signal user not mapped",
			slog.String("phone", senderPhone),
		)
		g.sendMessage(ctx, senderPhone, "", "Your Signal number is not mapped to an Akili user.")
		return
	}

	// Rate limit.
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			g.sendMessage(ctx, senderPhone, "", "Rate limit exceeded. Please wait before trying again.")
			return
		}
	}

	correlationID := newCorrelationID()
	text := env.Envelope.DataMessage.Message

	// Determine reply target: group or individual.
	groupID := ""
	if env.Envelope.DataMessage.GroupInfo != nil {
		groupID = env.Envelope.DataMessage.GroupInfo.GroupID
	}

	g.logger.Info("signal message",
		slog.String("user_id", userID),
		slog.String("phone", senderPhone),
		slog.String("correlation_id", correlationID),
		slog.String("group_id", groupID),
	)

	// Handle approval commands (/approve <id>, /deny <id>).
	if strings.HasPrefix(text, "/approve ") || strings.HasPrefix(text, "/deny ") {
		g.handleApprovalCommand(ctx, senderPhone, groupID, userID, text)
		return
	}

	// Derive deterministic conversation ID from (phone, groupID).
	convSeed := fmt.Sprintf("signal:%s:%s", senderPhone, groupID)
	conversationID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(convSeed)).String()

	// Process through agent.
	resp, err := g.agent.Process(ctx, &agent.Input{
		UserID:         userID,
		Message:        text,
		CorrelationID:  correlationID,
		ConversationID: conversationID,
	})
	if err != nil {
		var approvalErr *agent.ErrApprovalPending
		if errors.As(err, &approvalErr) {
			g.sendApprovalMessage(ctx, senderPhone, groupID, approvalErr)
			return
		}

		g.logger.Error("agent processing failed",
			slog.String("correlation_id", correlationID),
			slog.String("error", err.Error()),
		)
		g.sendMessage(ctx, senderPhone, groupID, "Error processing your request.")
		return
	}

	// Check for approval requests in response.
	for _, ar := range resp.ApprovalRequests {
		g.sendApprovalMessage(ctx, senderPhone, groupID, &agent.ErrApprovalPending{
			ApprovalID: ar.ApprovalID,
			ToolName:   ar.ToolName,
			ActionName: ar.ActionName,
			RiskLevel:  ar.RiskLevel,
			UserID:     ar.UserID,
		})
	}

	g.sendMessage(ctx, senderPhone, groupID, resp.Message)
}

func (g *Gateway) handleApprovalCommand(ctx context.Context, senderPhone, groupID, approverID, text string) {
	parts := strings.Fields(text)
	if len(parts) != 2 {
		g.sendMessage(ctx, senderPhone, groupID, "Usage: /approve <id> or /deny <id>")
		return
	}

	command := strings.TrimPrefix(parts[0], "/")
	approvalID := parts[1]

	if g.approvalMgr == nil {
		g.sendMessage(ctx, senderPhone, groupID, "Approval manager not configured.")
		return
	}

	g.logger.Info("signal approval command",
		slog.String("approver_id", approverID),
		slog.String("approval_id", approvalID),
		slog.String("command", command),
	)

	if command == "deny" {
		if err := g.approvalMgr.Deny(ctx, approvalID, approverID); err != nil {
			g.sendMessage(ctx, senderPhone, groupID, fmt.Sprintf("Deny failed: %v", err))
			return
		}
		g.sendMessage(ctx, senderPhone, groupID, fmt.Sprintf("Denied by %s.", approverID))
		return
	}

	// Approve and resume.
	if err := g.approvalMgr.Approve(ctx, approvalID, approverID); err != nil {
		g.sendMessage(ctx, senderPhone, groupID, fmt.Sprintf("Approval failed: %v", err))
		return
	}

	result, err := g.agent.ResumeWithApproval(ctx, approvalID)
	if err != nil {
		g.sendMessage(ctx, senderPhone, groupID, fmt.Sprintf("Execution after approval failed: %v", err))
		return
	}

	g.sendMessage(ctx, senderPhone, groupID, fmt.Sprintf("Approved by %s.\n\nResult:\n%s", approverID, result.Output))
}

// --- Signal API ---

func (g *Gateway) sendMessage(ctx context.Context, recipient, groupID, text string) {
	if text == "" {
		return
	}

	chunks := splitMessage(text, signalSafeMaxLen)
	for _, chunk := range chunks {
		g.callSend(ctx, recipient, groupID, chunk)
	}
}

func (g *Gateway) callSend(ctx context.Context, recipient, groupID, text string) {
	payload := map[string]any{
		"message": text,
		"number":  g.config.SenderNumber,
	}
	if groupID != "" {
		payload["recipients"] = []string{}
		payload["group_id"] = groupID
	} else {
		payload["recipients"] = []string{recipient}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		g.logger.Error("signal marshal error", slog.String("error", err.Error()))
		return
	}

	url := strings.TrimRight(g.config.APIURL, "/") + "/v2/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		g.logger.Error("signal request error", slog.String("error", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		g.logger.Error("signal api error", slog.String("error", err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		g.logger.Error("signal api non-2xx",
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBody)),
		)
	}
}

func (g *Gateway) sendApprovalMessage(ctx context.Context, recipient, groupID string, err *agent.ErrApprovalPending) {
	// Signal doesn't support inline buttons, so we use text-based approval commands.
	text := fmt.Sprintf(
		"APPROVAL REQUIRED\n\n"+
			"Tool: %s\n"+
			"Action: %s\n"+
			"Risk: %s\n"+
			"Requested by: %s\n\n"+
			"Approval ID: %s\n\n"+
			"Reply with:\n"+
			"  /approve %s\n"+
			"  /deny %s",
		err.ToolName,
		err.ActionName,
		err.RiskLevel,
		err.UserID,
		err.ApprovalID,
		err.ApprovalID,
		err.ApprovalID,
	)
	g.sendMessage(ctx, recipient, groupID, text)
}

// --- Types ---

// Envelope represents a signal-cli-rest-api received message envelope.
type Envelope struct {
	Envelope EnvelopeData `json:"envelope"`
}

// EnvelopeData contains the envelope fields.
type EnvelopeData struct {
	Source      string       `json:"source"`
	Timestamp   int64        `json:"timestamp"`
	DataMessage *DataMessage `json:"dataMessage,omitempty"`
}

// DataMessage represents a Signal data message.
type DataMessage struct {
	Timestamp int64      `json:"timestamp"`
	Message   string     `json:"message"`
	GroupInfo *GroupInfo `json:"groupInfo,omitempty"`
}

// GroupInfo represents Signal group metadata.
type GroupInfo struct {
	GroupID string `json:"groupId"`
	Type    string `json:"type"`
}

// --- Helpers ---

func (c Config) pollInterval() time.Duration {
	if c.PollIntervalSeconds > 0 {
		return time.Duration(c.PollIntervalSeconds) * time.Second
	}
	return defaultPollInterval
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// splitMessage splits text at logical boundaries to stay within maxLen.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		candidate := remaining[:maxLen]
		splitAt := -1

		// Priority 1: paragraph boundary (double newline).
		if idx := strings.LastIndex(candidate, "\n\n"); idx > 0 {
			splitAt = idx + 1
		}

		// Priority 2: line boundary (single newline).
		if splitAt < 0 {
			if idx := strings.LastIndex(candidate, "\n"); idx > 0 {
				splitAt = idx + 1
			}
		}

		// Priority 3: word boundary (space).
		if splitAt < 0 {
			if idx := strings.LastIndex(candidate, " "); idx > 0 {
				splitAt = idx + 1
			}
		}

		// Priority 4: hard cut.
		if splitAt < 0 {
			splitAt = maxLen
		}

		chunks = append(chunks, remaining[:splitAt])
		remaining = remaining[splitAt:]
	}

	return chunks
}
