package web

import (
	"fmt"
	"net"
	"strings"
)

// CheckSSRF resolves the host to IP addresses and blocks private/internal ranges.
// Exported for reuse by the browser tool.
func CheckSSRF(host string) error {
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed for %q: %w", host, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("invalid IP %q for host %q", ipStr, host)
		}
		if IsPrivateIP(ip) {
			return fmt.Errorf("SSRF blocked: host %q resolves to private IP %s", host, ipStr)
		}
	}

	return nil
}

// IsPrivateIP checks if an IP is in a private, loopback, or link-local range.
// Exported for reuse by the browser tool.
func IsPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}

	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"169.254.0.0/16"},
	}
	for _, r := range privateRanges {
		_, cidr, _ := net.ParseCIDR(r.network)
		if cidr != nil && cidr.Contains(ip) {
			return true
		}
	}

	// Private IPv6 (fc00::/7).
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return true
	}

	return false
}

// IsDomainAllowed checks if the host is in the given allowlist.
// Exported for reuse by the browser tool.
func IsDomainAllowed(host string, allowedDomains []string) bool {
	host = strings.ToLower(host)
	for _, d := range allowedDomains {
		if strings.ToLower(d) == host {
			return true
		}
	}
	return false
}
