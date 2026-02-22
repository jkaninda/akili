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

const slackPostMessageURL = "https://slack.com/api/chat.postMessage"

// SlackSender sends notifications via Slack Web API.
// Reuses the same bot token as the Slack gateway.
type SlackSender struct {
	botToken   string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewSlackSender creates a Slack notification sender.
func NewSlackSender(botToken string, logger *slog.Logger) *SlackSender {
	return &SlackSender{
		botToken: botToken,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

func (s *SlackSender) Type() string { return "slack" }

func (s *SlackSender) Send(ctx context.Context, ch *domain.NotificationChannel, msg *Message) error {
	channelID := ch.Config["channel_id"]
	if channelID == "" {
		return fmt.Errorf("slack channel %q missing channel_id in config", ch.Name)
	}

	text := msg.Body
	if msg.Subject != "" {
		text = fmt.Sprintf("*%s*\n%s", msg.Subject, text)
	}

	payload := map[string]any{
		"channel": channelID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackPostMessageURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+s.botToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack API returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Slack returns 200 even on errors — check the "ok" field.
	var slackResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &slackResp); err == nil && !slackResp.OK {
		return fmt.Errorf("slack API error: %s", slackResp.Error)
	}

	return nil
}
