// Package domain defines cross-cutting entity types used across the system.
package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Organization struct {
	ID        uuid.UUID
	Name      string
	Slug      string
	CreatedAt time.Time
}

// User represents an identity within an organization.
// ExternalID is the opaque string ID used by gateways (e.g. "cli-user", Slack user ID).
type User struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	ExternalID string
	Email      string
	CreatedAt  time.Time
}

// CronJob represents a scheduled recurring workflow execution.
// It runs as the specified UserID, inheriting that user's RBAC permissions,
// budget constraints, and audit trail. No privilege escalation is possible.
type CronJob struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	Name           string
	Description    string
	CronExpression string // Standard 5-field cron (minute hour dom month dow).
	Goal           string // Workflow goal — same as WorkflowRequest.Goal.
	UserID         string // The identity the job runs as for RBAC.
	BudgetLimitUSD float64
	MaxDepth       int
	MaxTasks       int
	Enabled        bool
	NextRunAt      *time.Time
	LastRunAt      *time.Time
	LastWorkflowID *uuid.UUID
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Conversation represents a persistent conversational session between a user and the agent.
// Scoped by (OrgID, UserID) for multi-tenant isolation.
type Conversation struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	UserID    string // External user ID (e.g. "cli-user", Slack user ID).
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConversationMessage is a single turn in a persisted conversation.
type ConversationMessage struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	OrgID          uuid.UUID
	SeqNum         int             // Monotonically increasing within conversation.
	Role           string          // "user" or "assistant" only.
	Content        string          // Plain text content.
	ContentBlocks  json.RawMessage // JSON-encoded []llm.ContentBlock for structured content.
	TokenEstimate  int             // Rough token count for context window management.
	CreatedAt      time.Time
}

// InfraNode represents a registered infrastructure node.
// Aliases enable natural-language lookups (e.g., "VM10", "prod-k8s").
// CredentialRef is an opaque reference into a SecretProvider — it MUST NOT be
// included in LLM-visible output.
type InfraNode struct {
	ID       uuid.UUID
	OrgID    uuid.UUID
	Name     string   // Canonical name (unique per org).
	Aliases  []string // Alternative names for LLM lookup.
	NodeType string   // "vm", "kubernetes", "docker_host", "bare_metal".
	Host     string   // Hostname or IP (shown to LLM for context).
	Port     int      // SSH port / API port. 0 = default for type.
	User     string   // Default connection user (shown to LLM).
	// Opaque key into SecretProvider(env, vault). NEVER shown to LLM.
	CredentialRef string
	// Arbitrary metadata (env, region, team, etc.).
	Tags map[string]string
	// MCPServer name of the MCP server that handles this node type.
	MCPServer string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NotificationChannel represents a configured notification destination.
// Scoped to an organization. CredentialRef stores an opaque reference to
// secrets (SMTP password, webhook secret) — MUST NOT be shown to LLM.
type NotificationChannel struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Name          string            // Unique per org (e.g. "ops-email", "telegram-oncall").
	ChannelType   string            // "telegram", "slack", "email", "webhook", "whatsapp", "signal".
	Config        map[string]string // Channel-specific config (chat_id, channel_id, to, url, phone_number_id, recipient).
	CredentialRef string            // Opaque key into SecretProvider. NEVER shown to LLM.
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AlertRule defines a monitoring condition that triggers notifications.
// Runs as the UserID that created it (no privilege escalation).
type AlertRule struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	Name           string
	Description    string
	Target         string            // What to monitor (e.g. "https://api.example.com/health").
	CheckType      string            // "http_status", "command".
	CheckConfig    map[string]string // Type-specific params (url, expected_status, command, timeout_seconds).
	CronExpression string            // How often to check (5-field cron).
	ChannelIDs     []uuid.UUID       // Notification channels to fire on alert.
	UserID         string            // Identity the check runs as (RBAC inherited).
	Enabled        bool
	CooldownS      int // Min seconds between notifications for same rule. Default: 300.
	LastCheckedAt  *time.Time
	LastAlertedAt  *time.Time
	LastStatus     string // "ok", "alert", "error".
	NextRunAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AlertHistory is an append-only record of every alert check evaluation and notification dispatch.
// Never updated or deleted (audit trail).
type AlertHistory struct {
	ID              uuid.UUID
	OrgID           uuid.UUID
	AlertRuleID     uuid.UUID
	Status          string   // "ok", "alert", "error".
	PreviousStatus  string   // Status before this check.
	Message         string   // Human-readable check result.
	NotifiedVia     []string // Channel names that were notified.
	NotifyErrors    []string // Per-channel error messages (empty on success).
	CheckDurationMS int64
	CreatedAt       time.Time
}

// HeartbeatTask represents a periodic task loaded from a Markdown definition file.
// It runs as the specified UserID, inheriting that user's RBAC permissions,
// budget constraints, and audit trail. No privilege escalation is possible.
type HeartbeatTask struct {
	ID                     uuid.UUID
	OrgID                  uuid.UUID
	Name                   string // Human-readable task name from Markdown heading.
	Description            string // Full prompt/goal text from Markdown body.
	SourceFile             string // File path that defined this task.
	FileGroup              string // Group name from frontmatter (name field).
	CronExpression         string // Per-task cron (5-field).
	Mode                   string // "quick" or "long".
	UserID                 string // Identity the task runs as for RBAC.
	BudgetLimitUSD         float64
	NotificationChannelIDs []uuid.UUID
	Enabled                bool
	NextRunAt              *time.Time
	LastRunAt              *time.Time
	LastStatus             string // "success", "failure", "running".
	LastError              string
	LastResultSummary      string // Truncated result for display.
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// HeartbeatTaskResult is an append-only record of every heartbeat task execution.
// Never updated or deleted (audit trail).
type HeartbeatTaskResult struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	TaskID        uuid.UUID
	Status        string // "success", "failure", "timeout".
	Output        string // Full agent response text.
	OutputSummary string // First 500 chars for notification/display.
	TokensUsed    int
	CostUSD       float64
	DurationMS    int64
	CorrelationID string
	NotifiedVia   []string // Channel names that were notified.
	NotifyErrors  []string // Per-channel error messages.
	CreatedAt     time.Time
}

// NewID generates a new random UUID.
func NewID() uuid.UUID {
	return uuid.New()
}
