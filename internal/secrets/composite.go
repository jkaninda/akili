package secrets

import (
	"context"
	"fmt"
)

// CompositeProvider chains multiple providers and tries each in order.
// The first provider that successfully resolves the reference wins.
type CompositeProvider struct {
	providers []Provider
}

// NewCompositeProvider creates a provider that delegates to the given providers in order.
func NewCompositeProvider(providers ...Provider) *CompositeProvider {
	return &CompositeProvider{providers: providers}
}

func (p *CompositeProvider) Name() string { return "composite" }

func (p *CompositeProvider) Resolve(ctx context.Context, credentialRef string) (*Secret, error) {
	var lastErr error
	for _, provider := range p.providers {
		secret, err := provider.Resolve(ctx, credentialRef)
		if err == nil {
			return secret, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: no provider could resolve %q", ErrSecretNotFound, credentialRef)
}
