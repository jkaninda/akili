// Package telegram implements a Telegram Bot gateway for Akili using
// long polling or webhook mode.
//
// Security:
//   - User allowlist: only explicitly listed Telegram user IDs can interact (default-deny)
//   - User mapping: Telegram user IDs mapped to Akili user IDs
//   - Bot token from TELEGRAM_BOT_TOKEN env var, never logged or stored in config
//   - Webhook path derived from bot token hash (prevents unauthorized POSTs)
//   - Per-user rate limiting
//   - All requests logged with correlation IDs
package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/ratelimit"
)

const (
	defaultPollTimeout    = 30
	maxUpdateSize         = 256 << 10 // 256 KB
	telegramMaxMessageLen = 4096
	telegramSafeMaxLen    = 4000 // Safe margin for unicode/encoding overhead.
)

// Config configures the Telegram gateway.
type Config struct {
	BotToken     string            // From TELEGRAM_BOT_TOKEN env var.
	WebhookURL   string            // If set, use webhook mode. If empty, use long polling.
	ListenAddr   string            // For webhook mode.
	AllowedUsers []int64           // Telegram user IDs allowed to interact. Empty = deny all.
	UserMapping  map[string]string // Telegram user ID (string) → Akili user ID.
	PollTimeout  int               // Long poll timeout in seconds. 0 = 30s default.
}

// Gateway is the Telegram gateway.
type Gateway struct {
	config      Config
	agent       agent.Agent
	approvalMgr approval.ApprovalManager
	limiter     *ratelimit.Limiter
	logger      *slog.Logger
	httpClient  *http.Client
	server      *http.Server // nil in polling mode
	cancel      context.CancelFunc
	allowed     map[int64]bool // pre-computed allowlist
}

// NewGateway creates a Telegram gateway.
func NewGateway(cfg Config, a agent.Agent, am approval.ApprovalManager, rl *ratelimit.Limiter, logger *slog.Logger) *Gateway {
	allowed := make(map[int64]bool, len(cfg.AllowedUsers))
	for _, uid := range cfg.AllowedUsers {
		allowed[uid] = true
	}
	return &Gateway{
		config:      cfg,
		agent:       a,
		approvalMgr: am,
		limiter:     rl,
		logger:      logger,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.pollTimeout()+10) * time.Second,
		},
		allowed: allowed,
	}
}

// Start launches the gateway in webhook or long-polling mode and blocks.
func (g *Gateway) Start(ctx context.Context) error {
	ctx, g.cancel = context.WithCancel(ctx)

	if g.config.WebhookURL != "" {
		return g.startWebhook(ctx)
	}
	return g.startPolling(ctx)
}

// Stop gracefully shuts down the gateway.
func (g *Gateway) Stop(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	if g.server != nil {
		g.logger.Info("telegram gateway stopping webhook server")
		return g.server.Shutdown(ctx)
	}
	g.logger.Info("telegram gateway stopping poller")
	return nil
}

// --- Long Polling ---

func (g *Gateway) startPolling(ctx context.Context) error {
	g.logger.Info("telegram gateway starting long polling",
		slog.Int("timeout", g.config.pollTimeout()),
	)

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := g.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			g.logger.Error("telegram getUpdates failed", slog.String("error", err.Error()))
			time.Sleep(2 * time.Second)
			continue
		}

		for _, u := range updates {
			g.processUpdate(ctx, &u)
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
		}
	}
}

func (g *Gateway) getUpdates(ctx context.Context, offset int64) ([]Update, error) {
	params := map[string]any{
		"offset":  offset,
		"timeout": g.config.pollTimeout(),
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL("getUpdates"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpdateSize)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding updates: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram API returned ok=false")
	}
	return result.Result, nil
}

// --- Webhook ---

func (g *Gateway) startWebhook(ctx context.Context) error {
	// Use a hash of the bot token as the webhook path to prevent unauthorized POSTs.
	secretPath := "/" + g.webhookSecret()

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+secretPath, g.handleWebhook)

	g.server = &http.Server{
		Addr:              g.config.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	g.logger.Info("telegram gateway starting webhook",
		slog.String("addr", g.config.ListenAddr),
	)

	err := g.server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (g *Gateway) handleWebhook(w http.ResponseWriter, r *http.Request) {
	var update Update
	if err := json.NewDecoder(io.LimitReader(r.Body, maxUpdateSize)).Decode(&update); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	g.processUpdate(r.Context(), &update)
	w.WriteHeader(http.StatusOK)
}

func (g *Gateway) webhookSecret() string {
	h := sha256.Sum256([]byte(g.config.BotToken))
	return hex.EncodeToString(h[:16]) // 32-char hex path
}

// --- Update Processing ---

func (g *Gateway) processUpdate(ctx context.Context, update *Update) {
	if update.CallbackQuery != nil {
		g.handleCallback(ctx, update.CallbackQuery)
		return
	}
	if update.Message != nil {
		g.handleMessage(ctx, update.Message)
	}
}

func (g *Gateway) handleMessage(ctx context.Context, msg *Message) {
	if msg.From == nil || msg.Text == "" {
		return
	}

	telegramUserID := msg.From.ID
	chatID := msg.Chat.ID

	// User allowlist check (default-deny).
	if !g.allowed[telegramUserID] {
		g.logger.Warn("telegram user not in allowlist",
			slog.Int64("telegram_user_id", telegramUserID),
		)
		g.sendHTML(ctx, chatID, "You are not authorized to use Akili.")
		return
	}

	// Map to Akili user ID.
	userID, ok := g.config.UserMapping[strconv.FormatInt(telegramUserID, 10)]
	if !ok {
		g.logger.Warn("telegram user not mapped",
			slog.Int64("telegram_user_id", telegramUserID),
		)
		g.sendHTML(ctx, chatID, "Your Telegram account is not mapped to an Akili user.")
		return
	}

	// Rate limit.
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			g.sendHTML(ctx, chatID, "Rate limit exceeded. Please wait before trying again.")
			return
		}
	}

	correlationID := newCorrelationID()

	g.logger.Info("telegram message",
		slog.String("user_id", userID),
		slog.Int64("telegram_user_id", telegramUserID),
		slog.String("correlation_id", correlationID),
	)

	// Strip /start and bot command prefixes.
	text := msg.Text
	if strings.HasPrefix(text, "/start") {
		g.sendHTML(ctx, chatID,
			"\U0001F6E1 <b>Akili</b> — Security-First AI Agent\n\n"+
				"Send a message to get started.\n"+
				"All actions follow <b>default-deny</b> security and require explicit permission.")
		return
	}

	// Derive deterministic conversation ID from (userID, chatID).
	conversationID := uuid.NewSHA1(uuid.NameSpaceDNS,
		[]byte(fmt.Sprintf("telegram:%d:%d", telegramUserID, chatID))).String()

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
			g.sendApprovalKeyboard(ctx, chatID, approvalErr)
			return
		}

		g.logger.Error("agent processing failed",
			slog.String("correlation_id", correlationID),
			slog.String("error", err.Error()),
		)
		g.sendHTML(ctx, chatID, "\u2757 Error processing your request.")
		return
	}

	// Check for approval requests in response.
	for _, ar := range resp.ApprovalRequests {
		g.sendApprovalKeyboard(ctx, chatID, &agent.ErrApprovalPending{
			ApprovalID: ar.ApprovalID,
			ToolName:   ar.ToolName,
			ActionName: ar.ActionName,
			RiskLevel:  ar.RiskLevel,
			UserID:     ar.UserID,
		})
	}

	g.sendMarkdown(ctx, chatID, resp.Message)
}

func (g *Gateway) handleCallback(ctx context.Context, cb *CallbackQuery) {
	if cb.From == nil || cb.Data == "" {
		return
	}

	telegramUserID := cb.From.ID

	// Allowlist check.
	if !g.allowed[telegramUserID] {
		g.answerCallback(ctx, cb.ID, "Not authorized.")
		return
	}

	// Map user.
	approverID, ok := g.config.UserMapping[strconv.FormatInt(telegramUserID, 10)]
	if !ok {
		g.answerCallback(ctx, cb.ID, "User not mapped.")
		return
	}

	// Parse callback data: "approve:<id>" or "deny:<id>"
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		g.answerCallback(ctx, cb.ID, "Invalid action.")
		return
	}

	decision := parts[0]
	approvalID := parts[1]

	g.logger.Info("telegram callback",
		slog.String("approver_id", approverID),
		slog.String("approval_id", approvalID),
		slog.String("decision", decision),
	)

	if g.approvalMgr == nil {
		g.answerCallback(ctx, cb.ID, "Approval manager not configured.")
		return
	}

	chatID := int64(0)
	if cb.Message != nil {
		chatID = cb.Message.Chat.ID
	}

	if decision == "deny" {
		if err := g.approvalMgr.Deny(ctx, approvalID, approverID); err != nil {
			g.answerCallback(ctx, cb.ID, fmt.Sprintf("Deny failed: %v", err))
			return
		}
		g.answerCallback(ctx, cb.ID, "\u274C Denied.")
		if chatID != 0 {
			g.sendHTML(ctx, chatID, fmt.Sprintf("\u274C <b>Denied</b> by %s.", escapeHTML(approverID)))
		}
		return
	}

	// Approve and resume.
	if err := g.approvalMgr.Approve(ctx, approvalID, approverID); err != nil {
		g.answerCallback(ctx, cb.ID, fmt.Sprintf("Approval failed: %v", err))
		return
	}

	g.answerCallback(ctx, cb.ID, "\u2705 Approved. Executing...")

	result, err := g.agent.ResumeWithApproval(ctx, approvalID)
	if err != nil {
		if chatID != 0 {
			g.sendHTML(ctx, chatID, fmt.Sprintf("\u2757 Execution after approval failed: %s", escapeHTML(err.Error())))
		}
		return
	}

	if chatID != 0 {
		g.sendHTML(ctx, chatID, fmt.Sprintf("\u2705 <b>Approved</b> by %s.", escapeHTML(approverID)))
		g.sendMarkdown(ctx, chatID, result.Output)
	}
}

// --- Telegram API ---

// sendMarkdown converts agent Markdown output to Telegram HTML, splits into
// chunks, and sends each chunk with HTML parse mode.
func (g *Gateway) sendMarkdown(ctx context.Context, chatID int64, text string) {
	if text == "" {
		return
	}
	chunks := splitMessage(text, telegramSafeMaxLen)
	for _, chunk := range chunks {
		html := markdownToTelegramHTML(chunk)
		g.callAPI(ctx, "sendMessage", map[string]any{
			"chat_id":                  chatID,
			"text":                     html,
			"parse_mode":               "HTML",
			"disable_web_page_preview": true,
		})
	}
}

// sendHTML sends pre-formatted HTML. Used for system messages that are
// already authored in HTML (approvals, errors, status).
func (g *Gateway) sendHTML(ctx context.Context, chatID int64, html string) {
	chunks := splitMessage(html, telegramSafeMaxLen)
	for _, chunk := range chunks {
		g.callAPI(ctx, "sendMessage", map[string]any{
			"chat_id":                  chatID,
			"text":                     chunk,
			"parse_mode":               "HTML",
			"disable_web_page_preview": true,
		})
	}
}

func (g *Gateway) sendApprovalKeyboard(ctx context.Context, chatID int64, err *agent.ErrApprovalPending) {
	riskEmoji := "\u26A0\uFE0F" // ⚠️
	if err.RiskLevel == "critical" {
		riskEmoji = "\U0001F6A8" // 🚨
	}

	text := fmt.Sprintf(
		"%s <b>Approval Required</b>\n\n"+
			"\U0001F527 <b>Tool:</b> <code>%s</code>\n"+
			"\U0001F3AF <b>Action:</b> %s\n"+
			"\u26A1 <b>Risk:</b> %s\n"+
			"\U0001F464 <b>Requested by:</b> %s\n\n"+
			"<i>Approval ID:</i> <code>%s</code>",
		riskEmoji,
		escapeHTML(err.ToolName),
		escapeHTML(err.ActionName),
		escapeHTML(err.RiskLevel),
		escapeHTML(err.UserID),
		escapeHTML(err.ApprovalID),
	)

	g.callAPI(ctx, "sendMessage", map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "\u2705 Approve", CallbackData: "approve:" + err.ApprovalID},
					{Text: "\u274C Deny", CallbackData: "deny:" + err.ApprovalID},
				},
			},
		},
	})
}

func (g *Gateway) answerCallback(ctx context.Context, callbackID, text string) {
	g.callAPI(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	})
}

func (g *Gateway) callAPI(ctx context.Context, method string, params map[string]any) {
	body, err := json.Marshal(params)
	if err != nil {
		g.logger.Error("telegram marshal error", slog.String("error", err.Error()))
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL(method), bytes.NewReader(body))
	if err != nil {
		g.logger.Error("telegram request error", slog.String("error", err.Error()))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		g.logger.Error("telegram api error",
			slog.String("method", method),
			slog.String("error", err.Error()),
		)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		g.logger.Error("telegram api non-200",
			slog.String("method", method),
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBody)),
		)
	}
}

func (g *Gateway) apiURL(method string) string {
	return fmt.Sprintf("https://api.telegram.org/bot%s/%s", g.config.BotToken, method)
}

// --- Types ---

// Update represents a Telegram Bot API update.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Message represents a Telegram message.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// CallbackQuery represents an inline keyboard button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

// User represents a Telegram user.
type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat represents a Telegram chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// InlineKeyboardMarkup represents inline keyboard buttons.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton represents a single inline keyboard button.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// --- Helpers ---

func (c Config) pollTimeout() int {
	if c.PollTimeout > 0 {
		return c.PollTimeout
	}
	return defaultPollTimeout
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// escapeHTML escapes characters that are special in Telegram's HTML parse mode.
func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// --- Markdown to Telegram HTML ---

// markdownToTelegramHTML converts common Markdown patterns from LLM output
// to Telegram-compatible HTML. Handles code blocks, inline code, bold,
// italic, and headers. All other text is HTML-escaped.
func markdownToTelegramHTML(text string) string {
	var out strings.Builder
	lines := strings.Split(text, "\n")
	inCodeBlock := false
	firstCodeLine := true

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Code block toggle.
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				lang := strings.TrimPrefix(trimmed, "```")
				lang = strings.TrimSpace(lang)
				if lang != "" {
					out.WriteString("<pre><code class=\"language-" + escapeHTML(lang) + "\">")
				} else {
					out.WriteString("<pre><code>")
				}
				inCodeBlock = true
				firstCodeLine = true
				continue
			}
			out.WriteString("</code></pre>")
			inCodeBlock = false
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			continue
		}

		if inCodeBlock {
			if !firstCodeLine {
				out.WriteByte('\n')
			}
			out.WriteString(escapeHTML(line))
			firstCodeLine = false
			continue
		}

		// Non-code line.
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(formatLine(line))
	}

	// Close unclosed code block.
	if inCodeBlock {
		out.WriteString("</code></pre>")
	}

	return out.String()
}

// formatLine converts a single non-code-block line of Markdown to HTML.
func formatLine(line string) string {
	// Headers → bold.
	if len(line) > 0 && line[0] == '#' {
		if spaceIdx := strings.IndexByte(line, ' '); spaceIdx > 0 && spaceIdx <= 6 {
			allHash := true
			for j := 0; j < spaceIdx; j++ {
				if line[j] != '#' {
					allHash = false
					break
				}
			}
			if allHash {
				return "<b>" + escapeHTML(strings.TrimSpace(line[spaceIdx+1:])) + "</b>"
			}
		}
	}

	return formatInline(line)
}

// formatInline converts inline Markdown (bold, italic, inline code) to HTML.
// Processes left-to-right: backtick spans take priority over bold/italic.
func formatInline(line string) string {
	var out strings.Builder
	out.Grow(len(line) + 32)
	i := 0

	for i < len(line) {
		// Inline code: `...`
		if line[i] == '`' {
			if end := strings.IndexByte(line[i+1:], '`'); end >= 0 {
				out.WriteString("<code>")
				out.WriteString(escapeHTML(line[i+1 : i+1+end]))
				out.WriteString("</code>")
				i = i + 1 + end + 1
				continue
			}
		}

		// Bold: **...**
		if i+1 < len(line) && line[i] == '*' && line[i+1] == '*' {
			if end := strings.Index(line[i+2:], "**"); end >= 0 && end > 0 {
				out.WriteString("<b>")
				out.WriteString(escapeHTML(line[i+2 : i+2+end]))
				out.WriteString("</b>")
				i = i + 2 + end + 2
				continue
			}
		}

		// Italic: *...* (single asterisk, not double)
		if line[i] == '*' && (i+1 >= len(line) || line[i+1] != '*') {
			if end := strings.IndexByte(line[i+1:], '*'); end > 0 {
				// Verify the closing * is not part of a ** pair.
				closeIdx := i + 1 + end
				if closeIdx+1 >= len(line) || line[closeIdx+1] != '*' {
					out.WriteString("<i>")
					out.WriteString(escapeHTML(line[i+1 : closeIdx]))
					out.WriteString("</i>")
					i = closeIdx + 1
					continue
				}
			}
		}

		// Regular character — HTML-escape.
		switch line[i] {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		default:
			out.WriteByte(line[i])
		}
		i++
	}

	return out.String()
}

// --- Message Splitting ---

// splitMessage splits text into chunks that fit within Telegram's message limit.
// It splits at paragraph boundaries, then line boundaries, then word boundaries,
// and tracks code fences (```) so they are properly closed/reopened across chunks.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	remaining := text
	inCodeBlock := false
	codeFenceLang := "" // language tag from opening fence, e.g. "go" from ```go

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		// Find the best split point within maxLen.
		candidate := remaining[:maxLen]
		splitAt := -1

		// Priority 1: paragraph boundary (double newline).
		if idx := strings.LastIndex(candidate, "\n\n"); idx > 0 {
			splitAt = idx + 1 // Keep first newline in this chunk.
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

		chunk := remaining[:splitAt]
		remaining = remaining[splitAt:]

		// Track code fences in this chunk. Count occurrences of ``` to determine
		// whether we end inside a code block.
		fenceCount := 0
		searchPos := 0
		for {
			idx := strings.Index(chunk[searchPos:], "```")
			if idx < 0 {
				break
			}
			absIdx := searchPos + idx
			fenceCount++
			if fenceCount%2 == 1 && !inCodeBlock {
				// Opening fence — capture language tag (on same line after ```).
				afterFence := chunk[absIdx+3:]
				if nlIdx := strings.Index(afterFence, "\n"); nlIdx >= 0 {
					codeFenceLang = strings.TrimSpace(afterFence[:nlIdx])
				} else {
					codeFenceLang = strings.TrimSpace(afterFence)
				}
			}
			searchPos = absIdx + 3
		}

		if fenceCount%2 == 1 {
			inCodeBlock = !inCodeBlock
		}

		// If we're inside a code block at the end of this chunk, close it
		// and re-open it at the start of the next chunk.
		if inCodeBlock {
			chunk += "\n```"
			if codeFenceLang != "" {
				remaining = "```" + codeFenceLang + "\n" + remaining
			} else {
				remaining = "```\n" + remaining
			}
		}

		chunks = append(chunks, chunk)
	}

	return chunks
}
