package alert

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// SendNotificationTool sends a one-off notification through configured channels.
type SendNotificationTool struct {
	dispatcher *notification.Dispatcher
	orgID      uuid.UUID
	logger     *slog.Logger
}

// NewSendNotificationTool creates a send_notification tool.
func NewSendNotificationTool(dispatcher *notification.Dispatcher, orgID uuid.UUID, logger *slog.Logger) *SendNotificationTool {
	return &SendNotificationTool{dispatcher: dispatcher, orgID: orgID, logger: logger}
}

func (t *SendNotificationTool) Name() string { return "send_notification" }

func (t *SendNotificationTool) Description() string {
	return "Send a notification message to one or more configured notification channels " +
		"(Telegram, Slack, email, webhook). Specify the channel names and the message to send."
}

func (t *SendNotificationTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channels": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Notification channel names to send to (must already exist)",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "Message subject (used by email; optional for chat channels)",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The notification message body",
			},
		},
		"required": []string{"channels", "message"},
	}
}

func (t *SendNotificationTool) RequiredAction() security.Action {
	return security.Action{Name: "send_notification", RiskLevel: security.RiskMedium}
}

func (t *SendNotificationTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *SendNotificationTool) Validate(params map[string]any) error {
	channels, ok := params["channels"]
	if !ok {
		return fmt.Errorf("missing required parameter: channels")
	}
	channelList, ok := channels.([]any)
	if !ok || len(channelList) == 0 {
		return fmt.Errorf("channels must be a non-empty array of channel names")
	}

	if _, err := requireString(params, "message"); err != nil {
		return err
	}
	return nil
}

func (t *SendNotificationTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	userID := tools.UserIDFromContext(ctx)
	if userID == "" {
		return nil, fmt.Errorf("user identity not available in execution context")
	}

	message, _ := requireString(params, "message")
	subject, _ := params["subject"].(string)

	// Resolve channel names to IDs.
	channelList, _ := params["channels"].([]any)
	store := t.dispatcher.Store()

	var channelIDs []uuid.UUID
	var channelNames []string
	for _, ch := range channelList {
		chName, ok := ch.(string)
		if !ok {
			continue
		}
		channel, err := store.GetByName(ctx, t.orgID, chName)
		if err != nil {
			return nil, fmt.Errorf("notification channel %q not found: %w", chName, err)
		}
		channelIDs = append(channelIDs, channel.ID)
		channelNames = append(channelNames, chName)
	}

	msg := &notification.Message{
		Subject: subject,
		Body:    message,
		Metadata: map[string]string{
			"type":    "manual",
			"user_id": userID,
		},
	}

	results := t.dispatcher.Notify(ctx, t.orgID, channelIDs, msg, userID)

	var succeeded, failed []string
	for chID, err := range results {
		// Find the channel name for this ID.
		name := chID.String()
		for i, id := range channelIDs {
			if id == chID {
				name = channelNames[i]
				break
			}
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", name, err.Error()))
		} else {
			succeeded = append(succeeded, name)
		}
	}

	t.logger.InfoContext(ctx, "notification sent via tool",
		slog.String("user_id", userID),
		slog.Int("succeeded", len(succeeded)),
		slog.Int("failed", len(failed)),
	)

	output := fmt.Sprintf("Notification sent.\nSucceeded: %s", strings.Join(succeeded, ", "))
	if len(failed) > 0 {
		output += fmt.Sprintf("\nFailed: %s", strings.Join(failed, "; "))
	}

	return &tools.Result{
		Output:  output,
		Success: len(failed) == 0,
		Metadata: map[string]any{
			"succeeded": succeeded,
			"failed":    failed,
		},
	}, nil
}
