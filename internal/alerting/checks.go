package alerting

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RunCheck dispatches to the appropriate check implementation based on checkType.
func RunCheck(ctx context.Context, checkType string, config map[string]string) (status, message string, err error) {
	switch checkType {
	case "http_status":
		return HTTPCheck(ctx, config)
	case "command":
		// Command checks require a sandbox, which is not available here.
		// They are executed via the workflow engine instead.
		return "error", "command checks must be executed via workflow engine", fmt.Errorf("command check type not supported in direct evaluation")
	default:
		return "error", fmt.Sprintf("unknown check type: %s", checkType), fmt.Errorf("unknown check type: %s", checkType)
	}
}

// HTTPCheck performs an HTTP GET and checks the response status code.
// Config keys:
//   - "url" (required): the URL to check
//   - "expected_status" (optional): expected HTTP status code (default: 200)
//   - "timeout_seconds" (optional): request timeout (default: 10)
func HTTPCheck(ctx context.Context, config map[string]string) (status, message string, err error) {
	rawURL := config["url"]
	if rawURL == "" {
		return "error", "missing url in check config", fmt.Errorf("missing url")
	}

	// SSRF protection: block private IPs.
	if err := validateCheckURL(rawURL); err != nil {
		return "error", fmt.Sprintf("URL rejected: %s", err), err
	}

	expectedStatus := 200
	if v := config["expected_status"]; v != "" {
		parsed, parseErr := strconv.Atoi(v)
		if parseErr != nil {
			return "error", "invalid expected_status", fmt.Errorf("invalid expected_status: %w", parseErr)
		}
		expectedStatus = parsed
	}

	timeoutSec := 10
	if v := config["timeout_seconds"]; v != "" {
		parsed, parseErr := strconv.Atoi(v)
		if parseErr == nil && parsed > 0 {
			timeoutSec = parsed
		}
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "error", fmt.Sprintf("creating request: %s", err), err
	}
	req.Header.Set("User-Agent", "Akili-HealthCheck/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "alert", fmt.Sprintf("request failed: %s", err), nil
	}
	defer resp.Body.Close()
	// Drain body to allow connection reuse.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	if resp.StatusCode == expectedStatus {
		return "ok", fmt.Sprintf("HTTP %d (expected %d)", resp.StatusCode, expectedStatus), nil
	}
	return "alert", fmt.Sprintf("HTTP %d (expected %d)", resp.StatusCode, expectedStatus), nil
}

// validateCheckURL blocks requests to private/internal IP addresses.
func validateCheckURL(rawURL string) error {
	// Extract hostname from URL.
	hostname := rawURL
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		hostname = rawURL[idx+3:]
	}
	if idx := strings.IndexAny(hostname, ":/"); idx >= 0 {
		hostname = hostname[:idx]
	}

	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "127.0.0.1" || lower == "::1" || lower == "0.0.0.0" {
		return fmt.Errorf("loopback addresses not allowed")
	}

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
