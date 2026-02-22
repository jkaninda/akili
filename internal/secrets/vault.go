package secrets

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// VaultProvider resolves credential references from HashiCorp Vault KV v2.
// Reference format: "vault://secret/data/myapp/db#password"
//   - vault://        — required prefix
//   - secret/data/... — full KV v2 API path
//   - #password       — optional field selector (omit to get entire data map as JSON)
//
// Uses token-based authentication (VAULT_TOKEN).
// Safe for concurrent use.
type VaultProvider struct {
	address   string
	token     string
	namespace string
	client    *http.Client
}

// NewVaultProvider creates a Vault KV v2 secret provider from config.
//
// Supported config keys:
//   - address:         Vault server URL (overridden by VAULT_ADDR env var)
//   - token:           Vault token (overridden by VAULT_TOKEN env var)
//   - namespace:       Enterprise namespace (overridden by VAULT_NAMESPACE env var)
//   - timeout:         HTTP timeout, e.g. "5s" (default: 5s)
//   - tls_skip_verify: Skip TLS verification, "true"/"false" (default: false)
func NewVaultProvider(cfg map[string]string) (*VaultProvider, error) {
	address := cfg["address"]
	if env := os.Getenv("VAULT_ADDR"); env != "" {
		address = env
	}
	if address == "" {
		return nil, fmt.Errorf("vault address is required (set config key 'address' or VAULT_ADDR)")
	}
	address = strings.TrimRight(address, "/")

	token := cfg["token"]
	if env := os.Getenv("VAULT_TOKEN"); env != "" {
		token = env
	}
	if token == "" {
		return nil, fmt.Errorf("vault token is required (set config key 'token' or VAULT_TOKEN)")
	}

	namespace := cfg["namespace"]
	if env := os.Getenv("VAULT_NAMESPACE"); env != "" {
		namespace = env
	}

	timeout := 5 * time.Second
	if t := cfg["timeout"]; t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return nil, fmt.Errorf("invalid vault timeout %q: %w", t, err)
		}
		timeout = d
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg["tls_skip_verify"] == "true" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &VaultProvider{
		address:   address,
		token:     token,
		namespace: namespace,
		client:    &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

func (p *VaultProvider) Name() string { return "vault" }

func (p *VaultProvider) Resolve(ctx context.Context, credentialRef string) (*Secret, error) {
	const prefix = "vault://"
	if !strings.HasPrefix(credentialRef, prefix) {
		return nil, fmt.Errorf("%w: vault provider only handles vault:// references, got %q",
			ErrSecretNotFound, credentialRef)
	}

	raw := strings.TrimPrefix(credentialRef, prefix)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty vault path", ErrSecretNotFound)
	}

	// Split path and optional field selector.
	path, field, _ := strings.Cut(raw, "#")
	if path == "" {
		return nil, fmt.Errorf("%w: empty vault path", ErrSecretNotFound)
	}

	url := fmt.Sprintf("%s/v1/%s", p.address, path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", p.token)
	if p.namespace != "" {
		req.Header.Set("X-Vault-Namespace", p.namespace)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return nil, fmt.Errorf("reading vault response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: vault path %q not found", ErrSecretNotFound, path)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("vault access denied for path %q (check token permissions)", path)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("vault server error %d for path %q", resp.StatusCode, path)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("vault returned status %d for path %q", resp.StatusCode, path)
	}

	// Parse KV v2 response envelope: { "data": { "data": { ... }, "metadata": { ... } } }
	var envelope struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parsing vault response: %w", err)
	}

	data := envelope.Data.Data
	if data == nil {
		return nil, fmt.Errorf("%w: vault path %q returned no data", ErrSecretNotFound, path)
	}

	metadata := map[string]string{
		"source": "vault",
		"path":   path,
	}

	// Field selector: return a single value.
	if field != "" {
		metadata["field"] = field
		val, ok := data[field]
		if !ok {
			return nil, fmt.Errorf("%w: field %q not found in vault path %q",
				ErrSecretNotFound, field, path)
		}
		str, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf("vault field %q in path %q is not a string", field, path)
		}
		return &Secret{Value: str, Metadata: metadata}, nil
	}

	// No field selector: return entire data map as JSON.
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshaling vault data: %w", err)
	}
	return &Secret{Value: string(jsonBytes), Metadata: metadata}, nil
}
