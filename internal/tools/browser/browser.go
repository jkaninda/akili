// Package browser implements a safe web navigation tool with SSRF protection.
// It extends the basic web_fetch tool with content extraction actions
// (text, links, CSS selectors) while maintaining the same security model.
package browser

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
	"github.com/jkaninda/akili/internal/tools/web"
)

const (
	defaultMaxResponseBytes      = 10 << 20 // 10 MB
	defaultTimeoutSeconds        = 30
	defaultMaxNavigationsPerCall = 5
)

// Config configures the browser tool restrictions.
type Config struct {
	AllowedDomains        []string
	MaxResponseBytes      int64
	TimeoutSeconds        int
	MaxNavigationsPerCall int
}

// Tool provides safe web navigation and content retrieval.
type Tool struct {
	config Config
	logger *slog.Logger
}

// NewTool creates a browser tool with the given restrictions.
func NewTool(cfg Config, logger *slog.Logger) *Tool {
	return &Tool{config: cfg, logger: logger}
}

func (t *Tool) Name() string { return "browser" }

func (t *Tool) Description() string {
	return "Navigate web pages and extract content (text, links) with SSRF protection and domain allowlist. " +
		"Supports CSS-like content extraction for targeted data retrieval."
}

func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "URL to navigate to (https only)",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"get", "extract_text", "extract_links", "screenshot_text"},
				"description": "Action to perform. Default: get",
			},
			"selector": map[string]any{
				"type":        "string",
				"description": "Simple tag selector to filter content (e.g., 'title', 'h1', 'p', 'a'). Optional.",
			},
		},
		"required": []string{"url"},
	}
}

func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "browser_navigate", RiskLevel: security.RiskMedium}
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

	if parsed.Scheme != "https" {
		return fmt.Errorf("only https scheme allowed, got %q", parsed.Scheme)
	}

	if !web.IsDomainAllowed(parsed.Hostname(), t.config.AllowedDomains) {
		return fmt.Errorf("domain %q is not in the browser allowlist", parsed.Hostname())
	}

	if action, ok := params["action"].(string); ok && action != "" {
		switch action {
		case "get", "extract_text", "extract_links", "screenshot_text":
			// valid
		default:
			return fmt.Errorf("unknown action %q", action)
		}
	}

	return nil
}

func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	rawURL, _ := requireString(params, "url")
	action := "get"
	if a, ok := params["action"].(string); ok && a != "" {
		action = a
	}
	selector, _ := params["selector"].(string)

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// SSRF check: resolve DNS and block private IPs.
	if err := web.CheckSSRF(parsed.Hostname()); err != nil {
		return nil, err
	}

	timeout := time.Duration(t.config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = time.Duration(defaultTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Redirect validation with SSRF + domain checks.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects (max 5)")
			}
			host := req.URL.Hostname()
			if !web.IsDomainAllowed(host, t.config.AllowedDomains) {
				return fmt.Errorf("redirect to disallowed domain %q blocked", host)
			}
			return web.CheckSSRF(host)
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Akili/1.0 Browser")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	t.logger.DebugContext(ctx, "browser navigating",
		slog.String("url", rawURL),
		slog.String("action", action),
	)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

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

	content := string(body)

	var output string
	switch action {
	case "get":
		output = content
	case "extract_text":
		output = extractText(content, selector)
	case "extract_links":
		output = extractLinks(content)
	case "screenshot_text":
		output = extractVisibleText(content)
	default:
		output = content
	}

	output = tools.TruncateOutput(output, tools.MaxOutputBytes)

	return &tools.Result{
		Output:  output,
		Success: resp.StatusCode >= 200 && resp.StatusCode < 400,
		Metadata: map[string]any{
			"status_code":  resp.StatusCode,
			"url":          resp.Request.URL.String(),
			"content_type": resp.Header.Get("Content-Type"),
			"truncated":    truncated,
			"action":       action,
		},
	}, nil
}

// extractText strips HTML tags and returns clean text.
// If selector is provided, only content within matching tags is returned.
func extractText(html, selector string) string {
	if selector != "" {
		html = extractByTag(html, selector)
	}
	return stripTags(html)
}

// extractLinks finds all <a href="..."> links and returns them formatted.
func extractLinks(html string) string {
	re := regexp.MustCompile(`<a\s[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, -1)

	var sb strings.Builder
	for _, m := range matches {
		if len(m) >= 3 {
			text := stripTags(m[2])
			sb.WriteString(fmt.Sprintf("[%s](%s)\n", strings.TrimSpace(text), m[1]))
		}
	}
	if sb.Len() == 0 {
		return "No links found."
	}
	return sb.String()
}

// extractVisibleText strips all tags, scripts, styles, and returns visible text.
func extractVisibleText(html string) string {
	// Remove script and style blocks.
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	return stripTags(html)
}

// extractByTag extracts content within matching HTML tags.
func extractByTag(html, tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	re := regexp.MustCompile(fmt.Sprintf(`(?is)<%s[^>]*>(.*?)</%s>`, regexp.QuoteMeta(tag), regexp.QuoteMeta(tag)))
	matches := re.FindAllStringSubmatch(html, -1)

	var sb strings.Builder
	for _, m := range matches {
		if len(m) >= 2 {
			sb.WriteString(m[1])
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// stripTags removes all HTML tags from a string and cleans up whitespace.
func stripTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, " ")
	// Collapse whitespace.
	spaceRe := regexp.MustCompile(`\s+`)
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
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
