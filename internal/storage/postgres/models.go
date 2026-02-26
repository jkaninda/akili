package postgres

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OrgModel maps to the "organizations" table.
type OrgModel struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name      string    `gorm:"not null;uniqueIndex"`
	Slug      string    `gorm:"not null;uniqueIndex"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (OrgModel) TableName() string { return "organizations" }

// UserModel maps to the "users" table.
type UserModel struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID      uuid.UUID `gorm:"type:uuid;not null;index"`
	ExternalID string    `gorm:"not null"`
	Email      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  gorm.DeletedAt `gorm:"index"`
}

func (UserModel) TableName() string { return "users" }

// RoleModel maps to the "roles" table.
type RoleModel struct {
	ID           uuid.UUID         `gorm:"type:uuid;primaryKey"`
	OrgID        uuid.UUID         `gorm:"type:uuid;not null;index"`
	Name         string            `gorm:"not null"`
	MaxRiskLevel string            `gorm:"not null;default:'critical'"`
	Permissions  []PermissionModel `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (RoleModel) TableName() string { return "roles" }

// PermissionModel maps to the "permissions" table.
type PermissionModel struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	RoleID           uuid.UUID `gorm:"type:uuid;not null;index"`
	ActionName       string    `gorm:"not null"`
	RequiresApproval bool      `gorm:"not null;default:false"`
}

func (PermissionModel) TableName() string { return "permissions" }

// UserRoleModel maps to the "user_roles" join table.
type UserRoleModel struct {
	UserID uuid.UUID `gorm:"type:uuid;primaryKey"`
	RoleID uuid.UUID `gorm:"type:uuid;primaryKey"`
}

func (UserRoleModel) TableName() string { return "user_roles" }

// BudgetModel maps to the "budgets" table.
type BudgetModel struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID       uuid.UUID `gorm:"type:uuid;not null;index"`
	UserID      uuid.UUID `gorm:"type:uuid;not null"`
	LimitUSD    float64   `gorm:"type:numeric(14,6);not null;default:10"`
	SpentUSD    float64   `gorm:"type:numeric(14,6);not null;default:0"`
	PeriodStart time.Time `gorm:"type:date;not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (BudgetModel) TableName() string { return "budgets" }

// BudgetReservationModel maps to the "budget_reservations" table.
type BudgetReservationModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID         uuid.UUID `gorm:"type:uuid;not null"`
	BudgetID      uuid.UUID `gorm:"type:uuid;not null;index"`
	AmountUSD     float64   `gorm:"type:numeric(14,6);not null"`
	CorrelationID string
	CreatedAt     time.Time
	ReleasedAt    *time.Time // NULL = active reservation
}

func (BudgetReservationModel) TableName() string { return "budget_reservations" }

// JSONB is a json.RawMessage that implements the driver.Valuer and sql.Scanner interfaces
// for GORM JSONB columns.
type JSONB json.RawMessage

// ApprovalModel maps to the "approvals" table.
type ApprovalModel struct {
	ID             string    `gorm:"primaryKey"`
	OrgID          uuid.UUID `gorm:"type:uuid;not null;index"`
	UserID         string    `gorm:"not null"`
	ToolName       string    `gorm:"not null"`
	Parameters     JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	ActionName     string    `gorm:"not null"`
	RiskLevel      string    `gorm:"not null"`
	EstimatedCost  float64   `gorm:"type:numeric(14,6)"`
	CorrelationID  string
	ConversationID string // Conversation to update after execution.
	ToolUseID      string // LLM tool_use block ID for updating the pending tool_result.
	Status         int16  `gorm:"not null;default:0"`
	ApprovedBy     string
	CreatedAt      time.Time
	ExpiresAt      time.Time `gorm:"index"`
	ResolvedAt     *time.Time
}

func (ApprovalModel) TableName() string { return "approvals" }

// AuditEventModel maps to the "audit_events" table.
// No UpdatedAt or DeletedAt — audit log is append-only and immutable.
type AuditEventModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID         uuid.UUID `gorm:"type:uuid;not null;index"`
	CorrelationID string    `gorm:"index"`
	UserID        string    `gorm:"not null"`
	Action        string    `gorm:"not null"`
	Tool          string    `gorm:"not null"`
	Parameters    JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	Result        string    `gorm:"not null"`
	TokensUsed    int
	CostUSD       float64 `gorm:"type:numeric(14,6)"`
	ApprovedBy    string
	Error         string
	CreatedAt     time.Time `gorm:"index"`
}

func (AuditEventModel) TableName() string { return "audit_events" }

// WorkflowModel maps to the "workflows" table.
type WorkflowModel struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey"`
	OrgID          uuid.UUID  `gorm:"type:uuid;not null;index"`
	UserID         string     `gorm:"not null"`
	CorrelationID  string     `gorm:"index"`
	Goal           string     `gorm:"type:text;not null"`
	Status         string     `gorm:"not null;default:'pending'"`
	RootTaskID     *uuid.UUID `gorm:"type:uuid"`
	BudgetLimitUSD float64    `gorm:"type:numeric(14,6);not null;default:0"`
	BudgetSpentUSD float64    `gorm:"type:numeric(14,6);not null;default:0"`
	MaxDepth       int        `gorm:"not null;default:5"`
	MaxTasks       int        `gorm:"not null;default:50"`
	TaskCount      int        `gorm:"not null;default:0"`
	Error          string     `gorm:"type:text"`
	Metadata       JSONB      `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

func (WorkflowModel) TableName() string { return "workflows" }

// TaskModel maps to the "tasks" table.
type TaskModel struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey"`
	WorkflowID   uuid.UUID  `gorm:"type:uuid;not null;index"`
	OrgID        uuid.UUID  `gorm:"type:uuid;not null;index"`
	ParentTaskID *uuid.UUID `gorm:"type:uuid;index"`
	AgentRole    string     `gorm:"not null"`
	Description  string     `gorm:"type:text;not null"`
	Input        string     `gorm:"type:text"`
	Output       string     `gorm:"type:text"`
	Status       string     `gorm:"not null;default:'pending'"`
	Mode         string     `gorm:"not null;default:'sequential'"`
	Depth        int        `gorm:"not null;default:0"`
	Priority     int        `gorm:"not null;default:0"`
	DependsOn    JSONB      `gorm:"type:jsonb;not null;default:'[]'"`
	Error        string     `gorm:"type:text"`
	CostUSD      float64    `gorm:"type:numeric(14,6);not null;default:0"`
	TokensUsed   int        `gorm:"not null;default:0"`
	ClaimedBy      string     `gorm:"index"`
	ClaimedAt      *time.Time `gorm:"index"`
	RetryCount     int        `gorm:"not null;default:0"`
	MaxRetries     int        `gorm:"not null;default:0"`
	OriginalTaskID *uuid.UUID `gorm:"type:uuid;index"`
	Metadata       JSONB      `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

func (TaskModel) TableName() string { return "tasks" }

// AgentMessageModel maps to the "agent_messages" table.
type AgentMessageModel struct {
	ID            uuid.UUID  `gorm:"type:uuid;primaryKey"`
	WorkflowID    uuid.UUID  `gorm:"type:uuid;not null;index"`
	OrgID         uuid.UUID  `gorm:"type:uuid;not null;index"`
	FromTaskID    uuid.UUID  `gorm:"type:uuid;not null"`
	ToTaskID      *uuid.UUID `gorm:"type:uuid"`
	FromRole      string     `gorm:"not null"`
	ToRole        string     `gorm:"not null"`
	MessageType   string     `gorm:"not null"`
	Content       string     `gorm:"type:text;not null"`
	Artifacts     JSONB      `gorm:"type:jsonb;not null;default:'[]'"`
	CorrelationID string     `gorm:"index"`
	CreatedAt     time.Time  `gorm:"index"`
}

func (AgentMessageModel) TableName() string { return "agent_messages" }

// AgentSkillModel maps to the "agent_skills" table.
type AgentSkillModel struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID            uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_agent_skills_org_role_key"`
	AgentRole        string    `gorm:"not null;uniqueIndex:idx_agent_skills_org_role_key"`
	SkillKey         string    `gorm:"not null;uniqueIndex:idx_agent_skills_org_role_key"`
	SuccessCount     int       `gorm:"not null;default:0"`
	FailureCount     int       `gorm:"not null;default:0"`
	AvgDurationMS    float64   `gorm:"type:numeric(14,4);not null;default:0"`
	AvgCostUSD       float64   `gorm:"type:numeric(14,6);not null;default:0"`
	ReliabilityScore float64   `gorm:"type:numeric(6,4);not null;default:0"`
	MaturityLevel    string    `gorm:"not null;default:'basic'"`
	CreatedAt        time.Time
	LastUpdatedAt    time.Time

	// Static definition metadata.
	Name          string  `gorm:"not null;default:''"`
	Category      string  `gorm:"not null;default:''"`
	ToolsRequired string  `gorm:"type:text;not null;default:'[]'"` // JSON-encoded []string.
	RiskLevel     string  `gorm:"not null;default:''"`
	DefaultBudget float64 `gorm:"type:numeric(14,6);not null;default:0"`
	Description   string  `gorm:"type:text;not null;default:''"`
}

func (AgentSkillModel) TableName() string { return "agent_skills" }

// CronJobModel maps to the "cron_jobs" table.
type CronJobModel struct {
	ID             uuid.UUID  `gorm:"type:uuid;primaryKey"`
	OrgID          uuid.UUID  `gorm:"type:uuid;not null;index"`
	Name           string     `gorm:"not null"`
	Description    string     `gorm:"type:text"`
	CronExpression string     `gorm:"not null"`
	Goal           string     `gorm:"type:text;not null"`
	UserID         string     `gorm:"not null"`
	BudgetLimitUSD float64    `gorm:"type:numeric(14,6);not null;default:0"`
	MaxDepth       int        `gorm:"not null;default:5"`
	MaxTasks       int        `gorm:"not null;default:50"`
	Enabled        bool       `gorm:"not null;default:true"`
	NextRunAt      *time.Time `gorm:"index"`
	LastRunAt      *time.Time
	LastWorkflowID *uuid.UUID `gorm:"type:uuid"`
	LastError      string     `gorm:"type:text"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (CronJobModel) TableName() string { return "cron_jobs" }

// InfraNodeModel maps to the "infra_nodes" table.
type InfraNodeModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_infra_org_name"`
	Name          string    `gorm:"not null;uniqueIndex:idx_infra_org_name"`
	Aliases       JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	NodeType      string    `gorm:"not null"`
	Host          string    `gorm:"not null"`
	Port          int       `gorm:"not null;default:0"`
	User          string    `gorm:"not null;default:''"`
	CredentialRef string    `gorm:"not null;default:''"`
	Tags          JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	MCPServer     string    `gorm:"not null;default:''"`
	Enabled       bool      `gorm:"not null;default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (InfraNodeModel) TableName() string { return "infra_nodes" }

// ConversationModel maps to the "conversations" table.
type ConversationModel struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null;index"`
	UserID    string    `gorm:"not null;index:idx_conv_user"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (ConversationModel) TableName() string { return "conversations" }

// ConversationMessageModel maps to the "conversation_messages" table.
type ConversationMessageModel struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey"`
	ConversationID uuid.UUID `gorm:"type:uuid;not null;index:idx_convmsg_seq"`
	OrgID          uuid.UUID `gorm:"type:uuid;not null;index"`
	SeqNum         int       `gorm:"not null;index:idx_convmsg_seq"`
	Role           string    `gorm:"not null"`
	Content        string    `gorm:"type:text"`
	ContentBlocks  JSONB     `gorm:"type:jsonb"`
	TokenEstimate  int       `gorm:"not null;default:0"`
	CreatedAt      time.Time
}

func (ConversationMessageModel) TableName() string { return "conversation_messages" }

// NotificationChannelModel maps to the "notification_channels" table.
type NotificationChannelModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_notif_ch_org_name"`
	Name          string    `gorm:"not null;uniqueIndex:idx_notif_ch_org_name"`
	ChannelType   string    `gorm:"not null"`
	Config        JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	CredentialRef string    `gorm:"not null;default:''"`
	Enabled       bool      `gorm:"not null;default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (NotificationChannelModel) TableName() string { return "notification_channels" }

// AlertRuleModel maps to the "alert_rules" table.
type AlertRuleModel struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID          uuid.UUID `gorm:"type:uuid;not null;index"`
	Name           string    `gorm:"not null"`
	Description    string    `gorm:"type:text"`
	Target         string    `gorm:"not null"`
	CheckType      string    `gorm:"not null"`
	CheckConfig    JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	CronExpression string    `gorm:"not null"`
	ChannelIDs     JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	UserID         string    `gorm:"not null"`
	Enabled        bool      `gorm:"not null;default:true"`
	CooldownS      int       `gorm:"not null;default:300"`
	LastCheckedAt  *time.Time
	LastAlertedAt  *time.Time
	LastStatus     string     `gorm:"not null;default:'ok'"`
	NextRunAt      *time.Time `gorm:"index"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (AlertRuleModel) TableName() string { return "alert_rules" }

// AlertHistoryModel maps to the "alert_history" table.
// Append-only. No UpdatedAt or DeletedAt.
type AlertHistoryModel struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID           uuid.UUID `gorm:"type:uuid;not null;index"`
	AlertRuleID     uuid.UUID `gorm:"type:uuid;not null;index:idx_alert_hist_rule"`
	Status          string    `gorm:"not null"`
	PreviousStatus  string    `gorm:"not null;default:''"`
	Message         string    `gorm:"type:text"`
	NotifiedVia     JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	NotifyErrors    JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	CheckDurationMS int64     `gorm:"not null;default:0"`
	CreatedAt       time.Time `gorm:"index:idx_alert_hist_rule"`
}

func (AlertHistoryModel) TableName() string { return "alert_history" }

// AgentIdentityModel maps to the "agent_identities" table.
type AgentIdentityModel struct {
	ID               uuid.UUID  `gorm:"type:uuid;primaryKey"`
	AgentID          string     `gorm:"not null;uniqueIndex"`
	Name             string     `gorm:"not null"`
	Version          string     `gorm:"not null"`
	PublicKey        string     `gorm:"not null"`
	Capabilities     JSONB      `gorm:"type:jsonb;not null;default:'[]'"`
	TrustLevel       string     `gorm:"not null;default:'untrusted'"`
	EnvironmentScope string     `gorm:"not null;default:''"`
	LastHeartbeatAt  *time.Time `gorm:"index"`
	Status           string     `gorm:"not null;default:'offline'"`
	RegisteredAt     time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DeletedAt        gorm.DeletedAt `gorm:"index"`
}

func (AgentIdentityModel) TableName() string { return "agent_identities" }

// AgentHeartbeatModel maps to the "agent_heartbeats" table.
// Append-only history of heartbeat events.
type AgentHeartbeatModel struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	AgentID      string    `gorm:"not null;index"`
	Version      string
	Status       string
	Skills       JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	Capabilities JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	ActiveTasks  int       `gorm:"not null;default:0"`
	MaxTasks     int       `gorm:"not null;default:0"`
	Metadata     JSONB     `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time `gorm:"index"`
}

func (AgentHeartbeatModel) TableName() string { return "agent_heartbeats" }

// HeartbeatTaskModel maps to the "heartbeat_tasks" table.
type HeartbeatTaskModel struct {
	ID                     uuid.UUID  `gorm:"type:uuid;primaryKey"`
	OrgID                  uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_hbt_org_group_name"`
	Name                   string     `gorm:"not null;uniqueIndex:idx_hbt_org_group_name"`
	Description            string     `gorm:"type:text"`
	SourceFile             string     `gorm:"not null;default:''"`
	FileGroup              string     `gorm:"not null;default:'';uniqueIndex:idx_hbt_org_group_name"`
	CronExpression         string     `gorm:"not null"`
	Mode                   string     `gorm:"not null;default:'quick'"`
	UserID                 string     `gorm:"not null"`
	BudgetLimitUSD         float64    `gorm:"type:numeric(14,6);not null;default:0.5"`
	NotificationChannelIDs JSONB      `gorm:"type:jsonb;not null;default:'[]'"`
	Enabled                bool       `gorm:"not null;default:true"`
	NextRunAt              *time.Time `gorm:"index"`
	LastRunAt              *time.Time
	LastStatus             string `gorm:"not null;default:''"`
	LastError              string `gorm:"type:text"`
	LastResultSummary      string `gorm:"type:text"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
	DeletedAt              gorm.DeletedAt `gorm:"index"`
}

func (HeartbeatTaskModel) TableName() string { return "heartbeat_tasks" }

// HeartbeatTaskResultModel maps to the "heartbeat_task_results" table.
// Append-only. No UpdatedAt or DeletedAt.
type HeartbeatTaskResultModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID         uuid.UUID `gorm:"type:uuid;not null;index"`
	TaskID        uuid.UUID `gorm:"type:uuid;not null;index:idx_hbt_result_task"`
	Status        string    `gorm:"not null"`
	Output        string    `gorm:"type:text"`
	OutputSummary string    `gorm:"type:text"`
	TokensUsed    int       `gorm:"not null;default:0"`
	CostUSD       float64   `gorm:"type:numeric(14,6);not null;default:0"`
	DurationMS    int64     `gorm:"not null;default:0"`
	CorrelationID string    `gorm:"index"`
	NotifiedVia   JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	NotifyErrors  JSONB     `gorm:"type:jsonb;not null;default:'[]'"`
	CreatedAt     time.Time `gorm:"index:idx_hbt_result_task"`
}

func (HeartbeatTaskResultModel) TableName() string { return "heartbeat_task_results" }

// SoulEventModel maps to the "soul_events" table.
// Append-only. No UpdatedAt or DeletedAt — the soul event log is immutable.
type SoulEventModel struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey"`
	OrgID       uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_soul_events_org_version"`
	Version     int       `gorm:"not null;uniqueIndex:idx_soul_events_org_version"`
	EventType   string    `gorm:"not null"`
	Severity    string    `gorm:"not null;default:'normal'"`
	Category    string    `gorm:"not null;default:''"`
	Title       string    `gorm:"not null"`
	Description string    `gorm:"type:text;not null"`
	Evidence    string    `gorm:"type:text;not null;default:'{}'"`
	PriorState  string    `gorm:"type:text;not null;default:''"`
	CreatedAt   time.Time `gorm:"index"`
}

func (SoulEventModel) TableName() string { return "soul_events" }
