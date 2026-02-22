package security

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// PGAuditLogger adapts an AuditStore to the auditAppender interface
// expected by the security Manager.
type PGAuditLogger struct {
	store  AuditStore
	orgID  uuid.UUID
	logger *slog.Logger
}

// NewPGAuditLogger creates a postgres-backed audit logger.
func NewPGAuditLogger(store AuditStore, orgID uuid.UUID, logger *slog.Logger) *PGAuditLogger {
	return &PGAuditLogger{
		store:  store,
		orgID:  orgID,
		logger: logger,
	}
}

// LogAction appends an audit event to the database.
func (a *PGAuditLogger) LogAction(ctx context.Context, event AuditEvent) error {
	err := a.store.Append(ctx, a.orgID, event)
	if err != nil {
		a.logger.ErrorContext(ctx, "failed to log audit event",
			slog.String("action", event.Action),
			slog.String("error", err.Error()),
		)
		return err
	}

	a.logger.InfoContext(ctx, "audit event logged (pg)",
		slog.String("action", event.Action),
		slog.String("user_id", event.UserID),
		slog.String("result", event.Result),
		slog.String("correlation_id", event.CorrelationID),
	)
	return nil
}

// Close is a no-op for the postgres audit logger. The database connection
// is managed by the storage layer and closed separately.
func (a *PGAuditLogger) Close() error {
	return nil
}
