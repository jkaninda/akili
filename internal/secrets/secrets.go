// Package secrets defines the SecretProvider interface for credential resolution.
// Implementations are backend-specific (env vars, HashiCorp Vault, AWS Secrets Manager).
// The LLM NEVER receives raw secret material — credentials are resolved at the tool
// execution layer and injected into MCP calls without passing through the LLM.
package secrets

import (
	"context"
	"fmt"
)

// Secret holds resolved credential material.
// This type MUST NOT be serialized to JSON or included in LLM responses.
type Secret struct {
	Value    string            // The raw secret value (password, SSH key, token).
	Metadata map[string]string // Backend-specific metadata (e.g., lease_id, version).
}

// Provider resolves opaque credential references into secret material.
// Implementations must be safe for concurrent use.
type Provider interface {
	// Resolve takes a credential reference (e.g., "env://MY_KEY" or "vault://ssh/prod")
	// and returns the raw secret. Returns ErrSecretNotFound if the reference cannot be resolved.
	Resolve(ctx context.Context, credentialRef string) (*Secret, error)

	// Name returns the provider identifier for logging (never includes secrets).
	Name() string
}

// ErrSecretNotFound is returned when a credential reference cannot be resolved.
var ErrSecretNotFound = fmt.Errorf("secret not found")
