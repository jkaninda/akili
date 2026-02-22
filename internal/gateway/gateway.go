// Package gateway defines the interface for user-facing entry points.
package gateway

import "context"

// Gateway is a user-facing interface (CLI, Slack, HTTP, Telegram, etc.).
type Gateway interface {
	// Start launches the gateway's event loop and blocks until the gateway
	// exits or the context is canceled. Returns an error only on failure.
	Start(ctx context.Context) error

	// Stop performs graceful shutdown. The context carries a deadline
	// for the grace period. In-flight requests should drain before returning.
	Stop(ctx context.Context) error
}
