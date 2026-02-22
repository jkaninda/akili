package security

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// AuditLogger writes audit events as append-only JSONL.
// Each event is a single JSON line followed by a newline.
// Thread-safe: multiple goroutines can log concurrently.
type AuditLogger struct {
	mu     sync.Mutex
	file   *os.File
	logger *slog.Logger
}

// NewAuditLogger opens (or creates) the audit log file in append-only mode.
// File permissions are 0600 (owner read/write only).
func NewAuditLogger(path string, logger *slog.Logger) (*AuditLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening audit log %s: %w", path, err)
	}
	return &AuditLogger{
		file:   f,
		logger: logger,
	}, nil
}

// LogAction serializes the event as JSON and appends it to the audit log.
// Marshal happens outside the lock; only the file write is serialized.
func (a *AuditLogger) LogAction(ctx context.Context, event AuditEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling audit event: %w", err)
	}
	data = append(data, '\n')

	a.mu.Lock()
	_, writeErr := a.file.Write(data)
	a.mu.Unlock()

	if writeErr != nil {
		return fmt.Errorf("writing audit event: %w", writeErr)
	}

	a.logger.InfoContext(ctx, "audit event logged",
		slog.String("action", event.Action),
		slog.String("user_id", event.UserID),
		slog.String("result", event.Result),
		slog.String("correlation_id", event.CorrelationID),
	)

	return nil
}

// Close closes the underlying file.
func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}
