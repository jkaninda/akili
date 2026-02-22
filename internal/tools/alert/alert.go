// Package alert implements the create_alert tool for Akili.
// It allows users to create monitoring alert rules conversationally through
// any gateway (Telegram, Slack, CLI). The LLM converts natural language
// like "every 5 minutes check api-service status" into a structured tool call.
package alert

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/alerting"
	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// CreateAlertTool creates alert rules via the agent's tool-use pipeline.
type CreateAlertTool struct {
	store        alerting.AlertRuleStore
	channelStore notification.ChannelStore
	orgID        uuid.UUID
	logger       *slog.Logger
}

// NewCreateAlertTool creates a create_alert tool backed by the given stores.
func NewCreateAlertTool(store alerting.AlertRuleStore, channelStore notification.ChannelStore, orgID uuid.UUID, logger *slog.Logger) *CreateAlertTool {
	return &CreateAlertTool{store: store, channelStore: channelStore, orgID: orgID, logger: logger}
}

func (t *CreateAlertTool) Name() string { return "create_alert" }

func (t *CreateAlertTool) Description() string {
	return "Create a monitoring alert rule that checks a target on a recurring schedule and " +
		"sends notifications when conditions change. Convert the user's request into structured " +
		"parameters: target URL or resource, check type, schedule (5-field cron expression), " +
		"and notification channel names."
}

func (t *CreateAlertTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Short descriptive name for the alert rule (e.g. 'api-health-check')",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Resource to monitor (e.g. 'https://api.example.com/health')",
			},
			"check_type": map[string]any{
				"type":        "string",
				"enum":        []string{"http_status", "command"},
				"description": "Type of check to perform",
			},
			"check_config": map[string]any{
				"type":        "object",
				"description": "Check-specific parameters (e.g. {\"url\": \"...\", \"expected_status\": \"200\"})",
			},
			"cron_expression": map[string]any{
				"type":        "string",
				"description": "Standard 5-field cron expression for check frequency (e.g. '*/5 * * * *' for every 5 minutes)",
			},
			"channels": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Notification channel names to alert on (must already exist)",
			},
			"cooldown_seconds": map[string]any{
				"type":        "integer",
				"description": "Minimum seconds between notifications for same rule (default: 300)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional longer description of what this alert monitors",
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Whether the alert is enabled immediately (default: true)",
			},
		},
		"required": []string{"name", "target", "check_type", "cron_expression", "channels"},
	}
}

func (t *CreateAlertTool) RequiredAction() security.Action {
	return security.Action{Name: "create_alert", RiskLevel: security.RiskHigh}
}

func (t *CreateAlertTool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *CreateAlertTool) Validate(params map[string]any) error {
	name, err := requireString(params, "name")
	if err != nil {
		return err
	}
	if len(name) > 200 {
		return fmt.Errorf("name must be 200 characters or fewer")
	}

	if _, err := requireString(params, "target"); err != nil {
		return err
	}

	checkType, err := requireString(params, "check_type")
	if err != nil {
		return err
	}
	switch checkType {
	case "http_status", "command":
		// OK.
	default:
		return fmt.Errorf("check_type must be http_status or command, got %q", checkType)
	}

	cronExpr, err := requireString(params, "cron_expression")
	if err != nil {
		return err
	}
	if _, err := scheduler.ComputeNextRunFrom(cronExpr, time.Now().UTC()); err != nil {
		return fmt.Errorf("invalid cron_expression: %w", err)
	}

	channels, ok := params["channels"]
	if !ok {
		return fmt.Errorf("missing required parameter: channels")
	}
	channelList, ok := channels.([]any)
	if !ok || len(channelList) == 0 {
		return fmt.Errorf("channels must be a non-empty array of channel names")
	}

	return nil
}

func (t *CreateAlertTool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	userID := tools.UserIDFromContext(ctx)
	if userID == "" {
		return nil, fmt.Errorf("user identity not available in execution context")
	}

	name, _ := requireString(params, "name")
	target, _ := requireString(params, "target")
	checkType, _ := requireString(params, "check_type")
	cronExpr, _ := requireString(params, "cron_expression")
	description, _ := params["description"].(string)

	// Parse check_config.
	checkConfig := make(map[string]string)
	if cc, ok := params["check_config"].(map[string]any); ok {
		for k, v := range cc {
			checkConfig[k] = fmt.Sprintf("%v", v)
		}
	}
	// Default: set url from target for http_status checks.
	if checkType == "http_status" && checkConfig["url"] == "" {
		checkConfig["url"] = target
	}

	// Resolve channel names to IDs.
	channelList, _ := params["channels"].([]any)
	var channelIDs []uuid.UUID
	var channelNames []string
	for _, ch := range channelList {
		chName, ok := ch.(string)
		if !ok {
			continue
		}
		channel, err := t.channelStore.GetByName(ctx, t.orgID, chName)
		if err != nil {
			return nil, fmt.Errorf("notification channel %q not found: %w", chName, err)
		}
		channelIDs = append(channelIDs, channel.ID)
		channelNames = append(channelNames, chName)
	}

	cooldownS := 300
	if v, ok := toInt(params["cooldown_seconds"]); ok && v > 0 {
		cooldownS = v
	}

	enabled := true
	if v, ok := params["enabled"].(bool); ok {
		enabled = v
	}

	nextRun, err := scheduler.ComputeNextRunFrom(cronExpr, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("invalid cron_expression: %w", err)
	}

	now := time.Now().UTC()
	rule := &domain.AlertRule{
		ID:             uuid.New(),
		OrgID:          t.orgID,
		Name:           name,
		Description:    description,
		Target:         target,
		CheckType:      checkType,
		CheckConfig:    checkConfig,
		CronExpression: cronExpr,
		ChannelIDs:     channelIDs,
		UserID:         userID,
		Enabled:        enabled,
		CooldownS:      cooldownS,
		LastStatus:     "ok",
		NextRunAt:      &nextRun,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := t.store.Create(ctx, rule); err != nil {
		return nil, fmt.Errorf("persisting alert rule: %w", err)
	}

	t.logger.InfoContext(ctx, "alert rule created via tool",
		slog.String("rule_id", rule.ID.String()),
		slog.String("user_id", userID),
		slog.String("name", name),
		slog.String("cron_expression", cronExpr),
		slog.Time("next_run_at", nextRun),
	)

	output := fmt.Sprintf(
		"Alert rule created successfully.\n"+
			"ID: %s\nName: %s\nTarget: %s\nCheck: %s\nSchedule: %s\nNext check: %s\n"+
			"Channels: %v\nCooldown: %ds\nEnabled: %t",
		rule.ID.String(), name, target, checkType, cronExpr,
		nextRun.Format(time.RFC3339), channelNames, cooldownS, enabled,
	)

	return &tools.Result{
		Output:  output,
		Success: true,
		Metadata: map[string]any{
			"rule_id":         rule.ID.String(),
			"cron_expression": cronExpr,
			"next_run_at":     nextRun.Format(time.RFC3339),
			"channels":        channelNames,
			"enabled":         enabled,
		},
	}, nil
}

func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string, got %T", key, v)
	}
	if s == "" {
		return "", fmt.Errorf("parameter %s must not be empty", key)
	}
	return s, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}
