package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jkaninda/akili/internal/domain"
)

const signalSafeMaxLen = 4096

// SignalSender sends notifications via the signal-cli-rest-api.
// Requires a self-hosted signal-cli-rest-api instance with a registered number.
type SignalSender struct {
	apiURL       string
	senderNumber string
	httpClient   *http.Client
	logger       *slog.Logger
}

// NewSignalSender creates a Signal notification sender.
// apiURL is the base URL of signal-cli-rest-api (e.g. "http://localhost:8080").
// senderNumber is the registered Signal phone number.
func NewSignalSender(apiURL, senderNumber string, logger *slog.Logger) *SignalSender {
	return &SignalSender{
		apiURL:       strings.TrimRight(apiURL, "/"),
		senderNumber: senderNumber,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

func (s *SignalSender) Type() string { return "signal" }

func (s *SignalSender) Send(ctx context.Context, ch *domain.NotificationChannel, msg *Message) error {
	recipient := ch.Config["recipient"]
	if recipient == "" {
		return fmt.Errorf("signal channel %q missing recipient in config", ch.Name)
	}

	// Override API URL from channel config if specified.
	apiURL := s.apiURL
	if chURL := ch.Config["signal_api_url"]; chURL != "" {
		apiURL = strings.TrimRight(chURL, "/")
	}

	// Override sender number from channel config if specified.
	sender := s.senderNumber
	if chSender := ch.Config["sender_number"]; chSender != "" {
		sender = chSender
	}

	text := msg.Body
	if msg.Subject != "" {
		text = fmt.Sprintf("*%s*\n\n%s", msg.Subject, text)
	}

	// Split long messages.
	chunks := splitMessage(text, signalSafeMaxLen)
	for i, chunk := range chunks {
		if len(chunks) > 1 {
			chunk = fmt.Sprintf("[Part %d/%d]\n%s", i+1, len(chunks), chunk)
		}
		if err := s.sendMessage(ctx, apiURL, sender, recipient, chunk); err != nil {
			return fmt.Errorf("sending signal message (part %d/%d): %w", i+1, len(chunks), err)
		}
	}
	return nil
}

func (s *SignalSender) sendMessage(ctx context.Context, apiURL, sender, recipient, text string) error {
	payload := map[string]any{
		"message": text,
		"number":  sender,
	}
	// Group IDs in signal-cli-rest-api are base64-encoded, typically prefixed with "group.".
	if strings.HasPrefix(recipient, "group.") {
		payload["recipients"] = []string{}
		payload["group_id"] = recipient
	} else {
		payload["recipients"] = []string{recipient}
	}

	body, _ := json.Marshal(payload)

	url := apiURL + "/v2/send"
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("signal API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
