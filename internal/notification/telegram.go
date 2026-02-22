package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jkaninda/akili/internal/domain"
)

const (
	telegramAPIBase    = "https://api.telegram.org/bot"
	telegramSafeMaxLen = 4000 // Safe margin under 4096 char limit.
)

// TelegramSender sends notifications via Telegram Bot API.
// Reuses the same bot token as the Telegram gateway.
type TelegramSender struct {
	botToken   string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewTelegramSender creates a Telegram notification sender.
func NewTelegramSender(botToken string, logger *slog.Logger) *TelegramSender {
	return &TelegramSender{
		botToken: botToken,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

func (s *TelegramSender) Type() string { return "telegram" }

func (s *TelegramSender) Send(ctx context.Context, ch *domain.NotificationChannel, msg *Message) error {
	chatID := ch.Config["chat_id"]
	if chatID == "" {
		return fmt.Errorf("telegram channel %q missing chat_id in config", ch.Name)
	}

	text := msg.Body
	if msg.Subject != "" {
		text = fmt.Sprintf("*%s*\n\n%s", escapeMarkdown(msg.Subject), text)
	}

	// Split long messages to respect Telegram 4096 char limit.
	chunks := splitMessage(text, telegramSafeMaxLen)
	for i, chunk := range chunks {
		if len(chunks) > 1 {
			chunk = fmt.Sprintf("[Part %d/%d]\n%s", i+1, len(chunks), chunk)
		}
		if err := s.sendMessage(ctx, chatID, chunk); err != nil {
			return fmt.Errorf("sending telegram message (part %d/%d): %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (s *TelegramSender) sendMessage(ctx context.Context, chatID, text string) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	body, _ := json.Marshal(payload)

	url := telegramAPIBase + s.botToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// splitMessage splits text at logical boundaries to stay within maxLen.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > maxLen {
		// Try to split at a newline boundary.
		cutAt := maxLen
		for i := maxLen - 1; i > maxLen/2; i-- {
			if text[i] == '\n' {
				cutAt = i + 1
				break
			}
		}
		chunks = append(chunks, text[:cutAt])
		text = text[cutAt:]
	}
	if len(text) > 0 {
		chunks = append(chunks, text)
	}
	return chunks
}

// escapeMarkdown escapes special characters for Telegram Markdown v1.
func escapeMarkdown(s string) string {
	replacer := []string{"_", "\\_", "*", "\\*", "[", "\\[", "`", "\\`"}
	result := s
	for i := 0; i < len(replacer); i += 2 {
		result = replaceAll(result, replacer[i], replacer[i+1])
	}
	return result
}

func replaceAll(s, old, new string) string {
	var buf bytes.Buffer
	for i := 0; i < len(s); i++ {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			buf.WriteString(new)
			i += len(old) - 1
		} else {
			buf.WriteByte(s[i])
		}
	}
	return buf.String()
}
