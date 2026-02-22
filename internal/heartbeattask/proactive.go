package heartbeattask

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/notification"
)

// ProactiveHandler triggers notifications when heartbeat tasks detect issues.
// It sends initial findings to configured channels without taking any
// destructive actions — read-only triage only.
type ProactiveHandler struct {
	dispatcher *notification.Dispatcher
	orgID      uuid.UUID
	logger     *slog.Logger
}

// NewProactiveHandler creates a handler that sends proactive alerts via notifications.
func NewProactiveHandler(dispatcher *notification.Dispatcher, orgID uuid.UUID, logger *slog.Logger) *ProactiveHandler {
	return &ProactiveHandler{
		dispatcher: dispatcher,
		orgID:      orgID,
		logger:     logger,
	}
}

// HandleFailure processes a failed heartbeat task and sends proactive alerts.
func (h *ProactiveHandler) HandleFailure(ctx context.Context, taskName, output, userID string, channelIDs []uuid.UUID) {
	if h.dispatcher == nil || len(channelIDs) == 0 {
		return
	}

	// Build alert message.
	summary := truncateProactiveOutput(output, 500)
	body := fmt.Sprintf(
		"Proactive Alert: %s detected an issue.\n\nInitial findings:\n%s\n\nAwaiting human guidance.",
		taskName, summary,
	)

	msg := &notification.Message{
		Subject: fmt.Sprintf("Alert: %s — Issue Detected", taskName),
		Body:    body,
		Metadata: map[string]string{
			"source":    "heartbeat_proactive",
			"task_name": taskName,
			"severity":  classifySeverity(output),
		},
	}

	errs := h.dispatcher.Notify(ctx, h.orgID, channelIDs, msg, userID)
	if len(errs) > 0 {
		for chID, err := range errs {
			h.logger.ErrorContext(ctx, "proactive alert dispatch failed",
				slog.String("task", taskName),
				slog.String("channel_id", chID.String()),
				slog.String("error", err.Error()),
			)
		}
		return
	}

	h.logger.InfoContext(ctx, "proactive alert sent",
		slog.String("task", taskName),
		slog.Int("channels", len(channelIDs)),
	)
}

// classifySeverity infers severity from output content.
func classifySeverity(output string) string {
	lower := strings.ToLower(output)
	criticalKeywords := []string{"critical", "fatal", "panic", "oom", "out of memory", "disk full"}
	for _, kw := range criticalKeywords {
		if strings.Contains(lower, kw) {
			return "critical"
		}
	}
	highKeywords := []string{"error", "failed", "timeout", "unreachable", "down"}
	for _, kw := range highKeywords {
		if strings.Contains(lower, kw) {
			return "high"
		}
	}
	return "medium"
}

func truncateProactiveOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [truncated]"
}
