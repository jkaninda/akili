package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jkaninda/akili/internal/domain"
)

// WebhookSender sends notifications via HTTP POST to a configured URL.
// Includes SSRF protection: blocks requests to private IP ranges.
type WebhookSender struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewWebhookSender creates a webhook notification sender.
func NewWebhookSender(logger *slog.Logger) *WebhookSender {
	return &WebhookSender{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			// Do not follow redirects — prevents SSRF via redirect to internal hosts.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger: logger,
	}
}

func (s *WebhookSender) Type() string { return "webhook" }

func (s *WebhookSender) Send(ctx context.Context, ch *domain.NotificationChannel, msg *Message) error {
	webhookURL := ch.Config["url"]
	if webhookURL == "" {
		return fmt.Errorf("webhook channel %q missing 'url' in config", ch.Name)
	}

	// SSRF protection: validate the URL host is not a private IP.
	if err := validateWebhookURL(webhookURL); err != nil {
		return fmt.Errorf("webhook URL rejected: %w", err)
	}

	payload := map[string]any{
		"subject":  msg.Subject,
		"body":     msg.Body,
		"metadata": msg.Metadata,
		"channel":  ch.Name,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Akili-Webhook/1.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// validateWebhookURL checks that the URL points to a public host.
// Blocks private IPs, loopback, link-local, and non-HTTP schemes.
func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}

	hostname := u.Hostname()

	// Block obvious loopback names.
	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "127.0.0.1" || lower == "::1" || lower == "0.0.0.0" {
		return fmt.Errorf("loopback addresses not allowed")
	}

	// Resolve and check IP ranges.
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %q: %w", hostname, err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("private/internal IP %s not allowed", ipStr)
		}
	}
	return nil
}
