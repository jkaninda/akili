// Package web implements a secure HTTP fetch tool with SSRF protection.
//
// Security:
//   - Domain allowlist enforced before every request and on every redirect
//   - DNS resolution checked: private/internal IPs blocked (SSRF protection)
//   - Response body capped to prevent OOM
//   - Only GET and HEAD methods allowed
//   - Timeout enforced via context
package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Config configures the web fetch tool restrictions.
type Config struct {
	AllowedDomains   []string // Domains allowed for requests. Empty = deny all.
	MaxResponseBytes int64    // Maximum response body size. 0 = 5 MB default.
	TimeoutSeconds   int      // HTTP timeout. 0 = 10s default.
}

const (
	defaultMaxResponseBytes = 5 << 20 // 5 MB
	defaultTimeoutSeconds   = 10
)

// Tool fetches URLs within the configured allowlist.
type Tool struct {
	config Config
	logger *slog.Logger
}

// NewTool creates a web fetch tool restricted to the given domains.
func NewTool(cfg Config, logger *slog.Logger) *Tool {
	return &Tool{config: cfg, logger: logger}
}

func (t *Tool) Name() string        { return "web_fetch" }
func (t *Tool) Description() string { return "Fetch content from allowed URLs with SSRF protection" }
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":    map[string]any{"type": "string", "description": "The URL to fetch (http or https)"},
			"method": map[string]any{"type": "string", "enum": []string{"GET", "HEAD"}, "description": "HTTP method. Defaults to GET"},
		},
		"required": []string{"url"},
	}
}
func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "web_search", RiskLevel: security.RiskLow}
}
func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *Tool) Validate(params map[string]any) error {
	rawURL, err := requireString(params, "url")
	if err != nil {
		return err
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https schemes allowed, got %q", parsed.Scheme)
	}

	if !IsDomainAllowed(parsed.Hostname(), t.config.AllowedDomains) {
		return fmt.Errorf("domain %q is not in the allowlist", parsed.Hostname())
	}

	method := "GET"
	if m, ok := params["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}
	if method != "GET" && method != "HEAD" {
		return fmt.Errorf("only GET and HEAD methods allowed, got %q", method)
	}

	return nil
}

func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	rawURL, _ := requireString(params, "url")

	method := "GET"
	if m, ok := params["method"].(string); ok && m != "" {
		method = strings.ToUpper(m)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// SSRF check: resolve DNS and block private IPs.
	if err := CheckSSRF(parsed.Hostname()); err != nil {
		return nil, err
	}

	timeout := time.Duration(t.config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = time.Duration(defaultTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// HTTP client that validates redirects.
	client := &http.Client{
		CheckRedirect: t.checkRedirect,
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Akili/1.0")

	t.logger.InfoContext(ctx, "web_fetch executing",
		slog.String("method", method),
		slog.String("url", rawURL),
	)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Cap response body.
	maxBytes := t.config.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	truncated := false
	if int64(len(body)) > maxBytes {
		body = body[:maxBytes]
		truncated = true
	}

	output := tools.TruncateOutput(string(body), tools.MaxOutputBytes)

	return &tools.Result{
		Output:  output,
		Success: resp.StatusCode >= 200 && resp.StatusCode < 400,
		Metadata: map[string]any{
			"status_code": resp.StatusCode,
			"url":         resp.Request.URL.String(),
			"truncated":   truncated,
		},
	}, nil
}

// checkRedirect validates that redirect targets are also in the allowlist
// and don't resolve to private IPs.
func (t *Tool) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects (max 5)")
	}

	host := req.URL.Hostname()
	if !IsDomainAllowed(host, t.config.AllowedDomains) {
		return fmt.Errorf("redirect to disallowed domain %q blocked", host)
	}

	if err := CheckSSRF(host); err != nil {
		return err
	}

	return nil
}

func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string, got %T", key, v)
	}
	if s == "" {
		return "", fmt.Errorf("parameter %s must not be empty", key)
	}
	return s, nil
}
