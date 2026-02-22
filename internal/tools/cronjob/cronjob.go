// Package cronjob implements the create_cronjob tool for Akili.
// It allows users to create scheduled cron jobs conversationally through
// any gateway (Telegram, Slack, CLI). The LLM converts natural language
// like "every 15 minutes check failed pods" into a structured tool call.
package cronjob

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Tool creates scheduled cron jobs via the agent's tool-use pipeline.
type Tool struct {
	store  scheduler.CronJobStore
	orgID  uuid.UUID
	logger *slog.Logger
}

// NewTool creates a create_cronjob tool backed by the given store.
func NewTool(store scheduler.CronJobStore, orgID uuid.UUID, logger *slog.Logger) *Tool {
	return &Tool{store: store, orgID: orgID, logger: logger}
}

func (t *Tool) Name() string { return "create_cronjob" }
func (t *Tool) Description() string {
	return "Create a scheduled cron job that runs a workflow on a recurring schedule. " +
		"Convert the user's natural language schedule into a standard 5-field cron expression " +
		"(minute hour day-of-month month day-of-week). The goal field should describe what " +
		"the workflow should accomplish on each run."
}

func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Short descriptive name for the cron job (e.g. 'check-failed-pods')",
			},
			"cron_expression": map[string]any{
				"type":        "string",
				"description": "Standard 5-field cron expression: minute hour day-of-month month day-of-week (e.g. '*/15 * * * *' for every 15 minutes)",
			},
			"goal": map[string]any{
				"type":        "string",
				"description": "The workflow goal — what the agent should do on each scheduled run",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional longer description of the cron job's purpose",
			},
			"budget_limit_usd": map[string]any{
				"type":        "number",
				"description": "Optional per-execution budget limit in USD (default: system default)",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": "Optional maximum workflow depth (default: system default)",
			},
			"max_tasks": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of tasks per execution (default: system default)",
			},
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Whether the cron job is enabled immediately (default: true)",
			},
		},
		"required": []string{"name", "cron_expression", "goal"},
	}
}

func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "create_cronjob", RiskLevel: security.RiskHigh}
}

func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *Tool) Validate(params map[string]any) error {
	name, err := requireString(params, "name")
	if err != nil {
		return err
	}
	if len(name) > 200 {
		return fmt.Errorf("name must be 200 characters or fewer")
	}

	cronExpr, err := requireString(params, "cron_expression")
	if err != nil {
		return err
	}
	if _, err := scheduler.ComputeNextRunFrom(cronExpr, time.Now().UTC()); err != nil {
		return fmt.Errorf("invalid cron_expression: %w", err)
	}

	if _, err := requireString(params, "goal"); err != nil {
		return err
	}

	return nil
}

func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	userID := tools.UserIDFromContext(ctx)
	if userID == "" {
		return nil, fmt.Errorf("user identity not available in execution context")
	}

	name, _ := requireString(params, "name")
	cronExpr, _ := requireString(params, "cron_expression")
	goal, _ := requireString(params, "goal")

	description, _ := params["description"].(string)
	budgetLimitUSD, _ := params["budget_limit_usd"].(float64)
	maxDepth, _ := toInt(params["max_depth"])
	maxTasks, _ := toInt(params["max_tasks"])

	enabled := true
	if v, ok := params["enabled"].(bool); ok {
		enabled = v
	}

	nextRun, err := scheduler.ComputeNextRunFrom(cronExpr, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("invalid cron_expression: %w", err)
	}

	now := time.Now().UTC()
	cj := &domain.CronJob{
		ID:             uuid.New(),
		OrgID:          t.orgID,
		Name:           name,
		Description:    description,
		CronExpression: cronExpr,
		Goal:           goal,
		UserID:         userID,
		BudgetLimitUSD: budgetLimitUSD,
		MaxDepth:       maxDepth,
		MaxTasks:       maxTasks,
		Enabled:        enabled,
		NextRunAt:      &nextRun,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := t.store.Create(ctx, cj); err != nil {
		return nil, fmt.Errorf("persisting cron job: %w", err)
	}

	t.logger.InfoContext(ctx, "cron job created via tool",
		slog.String("cronjob_id", cj.ID.String()),
		slog.String("user_id", userID),
		slog.String("name", name),
		slog.String("cron_expression", cronExpr),
		slog.Time("next_run_at", nextRun),
	)

	output := fmt.Sprintf(
		"Cron job created successfully.\n"+
			"ID: %s\nName: %s\nSchedule: %s\nNext run: %s\nEnabled: %t\nGoal: %s",
		cj.ID.String(), name, cronExpr,
		nextRun.Format(time.RFC3339), enabled, goal,
	)

	return &tools.Result{
		Output:  output,
		Success: true,
		Metadata: map[string]any{
			"cronjob_id":      cj.ID.String(),
			"cron_expression": cronExpr,
			"next_run_at":     nextRun.Format(time.RFC3339),
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

// toInt converts a JSON number (float64) to int, handling both float64 and direct int types.
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
