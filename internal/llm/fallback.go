package llm

import (
	"context"
	"fmt"
	"log/slog"
)

// FallbackProvider wraps multiple providers and tries them in order.
// If the primary provider fails, subsequent providers are tried until
// one succeeds or all have failed.
type FallbackProvider struct {
	providers []Provider
	logger    *slog.Logger
}

// NewFallbackProvider creates a provider that tries each provider in order.
// At least one provider is required.
func NewFallbackProvider(providers []Provider, logger *slog.Logger) *FallbackProvider {
	if len(providers) == 0 {
		panic("FallbackProvider requires at least one provider")
	}
	return &FallbackProvider{
		providers: providers,
		logger:    logger,
	}
}

// SendMessage tries each provider in order, returning the first successful response.
func (f *FallbackProvider) SendMessage(ctx context.Context, req *Request) (*Response, error) {
	var lastErr error
	for i, p := range f.providers {
		resp, err := p.SendMessage(ctx, req)
		if err == nil {
			if i > 0 {
				f.logger.InfoContext(ctx, "provider fallback succeeded",
					slog.String("provider", p.Name()),
					slog.Int("attempt", i+1),
				)
			}
			return resp, nil
		}
		lastErr = err
		f.logger.WarnContext(ctx, "provider failed, trying next",
			slog.String("provider", p.Name()),
			slog.String("error", err.Error()),
			slog.Int("attempt", i+1),
			slog.Int("remaining", len(f.providers)-i-1),
		)
	}
	return nil, fmt.Errorf("all %d providers failed, last error: %w", len(f.providers), lastErr)
}

// Name returns a composite name indicating fallback configuration.
func (f *FallbackProvider) Name() string {
	return f.providers[0].Name() + "+fallback"
}
