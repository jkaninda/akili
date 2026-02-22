package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EnvProvider resolves credential references from environment variables.
// Reference format: "env://VARIABLE_NAME".
type EnvProvider struct{}

// NewEnvProvider creates an environment variable-based secret provider.
func NewEnvProvider() *EnvProvider { return &EnvProvider{} }

func (p *EnvProvider) Name() string { return "env" }

func (p *EnvProvider) Resolve(_ context.Context, credentialRef string) (*Secret, error) {
	const prefix = "env://"
	if !strings.HasPrefix(credentialRef, prefix) {
		return nil, fmt.Errorf("%w: env provider only handles env:// references, got %q",
			ErrSecretNotFound, credentialRef)
	}
	envVar := strings.TrimPrefix(credentialRef, prefix)
	if envVar == "" {
		return nil, fmt.Errorf("%w: empty environment variable name", ErrSecretNotFound)
	}
	value := os.Getenv(envVar)
	if value == "" {
		return nil, fmt.Errorf("%w: environment variable %q is not set or empty",
			ErrSecretNotFound, envVar)
	}
	return &Secret{
		Value:    value,
		Metadata: map[string]string{"source": "env", "variable": envVar},
	}, nil
}
