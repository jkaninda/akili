package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jkaninda/akili/internal/domain"
)

const (
	whatsappAPIBase    = "https://graph.facebook.com/v21.0"
	whatsappSafeMaxLen = 4096
)

// WhatsAppSender sends notifications via the WhatsApp Cloud API (Meta Business Platform).
// Requires a WhatsApp Business account and a permanent access token.
type WhatsAppSender struct {
	accessToken string
	httpClient  *http.Client
	logger      *slog.Logger
}

// NewWhatsAppSender creates a WhatsApp notification sender.
// accessToken is the WhatsApp Business API permanent access token.
func NewWhatsAppSender(accessToken string, logger *slog.Logger) *WhatsAppSender {
	return &WhatsAppSender{
		accessToken: accessToken,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

func (s *WhatsAppSender) Type() string { return "whatsapp" }

func (s *WhatsAppSender) Send(ctx context.Context, ch *domain.NotificationChannel, msg *Message) error {
	phoneNumberID := ch.Config["phone_number_id"]
	if phoneNumberID == "" {
		return fmt.Errorf("whatsapp channel %q missing phone_number_id in config", ch.Name)
	}
	recipient := ch.Config["recipient"]
	if recipient == "" {
		return fmt.Errorf("whatsapp channel %q missing recipient in config", ch.Name)
	}

	// Resolve access token: CredentialRef (env var name) takes precedence.
	token := s.accessToken
	if ch.CredentialRef != "" {
		if envToken := os.Getenv(ch.CredentialRef); envToken != "" {
			token = envToken
		}
	}
	if token == "" {
		return fmt.Errorf("whatsapp channel %q: no access token available", ch.Name)
	}

	text := msg.Body
	if msg.Subject != "" {
		text = fmt.Sprintf("*%s*\n\n%s", msg.Subject, text)
	}

	// Split long messages to respect WhatsApp limit.
	chunks := splitMessage(text, whatsappSafeMaxLen)
	for i, chunk := range chunks {
		if len(chunks) > 1 {
			chunk = fmt.Sprintf("[Part %d/%d]\n%s", i+1, len(chunks), chunk)
		}
		if err := s.sendMessage(ctx, phoneNumberID, recipient, chunk, token); err != nil {
			return fmt.Errorf("sending whatsapp message (part %d/%d): %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (s *WhatsAppSender) sendMessage(ctx context.Context, phoneNumberID, recipient, text, token string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                recipient,
		"type":              "text",
		"text": map[string]string{
			"body": text,
		},
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/%s/messages", whatsappAPIBase, phoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("whatsapp API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
