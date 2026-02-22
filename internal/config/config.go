// Package config handles loading and validating Akili configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

func init() {
	// Load .env file if it exists
	_ = godotenv.Load()
}

// Config is the root configuration for Akili.
type Config struct {
	Workspace      string                `json:"workspace,omitempty" yaml:"workspace,omitempty"` // Workspace root. Default: ~/.akili/workspace. Override: AKILI_WORKSPACE env var.
	DataDir        string                `json:"data_dir,omitempty" yaml:"data_dir,omitempty"`   // Persistent data directory. Default: ~/.akili/data. Override: AKILI_DATA_DIR env var.
	Storage        *StorageConfig        `json:"storage,omitempty" yaml:"storage,omitempty"`     // nil = SQLite default (derived from workspace)
	Security       SecurityConfig        `json:"security" yaml:"security"`
	Budget         BudgetConfig          `json:"budget" yaml:"budget"`
	Sandbox        SandboxConfig         `json:"sandbox" yaml:"sandbox"`
	Tools          ToolsConfig           `json:"tools" yaml:"tools"`
	Gateways       GatewaysConfig        `json:"gateways" yaml:"gateways"`
	Approval       ApprovalConfig        `json:"approval" yaml:"approval"`
	Providers      ProvidersConfig       `json:"providers" yaml:"providers"`
	Observability  *ObservabilityConfig  `json:"observability,omitempty" yaml:"observability,omitempty"`     // nil = observability disabled
	Orchestrator   *OrchestratorConfig   `json:"orchestrator,omitempty" yaml:"orchestrator,omitempty"`       // nil = multi-agent disabled
	Runtime        *RuntimeConfig        `json:"runtime,omitempty" yaml:"runtime,omitempty"`                 // nil = gateway mode (default)
	Scheduler      *SchedulerConfig      `json:"scheduler,omitempty" yaml:"scheduler,omitempty"`             // nil = cron scheduler disabled
	Memory         *MemoryConfig         `json:"memory,omitempty" yaml:"memory,omitempty"`                   // nil = no persistent memory
	Infrastructure *InfrastructureConfig `json:"infrastructure,omitempty" yaml:"infrastructure,omitempty"`   // nil = infrastructure features disabled
	Secrets        *SecretsConfig        `json:"secrets,omitempty" yaml:"secrets,omitempty"`                 // nil = env-only secrets
	Notification   *NotificationConfig   `json:"notification,omitempty" yaml:"notification,omitempty"`       // nil = notifications disabled
	Alerting       *AlertingConfig       `json:"alerting,omitempty" yaml:"alerting,omitempty"`               // nil = alert checking disabled
	HeartbeatTasks *HeartbeatTasksConfig `json:"heartbeat_tasks,omitempty" yaml:"heartbeat_tasks,omitempty"` // nil = heartbeat tasks disabled
	Policies       *PoliciesConfig       `json:"policies,omitempty" yaml:"policies,omitempty"`               // nil = no policy enforcement
	EmbeddedAgent  *EmbeddedAgentConfig  `json:"embedded_agent,omitempty" yaml:"embedded_agent,omitempty"`   // nil = embedded agent with fallback enabled
}

// Mode returns the runtime mode. Defaults to "gateway".
func (c *Config) Mode() string {
	if c.Runtime != nil && c.Runtime.Mode != "" {
		return c.Runtime.Mode
	}
	return "gateway"
}

// StorageConfig configures the persistence backend.
// When nil, defaults to SQLite with the database path derived from the workspace.
type StorageConfig struct {
	Driver   string                 `json:"driver" yaml:"driver"`                         // "sqlite" (default) or "postgres".
	SQLite   *SQLiteStorageConfig   `json:"sqlite,omitempty" yaml:"sqlite,omitempty"`     // SQLite-specific settings.
	Postgres *PostgresStorageConfig `json:"postgres,omitempty" yaml:"postgres,omitempty"` // PostgreSQL-specific settings.
}

// StorageDriver returns the configured driver, defaulting to "sqlite".
func (s *StorageConfig) StorageDriver() string {
	if s != nil && s.Driver != "" {
		return s.Driver
	}
	return "sqlite"
}

// SQLiteStorageConfig holds SQLite-specific settings.
type SQLiteStorageConfig struct {
	Path        string `json:"path,omitempty" yaml:"path,omitempty"` // Database file path. Default: derived from workspace.
	JournalMode string `json:"journal_mode" yaml:"journal_mode"`     // "wal" (default), "delete", "truncate", etc.
}

// PostgresStorageConfig holds PostgreSQL-specific settings.
type PostgresStorageConfig struct {
	DSN              string `json:"dsn" yaml:"dsn"`
	MaxOpenConns     int    `json:"max_open_conns" yaml:"max_open_conns"`           // Default: 25
	MaxIdleConns     int    `json:"max_idle_conns" yaml:"max_idle_conns"`           // Default: 5
	ConnMaxLifetimeS int    `json:"conn_max_lifetime_s" yaml:"conn_max_lifetime_s"` // Default: 1800 (30 min)
	DefaultOrgName   string `json:"default_org_name" yaml:"default_org_name"`       // Default: "default"
}

// EmbeddedAgentConfig controls the embedded agent in gateway mode.
// When no external agents are connected via WebSocket, the gateway can
// optionally run an embedded agent for simple CLI usage.
type EmbeddedAgentConfig struct {
	Enabled  bool `json:"enabled" yaml:"enabled"`   // Default: true.
	Fallback bool `json:"fallback" yaml:"fallback"` // Use embedded agent as fallback when no remote agents connected. Default: true.
}

// DatabaseConfig configures PostgreSQL persistence.
// DEPRECATED: Use Storage.Postgres instead. Kept for backward compatibility.
// When present and DSN is non-empty, the system uses postgres-backed storage
// instead of in-memory state. DSN can be overridden by AKILI_DB_DSN env var.
type DatabaseConfig struct {
	DSN              string `json:"dsn" yaml:"dsn"`
	MaxOpenConns     int    `json:"max_open_conns" yaml:"max_open_conns"`           // Default: 25
	MaxIdleConns     int    `json:"max_idle_conns" yaml:"max_idle_conns"`           // Default: 5
	ConnMaxLifetimeS int    `json:"conn_max_lifetime_s" yaml:"conn_max_lifetime_s"` // Default: 1800 (30 min)
	DefaultOrgName   string `json:"default_org_name" yaml:"default_org_name"`       // Default: "default"
}

// ObservabilityConfig configures metrics, tracing, health checks, and anomaly detection.
// When nil, all observability features are disabled with zero overhead.
type ObservabilityConfig struct {
	Metrics *MetricsConfig `json:"metrics,omitempty" yaml:"metrics,omitempty"`
	Tracing *TracingConfig `json:"tracing,omitempty" yaml:"tracing,omitempty"`
	Health  *HealthConfig  `json:"health,omitempty" yaml:"health,omitempty"`
	Anomaly *AnomalyConfig `json:"anomaly,omitempty" yaml:"anomaly,omitempty"`
}

// MetricsConfig configures Prometheus metrics exposition.
type MetricsConfig struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Path    string `json:"path" yaml:"path"` // Default: "/metrics"
}

// TracingConfig configures OpenTelemetry distributed tracing.
type TracingConfig struct {
	Enabled     bool    `json:"enabled" yaml:"enabled"`
	Endpoint    string  `json:"endpoint" yaml:"endpoint"`         // OTLP endpoint, e.g. "localhost:4317"
	Protocol    string  `json:"protocol" yaml:"protocol"`         // "grpc" or "http". Default: "grpc"
	ServiceName string  `json:"service_name" yaml:"service_name"` // Default: "akili"
	SampleRate  float64 `json:"sample_rate" yaml:"sample_rate"`   // 0.0–1.0. Default: 1.0
	Insecure    bool    `json:"insecure" yaml:"insecure"`         // Skip TLS for dev
}

// HealthConfig configures dependency health checks for readiness probes.
type HealthConfig struct {
	IncludeDB      bool `json:"include_db" yaml:"include_db"`
	IncludeSandbox bool `json:"include_sandbox" yaml:"include_sandbox"`
}

// AnomalyConfig configures threshold-based anomaly detection.
type AnomalyConfig struct {
	Enabled               bool    `json:"enabled" yaml:"enabled"`
	ErrorRateThreshold    float64 `json:"error_rate_threshold" yaml:"error_rate_threshold"`       // e.g. 0.5 = 50% errors
	BudgetSpikeMultiplier float64 `json:"budget_spike_multiplier" yaml:"budget_spike_multiplier"` // e.g. 3.0 = 3x normal
	WindowSeconds         int     `json:"window_seconds" yaml:"window_seconds"`                   // Sliding window. Default: 300
}

// OrchestratorConfig configures the multi-agent workflow engine.
// When nil, the system operates in single-agent mode (backward compatible).
type OrchestratorConfig struct {
	Enabled                  bool     `json:"enabled" yaml:"enabled"`
	DefaultBudgetUSD         float64  `json:"default_budget_usd" yaml:"default_budget_usd"`                         // Per-workflow. Default: 1.0
	DefaultMaxDepth          int      `json:"max_depth" yaml:"max_depth"`                                           // Default: 5
	DefaultMaxTasks          int      `json:"max_tasks" yaml:"max_tasks"`                                           // Default: 50
	MaxConcurrentTasks       int      `json:"max_concurrent_tasks" yaml:"max_concurrent_tasks"`                     // Default: 5
	TaskTimeoutSeconds       int      `json:"task_timeout_seconds" yaml:"task_timeout_seconds"`                     // Default: 300
	SkillsDir                string   `json:"skills_dir,omitempty" yaml:"skills_dir,omitempty"`                     // Path to skill Markdown files.
	SkillsDirs               []string `json:"skills_dirs,omitempty" yaml:"skills_dirs,omitempty"`                   // Additional skill directories.
	SkillPollIntervalSeconds int      `json:"skill_poll_interval_seconds" yaml:"skill_poll_interval_seconds"`       // 0 = one-shot load (backward compat).
	CommunityPacksDirs       []string `json:"community_packs_dirs,omitempty" yaml:"community_packs_dirs,omitempty"` // Paths to community skill pack directories.
	PlanningEnabled          bool     `json:"planning_enabled" yaml:"planning_enabled"`                             // Enable ReAct planning for complex tasks.
}

// SkillPollInterval returns the skill polling interval. 0 = disabled (one-shot load).
func (o *OrchestratorConfig) SkillPollInterval() time.Duration {
	if o != nil && o.SkillPollIntervalSeconds > 0 {
		return time.Duration(o.SkillPollIntervalSeconds) * time.Second
	}
	return 0
}

// AllSkillsDirs returns all configured skill directories (merges SkillsDir + SkillsDirs).
func (o *OrchestratorConfig) AllSkillsDirs() []string {
	if o == nil {
		return nil
	}
	var dirs []string
	if o.SkillsDir != "" {
		dirs = append(dirs, o.SkillsDir)
	}
	dirs = append(dirs, o.SkillsDirs...)
	return dirs
}

// RuntimeConfig controls which mode the Akili process runs in.
// When nil, defaults to gateway mode for backward compatibility.
type RuntimeConfig struct {
	Mode  string       `json:"mode" yaml:"mode"`                       // "gateway" (default) or "agent".
	Agent *AgentConfig `json:"agent,omitempty" yaml:"agent,omitempty"` // Agent-specific settings.
}

// AgentConfig holds settings specific to agent mode.
type AgentConfig struct {
	AgentID                  string   `json:"agent_id" yaml:"agent_id"`                                     // Unique instance ID. Default: hostname-pid.
	GatewayURL               string   `json:"gateway_url" yaml:"gateway_url"`                               // WebSocket URL for gateway connection (e.g., "ws://localhost:8080/ws/agents").
	Token                    string   `json:"token" yaml:"token"`                                           // Authentication token for WebSocket connection.
	Roles                    []string `json:"roles" yaml:"roles"`                                           // Roles to handle. Empty = all.
	PollIntervalSeconds      int      `json:"poll_interval_seconds" yaml:"poll_interval_seconds"`           // Default: 2. (legacy DB polling mode)
	MaxConcurrentTasks       int      `json:"max_concurrent_tasks" yaml:"max_concurrent_tasks"`             // Default: 5.
	TaskTimeoutSeconds       int      `json:"task_timeout_seconds" yaml:"task_timeout_seconds"`             // Default: 300.
	HeartbeatIntervalSeconds int      `json:"heartbeat_interval_seconds" yaml:"heartbeat_interval_seconds"` // Default: 30.
	ReconnectIntervalSeconds int      `json:"reconnect_interval_seconds" yaml:"reconnect_interval_seconds"` // Default: 5. WebSocket reconnect backoff base.
	StaleClaimSeconds        int      `json:"stale_claim_seconds" yaml:"stale_claim_seconds"`               // Default: 120.
	Name                     string   `json:"name" yaml:"name"`                                             // Human-readable name. Default: hostname.
	Version                  string   `json:"version" yaml:"version"`                                       // Agent software version.
	Capabilities             []string `json:"capabilities" yaml:"capabilities"`                             // Declared capabilities (tool names, roles).
	EnvironmentScope         string   `json:"environment_scope" yaml:"environment_scope"`                   // "production", "staging", "development".
	KeyFile                  string   `json:"key_file" yaml:"key_file"`                                     // Path to Ed25519 private key. Empty = generate new.
}

// ReconnectInterval returns the base reconnect interval with a default of 5s.
func (a *AgentConfig) ReconnectInterval() time.Duration {
	if a != nil && a.ReconnectIntervalSeconds > 0 {
		return time.Duration(a.ReconnectIntervalSeconds) * time.Second
	}
	return 5 * time.Second
}

// UseWebSocket returns true if the agent should connect via WebSocket.
func (a *AgentConfig) UseWebSocket() bool {
	return a != nil && a.GatewayURL != ""
}

// PollInterval returns the poll interval with a default of 2s.
func (a *AgentConfig) PollInterval() time.Duration {
	if a != nil && a.PollIntervalSeconds > 0 {
		return time.Duration(a.PollIntervalSeconds) * time.Second
	}
	return 2 * time.Second
}

// Concurrency returns the max concurrent tasks with a default of 5.
func (a *AgentConfig) Concurrency() int {
	if a != nil && a.MaxConcurrentTasks > 0 {
		return a.MaxConcurrentTasks
	}
	return 5
}

// TaskTimeout returns the per-task timeout with a default of 5m.
func (a *AgentConfig) TaskTimeout() time.Duration {
	if a != nil && a.TaskTimeoutSeconds > 0 {
		return time.Duration(a.TaskTimeoutSeconds) * time.Second
	}
	return 5 * time.Minute
}

// HeartbeatInterval returns the heartbeat interval with a default of 30s.
func (a *AgentConfig) HeartbeatInterval() time.Duration {
	if a != nil && a.HeartbeatIntervalSeconds > 0 {
		return time.Duration(a.HeartbeatIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

// StaleClaim returns the stale claim duration with a default of 2m.
func (a *AgentConfig) StaleClaim() time.Duration {
	if a != nil && a.StaleClaimSeconds > 0 {
		return time.Duration(a.StaleClaimSeconds) * time.Second
	}
	return 2 * time.Minute
}

// SchedulerConfig configures the cron job scheduler.
// When nil, no cron jobs are executed. Requires database and orchestrator.
type SchedulerConfig struct {
	Enabled                bool `json:"enabled" yaml:"enabled"`
	PollIntervalSeconds    int  `json:"poll_interval_seconds" yaml:"poll_interval_seconds"`         // Default: 30.
	MaxConcurrentJobs      int  `json:"max_concurrent_jobs" yaml:"max_concurrent_jobs"`             // Default: 5.
	MissedJobWindowSeconds int  `json:"missed_job_window_seconds" yaml:"missed_job_window_seconds"` // Default: 3600 (1 hour).
}

// PollInterval returns the poll interval with a default of 30s.
func (s *SchedulerConfig) PollInterval() time.Duration {
	if s != nil && s.PollIntervalSeconds > 0 {
		return time.Duration(s.PollIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

// MaxConcurrent returns the max concurrent jobs with a default of 5.
func (s *SchedulerConfig) MaxConcurrent() int {
	if s != nil && s.MaxConcurrentJobs > 0 {
		return s.MaxConcurrentJobs
	}
	return 5
}

// MissedJobWindow returns the window for recovering missed executions.
// Jobs missed more than this duration ago are skipped. Default: 1 hour.
func (s *SchedulerConfig) MissedJobWindow() time.Duration {
	if s != nil && s.MissedJobWindowSeconds > 0 {
		return time.Duration(s.MissedJobWindowSeconds) * time.Second
	}
	return 1 * time.Hour
}

// HeartbeatTasksConfig configures the periodic heartbeat task executor.
// When nil, no heartbeat tasks are loaded or executed.
type HeartbeatTasksConfig struct {
	Enabled                 bool     `json:"enabled" yaml:"enabled"`
	TasksDirs               []string `json:"tasks_dirs" yaml:"tasks_dirs"`                                 // Paths to directories containing heartbeat task Markdown files.
	PollIntervalSeconds     int      `json:"poll_interval_seconds" yaml:"poll_interval_seconds"`           // How often to check for due tasks. Default: 30.
	FilePollIntervalSeconds int      `json:"file_poll_interval_seconds" yaml:"file_poll_interval_seconds"` // How often to re-scan Markdown files for changes. Default: 60.
	MaxConcurrentTasks      int      `json:"max_concurrent_tasks" yaml:"max_concurrent_tasks"`             // Maximum tasks executing in parallel. Default: 3.
	QuickTaskTimeoutSeconds int      `json:"quick_task_timeout_seconds" yaml:"quick_task_timeout_seconds"` // Timeout for quick-mode tasks. Default: 60.
	LongTaskTimeoutSeconds  int      `json:"long_task_timeout_seconds" yaml:"long_task_timeout_seconds"`   // Timeout for long-mode tasks. Default: 300.
	DefaultUserID           string   `json:"default_user_id" yaml:"default_user_id"`                       // Fallback user ID if not specified in file. Default: "system".
	ProactiveAlerts         bool     `json:"proactive_alerts" yaml:"proactive_alerts"`                     // Send proactive notifications on task failure.
}

// PollInterval returns the due-task poll interval with a default of 30s.
func (h *HeartbeatTasksConfig) PollInterval() time.Duration {
	if h != nil && h.PollIntervalSeconds > 0 {
		return time.Duration(h.PollIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

// FilePollInterval returns the file scan interval with a default of 60s.
func (h *HeartbeatTasksConfig) FilePollInterval() time.Duration {
	if h != nil && h.FilePollIntervalSeconds > 0 {
		return time.Duration(h.FilePollIntervalSeconds) * time.Second
	}
	return 60 * time.Second
}

// MaxConcurrent returns max concurrent tasks with a default of 3.
func (h *HeartbeatTasksConfig) MaxConcurrent() int {
	if h != nil && h.MaxConcurrentTasks > 0 {
		return h.MaxConcurrentTasks
	}
	return 3
}

// QuickTaskTimeout returns the quick task timeout with a default of 60s.
func (h *HeartbeatTasksConfig) QuickTaskTimeout() time.Duration {
	if h != nil && h.QuickTaskTimeoutSeconds > 0 {
		return time.Duration(h.QuickTaskTimeoutSeconds) * time.Second
	}
	return 60 * time.Second
}

// LongTaskTimeout returns the long task timeout with a default of 5m.
func (h *HeartbeatTasksConfig) LongTaskTimeout() time.Duration {
	if h != nil && h.LongTaskTimeoutSeconds > 0 {
		return time.Duration(h.LongTaskTimeoutSeconds) * time.Second
	}
	return 5 * time.Minute
}

// DefaultUser returns the default user ID with a default of "system".
func (h *HeartbeatTasksConfig) DefaultUser() string {
	if h != nil && h.DefaultUserID != "" {
		return h.DefaultUserID
	}
	return "system"
}

// MemoryConfig configures persistent conversation memory.
// When nil, conversations are ephemeral (lost on restart).
type MemoryConfig struct {
	Enabled             bool `json:"enabled" yaml:"enabled"`
	MaxHistoryMessages  int  `json:"max_history_messages" yaml:"max_history_messages"`   // Default: 100
	MaxMessageBytes     int  `json:"max_message_bytes" yaml:"max_message_bytes"`         // Default: 32768
	SummarizeOnTruncate bool `json:"summarize_on_truncate" yaml:"summarize_on_truncate"` // Summarize old messages instead of dropping them.
}

// MaxHistory returns the max history messages with a default of 100.
func (m *MemoryConfig) MaxHistory() int {
	if m != nil && m.MaxHistoryMessages > 0 {
		return m.MaxHistoryMessages
	}
	return 100
}

// MaxMsgBytes returns the max message bytes with a default of 32768.
func (m *MemoryConfig) MaxMsgBytes() int {
	if m != nil && m.MaxMessageBytes > 0 {
		return m.MaxMessageBytes
	}
	return 32768
}

// InfrastructureConfig configures the infrastructure memory subsystem.
// When nil, infra_lookup/infra_exec/infra_manage tools are not registered.
type InfrastructureConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// SecretsConfig configures the secret provider chain.
// When nil, only environment variable-based secrets are available.
type SecretsConfig struct {
	Providers []SecretProviderConfig `json:"providers" yaml:"providers"` // Tried in order.
}

// SecretProviderConfig configures a single secret provider backend.
type SecretProviderConfig struct {
	Type   string            `json:"type" yaml:"type"`                         // "env", "vault" (future: "aws_secrets_manager").
	Config map[string]string `json:"config,omitempty" yaml:"config,omitempty"` // Backend-specific configuration.
}

// NotificationConfig configures the notification dispatch subsystem.
// When nil, no notification features are available.
type NotificationConfig struct {
	Enabled  bool            `json:"enabled" yaml:"enabled"`
	Email    *EmailConfig    `json:"email,omitempty" yaml:"email,omitempty"`       // nil = email notifications disabled.
	WhatsApp *WhatsAppConfig `json:"whatsapp,omitempty" yaml:"whatsapp,omitempty"` // nil = WhatsApp notifications disabled.
	Signal   *SignalConfig   `json:"signal,omitempty" yaml:"signal,omitempty"`     // nil = Signal notifications disabled.
}

// EmailConfig configures the SMTP sender for email notifications.
// SMTP password should be stored in a secret provider, referenced via CredentialRef
// on individual NotificationChannel records, NOT in this config.
type EmailConfig struct {
	Host     string `json:"host" yaml:"host"`
	Port     int    `json:"port" yaml:"port"` // Default: 587.
	Username string `json:"username" yaml:"username"`
	From     string `json:"from" yaml:"from"`
	TLS      bool   `json:"tls" yaml:"tls"` // Default: true.
}

// WhatsAppConfig configures the WhatsApp Cloud API sender.
// Access token can be set here or via WHATSAPP_ACCESS_TOKEN env var.
// Per-channel overrides are supported via CredentialRef on NotificationChannel records.
type WhatsAppConfig struct {
	AccessToken string `json:"access_token,omitempty" yaml:"access_token,omitempty"` // Override: WHATSAPP_ACCESS_TOKEN env var.
}

// SignalConfig configures the Signal notification sender via signal-cli-rest-api.
// API URL and sender number can be set here or via SIGNAL_API_URL / SIGNAL_SENDER_NUMBER env vars.
type SignalConfig struct {
	APIURL       string `json:"api_url" yaml:"api_url"`             // Base URL of signal-cli-rest-api (e.g. "http://localhost:8080").
	SenderNumber string `json:"sender_number" yaml:"sender_number"` // Registered Signal phone number (e.g. "+1234567890").
}

// AlertingConfig configures the alert rule checker.
// When nil, no alert checks are executed. Requires notification and database.
type AlertingConfig struct {
	Enabled             bool `json:"enabled" yaml:"enabled"`
	PollIntervalSeconds int  `json:"poll_interval_seconds" yaml:"poll_interval_seconds"`       // Default: 30.
	MaxConcurrentChecks int  `json:"max_concurrent_checks" yaml:"max_concurrent_checks"`       // Default: 10.
	DefaultCooldownS    int  `json:"default_cooldown_seconds" yaml:"default_cooldown_seconds"` // Default: 300.
}

// AlertPollInterval returns the poll interval with a default of 30s.
func (a *AlertingConfig) AlertPollInterval() time.Duration {
	if a != nil && a.PollIntervalSeconds > 0 {
		return time.Duration(a.PollIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

// AlertMaxConcurrent returns the max concurrent checks with a default of 10.
func (a *AlertingConfig) AlertMaxConcurrent() int {
	if a != nil && a.MaxConcurrentChecks > 0 {
		return a.MaxConcurrentChecks
	}
	return 10
}

// AlertDefaultCooldown returns the default cooldown with a default of 300s.
func (a *AlertingConfig) AlertDefaultCooldown() int {
	if a != nil && a.DefaultCooldownS > 0 {
		return a.DefaultCooldownS
	}
	return 300
}

// PoliciesConfig defines capability-based security policies for agents and roles.
// When nil, no policy enforcement is applied (backward compatible).
type PoliciesConfig struct {
	Policies []PolicyConfig    `json:"policies" yaml:"policies"`
	Bindings map[string]string `json:"bindings" yaml:"bindings"` // agent_id or role → policy name.
}

// PolicyConfig defines a single security policy.
type PolicyConfig struct {
	Name            string   `json:"name" yaml:"name"`
	AllowedTools    []string `json:"allowed_tools" yaml:"allowed_tools"`
	DeniedTools     []string `json:"denied_tools" yaml:"denied_tools"`
	AllowedPaths    []string `json:"allowed_paths" yaml:"allowed_paths"`
	DeniedPaths     []string `json:"denied_paths" yaml:"denied_paths"`
	AllowedDomains  []string `json:"allowed_domains" yaml:"allowed_domains"`
	DeniedDomains   []string `json:"denied_domains" yaml:"denied_domains"`
	AllowedCommands []string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands  []string `json:"denied_commands" yaml:"denied_commands"`
	MaxRiskLevel    string   `json:"max_risk_level" yaml:"max_risk_level"`
	RequireApproval []string `json:"require_approval" yaml:"require_approval"`
	AllowedSkills   []string `json:"allowed_skills" yaml:"allowed_skills"`
	DeniedSkills    []string `json:"denied_skills" yaml:"denied_skills"`
}

type SecurityConfig struct {
	DefaultPermissions []string              `json:"default_permissions" yaml:"default_permissions"`
	RequireApproval    []string              `json:"require_approval" yaml:"require_approval"`
	AuditLogPath       string                `json:"audit_log_path" yaml:"audit_log_path"`
	Roles              map[string]RoleConfig `json:"roles" yaml:"roles"`
	UserRoles          map[string]string     `json:"user_roles" yaml:"user_roles"`
	DefaultRole        string                `json:"default_role" yaml:"default_role"`
}

// RoleConfig defines a role's permissions and risk thresholds.
type RoleConfig struct {
	Permissions     []string `json:"permissions" yaml:"permissions"`
	MaxRiskLevel    string   `json:"max_risk_level" yaml:"max_risk_level"`
	RequireApproval []string `json:"require_approval" yaml:"require_approval"`
}

type BudgetConfig struct {
	DefaultLimitUSD float64 `json:"default_limit_usd" yaml:"default_limit_usd"`
}

type SandboxConfig struct {
	Type                string              `json:"type" yaml:"type"` // "process" or "docker"
	MaxCPUCores         int                 `json:"max_cpu_cores" yaml:"max_cpu_cores"`
	MaxMemoryMB         int                 `json:"max_memory_mb" yaml:"max_memory_mb"`
	MaxExecutionSeconds int                 `json:"max_execution_seconds" yaml:"max_execution_seconds"`
	NetworkAllowed      bool                `json:"network_allowed" yaml:"network_allowed"`
	Docker              DockerSandboxConfig `json:"docker" yaml:"docker"`
}

// DockerSandboxConfig holds Docker-specific sandbox settings.
type DockerSandboxConfig struct {
	Image     string  `json:"image" yaml:"image"`           // Container image (e.g. "akili-runtime:latest").
	CPUCores  float64 `json:"cpu_cores" yaml:"cpu_cores"`   // Docker --cpus flag (e.g. 0.5). 0 = 1.0 default.
	PIDsLimit int     `json:"pids_limit" yaml:"pids_limit"` // Docker --pids-limit flag. 0 = 64 default.
}

// ToolsConfig configures individual tool settings.
type ToolsConfig struct {
	File     FileToolConfig     `json:"file" yaml:"file"`
	Web      WebToolConfig      `json:"web" yaml:"web"`
	Browser  BrowserToolConfig  `json:"browser" yaml:"browser"`
	Code     CodeToolConfig     `json:"code" yaml:"code"`
	Database DatabaseToolConfig `json:"database" yaml:"database"`
	Git      GitToolConfig      `json:"git" yaml:"git"`
	MCP      []MCPServerConfig  `json:"mcp,omitempty" yaml:"mcp,omitempty"` // External MCP tool servers.
}

// BrowserToolConfig restricts browser tool access.
type BrowserToolConfig struct {
	AllowedDomains        []string `json:"allowed_domains" yaml:"allowed_domains"`
	MaxResponseBytes      int64    `json:"max_response_bytes" yaml:"max_response_bytes"`             // Default: 10 MB.
	TimeoutSeconds        int      `json:"timeout_seconds" yaml:"timeout_seconds"`                   // Default: 30.
	MaxNavigationsPerCall int      `json:"max_navigations_per_call" yaml:"max_navigations_per_call"` // Default: 5.
}

// MCPServerConfig defines a single external MCP server connection.
// Akili acts as an MCP client, connecting at startup, discovering tools,
// and registering them in the tool registry with the configured security settings.
type MCPServerConfig struct {
	Name       string            `json:"name" yaml:"name"`                                 // Server ID used for tool namespacing (e.g., "github").
	Transport  string            `json:"transport" yaml:"transport"`                       // "stdio", "sse", or "streamable_http".
	Command    string            `json:"command,omitempty" yaml:"command,omitempty"`       // Executable to launch (stdio only).
	Args       []string          `json:"args,omitempty" yaml:"args,omitempty"`             // Command arguments (stdio only).
	Env        map[string]string `json:"env,omitempty" yaml:"env,omitempty"`               // Subprocess env vars (stdio only). Values support ${VAR} expansion.
	URL        string            `json:"url,omitempty" yaml:"url,omitempty"`               // Server endpoint (sse/streamable_http only).
	Headers    map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`       // HTTP headers (sse/streamable_http). Values support ${VAR} expansion.
	Permission string            `json:"permission,omitempty" yaml:"permission,omitempty"` // RBAC action name. Default: "mcp__<name>".
	RiskLevel  string            `json:"risk_level,omitempty" yaml:"risk_level,omitempty"` // "low", "medium", "high", "critical". Default: "medium".
}

// FileToolConfig restricts file access to specific paths.
type FileToolConfig struct {
	AllowedPaths     []string `json:"allowed_paths" yaml:"allowed_paths"`
	MaxFileSizeBytes int64    `json:"max_file_size_bytes" yaml:"max_file_size_bytes"`
}

// WebToolConfig restricts web access to specific domains.
type WebToolConfig struct {
	AllowedDomains   []string `json:"allowed_domains" yaml:"allowed_domains"`
	MaxResponseBytes int64    `json:"max_response_bytes" yaml:"max_response_bytes"`
	TimeoutSeconds   int      `json:"timeout_seconds" yaml:"timeout_seconds"`
}

// CodeToolConfig restricts which languages can be executed.
type CodeToolConfig struct {
	AllowedLanguages []string `json:"allowed_languages" yaml:"allowed_languages"`
}

// DatabaseToolConfig configures the read-only database query tool.
type DatabaseToolConfig struct {
	DSN            string `json:"dsn" yaml:"dsn"`                         // Connection string. Can be overridden by AKILI_TOOL_DB_DSN env var.
	MaxRows        int    `json:"max_rows" yaml:"max_rows"`               // Maximum rows per query. Default: 1000.
	TimeoutSeconds int    `json:"timeout_seconds" yaml:"timeout_seconds"` // Per-query timeout. Default: 30.
}

// GitToolConfig configures the git tools.
type GitToolConfig struct {
	AllowedPaths []string `json:"allowed_paths" yaml:"allowed_paths"` // Restrict git operations to these repo paths.
}

// GatewaysConfig defines which gateways are enabled and their settings.
// Nil pointers mean the gateway is not configured. If the entire section
// is absent, the CLI gateway is enabled by default for backward compatibility.
type GatewaysConfig struct {
	CLI       *CLIGatewayConfig       `json:"cli,omitempty" yaml:"cli,omitempty"`
	HTTP      *HTTPGatewayConfig      `json:"http,omitempty" yaml:"http,omitempty"`
	Slack     *SlackGatewayConfig     `json:"slack,omitempty" yaml:"slack,omitempty"`
	Telegram  *TelegramGatewayConfig  `json:"telegram,omitempty" yaml:"telegram,omitempty"`
	Signal    *SignalGatewayConfig    `json:"signal,omitempty" yaml:"signal,omitempty"`
	WebSocket *WebSocketGatewayConfig `json:"websocket,omitempty" yaml:"websocket,omitempty"` // WebSocket server for remote agents.
}

// WebSocketGatewayConfig configures the WebSocket server for agent connections.
type WebSocketGatewayConfig struct {
	Enabled                    bool   `json:"enabled" yaml:"enabled"`
	ListenAddr                 string `json:"listen_addr,omitempty" yaml:"listen_addr,omitempty"`                 // Standalone listen address (when HTTP gateway is disabled). Default: ":8081".
	Path                       string `json:"path" yaml:"path"`                                                   // URL path for WebSocket endpoint. Default: "/ws/agents".
	AgentToken                 string `json:"agent_token" yaml:"agent_token"`                                     // Shared token for agent authentication.
	HeartbeatIntervalSeconds   int    `json:"heartbeat_interval_seconds" yaml:"heartbeat_interval_seconds"`       // Default: 30.
	TaskReassignTimeoutSeconds int    `json:"task_reassign_timeout_seconds" yaml:"task_reassign_timeout_seconds"` // Default: 120.
}

// WSPath returns the WebSocket path with a default of "/ws/agents".
func (w *WebSocketGatewayConfig) WSPath() string {
	if w != nil && w.Path != "" {
		return w.Path
	}
	return "/ws/agents"
}

// WSHeartbeatInterval returns the heartbeat interval with a default of 30s.
func (w *WebSocketGatewayConfig) WSHeartbeatInterval() time.Duration {
	if w != nil && w.HeartbeatIntervalSeconds > 0 {
		return time.Duration(w.HeartbeatIntervalSeconds) * time.Second
	}
	return 30 * time.Second
}

// WSTaskReassignTimeout returns the task reassign timeout with a default of 120s.
func (w *WebSocketGatewayConfig) WSTaskReassignTimeout() time.Duration {
	if w != nil && w.TaskReassignTimeoutSeconds > 0 {
		return time.Duration(w.TaskReassignTimeoutSeconds) * time.Second
	}
	return 120 * time.Second
}

// CLIGatewayConfig configures the interactive CLI gateway.
type CLIGatewayConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

// HTTPGatewayConfig configures the HTTP API gateway.
type HTTPGatewayConfig struct {
	Enabled             bool              `json:"enabled" yaml:"enabled"`
	EnableDocs          bool              `json:"enable_docs" yaml:"enable_docs"`
	ListenAddr          string            `json:"listen_addr" yaml:"listen_addr"`
	MaxRequestSizeBytes int64             `json:"max_request_size_bytes" yaml:"max_request_size_bytes"`
	APIKeyUserMapping   map[string]string `json:"api_key_user_mapping" yaml:"api_key_user_mapping"` // API key hash → user ID.
	RateLimit           RateLimitConfig   `json:"rate_limit" yaml:"rate_limit"`
	SSE                 bool              `json:"sse" yaml:"sse"` // Enable SSE streaming endpoint.
}

// SlackGatewayConfig configures the Slack gateway.
// Signing secret and bot token can be set here or via SLACK_SIGNING_SECRET / SLACK_BOT_TOKEN env vars.
// Environment variables take precedence over config values.
type SlackGatewayConfig struct {
	Enabled         bool              `json:"enabled" yaml:"enabled"`
	SigningSecret   string            `json:"signing_secret,omitempty" yaml:"signing_secret,omitempty"` // Override: SLACK_SIGNING_SECRET env var.
	BotToken        string            `json:"bot_token,omitempty" yaml:"bot_token,omitempty"`           // Override: SLACK_BOT_TOKEN env var.
	ListenAddr      string            `json:"listen_addr" yaml:"listen_addr"`
	UserMapping     map[string]string `json:"user_mapping" yaml:"user_mapping"`
	DefaultUserRole string            `json:"default_user_role" yaml:"default_user_role"`
	ApprovalChannel string            `json:"approval_channel" yaml:"approval_channel"`
	RateLimit       RateLimitConfig   `json:"rate_limit" yaml:"rate_limit"`
}

// TelegramGatewayConfig configures the Telegram gateway.
// Bot token can be set here or via TELEGRAM_BOT_TOKEN env var.
// Environment variable takes precedence over config value.
type TelegramGatewayConfig struct {
	Enabled            bool              `json:"enabled" yaml:"enabled"`
	BotToken           string            `json:"bot_token,omitempty" yaml:"bot_token,omitempty"` // Override: TELEGRAM_BOT_TOKEN env var.
	WebhookURL         string            `json:"webhook_url" yaml:"webhook_url"`
	ListenAddr         string            `json:"listen_addr" yaml:"listen_addr"`
	AllowedUsers       []int64           `json:"allowed_users" yaml:"allowed_users"`
	UserMapping        map[string]string `json:"user_mapping" yaml:"user_mapping"`
	PollTimeoutSeconds int               `json:"poll_timeout_seconds" yaml:"poll_timeout_seconds"`
	RateLimit          RateLimitConfig   `json:"rate_limit" yaml:"rate_limit"`
}

// SignalGatewayConfig configures the Signal gateway via signal-cli-rest-api.
// API URL and sender number can be set here or via SIGNAL_GATEWAY_API_URL / SIGNAL_GATEWAY_SENDER_NUMBER env vars.
// Environment variables take precedence over config values.
type SignalGatewayConfig struct {
	Enabled             bool              `json:"enabled" yaml:"enabled"`
	APIURL              string            `json:"api_url" yaml:"api_url"`                             // Override: SIGNAL_GATEWAY_API_URL env var.
	SenderNumber        string            `json:"sender_number" yaml:"sender_number"`                 // Override: SIGNAL_GATEWAY_SENDER_NUMBER env var.
	AllowedUsers        []string          `json:"allowed_users" yaml:"allowed_users"`                 // Phone numbers allowed to interact. Empty = deny all.
	UserMapping         map[string]string `json:"user_mapping" yaml:"user_mapping"`                   // Signal phone → Akili user ID.
	PollIntervalSeconds int               `json:"poll_interval_seconds" yaml:"poll_interval_seconds"` // Receive poll interval. Default: 2.
	RateLimit           RateLimitConfig   `json:"rate_limit" yaml:"rate_limit"`
}

// RateLimitConfig configures per-user rate limiting for a gateway.
type RateLimitConfig struct {
	RequestsPerMinute int `json:"requests_per_minute" yaml:"requests_per_minute"`
	BurstSize         int `json:"burst_size" yaml:"burst_size"`
}

// ApprovalConfig configures the approval workflow.
type ApprovalConfig struct {
	TTLSeconds   int                 `json:"ttl_seconds" yaml:"ttl_seconds"` // How long approvals are valid. 0 = 300s (5 min).
	AutoApproval *AutoApprovalConfig `json:"auto_approval,omitempty" yaml:"auto_approval,omitempty"`
}

// AutoApprovalConfig controls pattern-based automatic approval.
type AutoApprovalConfig struct {
	Enabled           bool     `json:"enabled" yaml:"enabled"`
	MaxAutoApprovals  int      `json:"max_auto_approvals" yaml:"max_auto_approvals"` // Per user per hour. Default: 10.
	AllowedTools      []string `json:"allowed_tools" yaml:"allowed_tools"`           // Tools eligible for auto-approval.
	RequiredApprovals int      `json:"required_approvals" yaml:"required_approvals"` // Manual approvals before auto. Default: 3.
	WindowHours       int      `json:"window_hours" yaml:"window_hours"`             // Lookback window. Default: 24.
}

type ProvidersConfig struct {
	Default   string          `json:"default" yaml:"default"`                       // "anthropic", "openai", "gemini", "ollama". Empty = "anthropic".
	Fallback  []string        `json:"fallback,omitempty" yaml:"fallback,omitempty"` // Fallback providers tried in order when default fails.
	Anthropic AnthropicConfig `json:"anthropic" yaml:"anthropic"`
	OpenAI    OpenAIConfig    `json:"openai" yaml:"openai"`
	Gemini    GeminiConfig    `json:"gemini" yaml:"gemini"`
	Ollama    OllamaConfig    `json:"ollama" yaml:"ollama"`
}

type AnthropicConfig struct {
	APIKey string `json:"api_key" yaml:"api_key"`
	Model  string `json:"model" yaml:"model"`
}

type OpenAIConfig struct {
	APIKey  string `json:"api_key" yaml:"api_key"`
	Model   string `json:"model" yaml:"model"`
	BaseURL string `json:"base_url" yaml:"base_url"` // Optional. Defaults to https://api.openai.com.
}

type GeminiConfig struct {
	APIKey  string `json:"api_key" yaml:"api_key"`
	Model   string `json:"model" yaml:"model"`
	BaseURL string `json:"base_url" yaml:"base_url"` // Optional. Defaults to https://generativelanguage.googleapis.com.
}

type OllamaConfig struct {
	Model   string `json:"model" yaml:"model"`
	BaseURL string `json:"base_url" yaml:"base_url"` // Optional. Defaults to http://localhost:11434.
}

// DefaultConfigPath returns the default config file path (~/.akili/config.json).
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "configs/akili.json" // fallback for environments without a home dir
	}
	return filepath.Join(home, ".akili", "config.json")
}

// Load reads a JSON or YAML config file and returns a validated Config.
// The format is detected by file extension: .yml/.yaml for YAML, everything else for JSON.
// Provider API keys and gateway tokens can be set in the config file or overridden
// by environment variables. Environment variables take precedence.
func Load(path string) (*Config, error) {
	// Expand ~ in config path.
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path %s: %w", path, err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", resolved, err)
	}

	var cfg Config
	switch ext := strings.ToLower(filepath.Ext(resolved)); ext {
	case ".yml", ".yaml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing YAML config %s: %w", resolved, err)
		}
	default:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parsing JSON config %s: %w", resolved, err)
		}
	}

	// Environment variable overrides — env vars take precedence over config values.
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		cfg.Providers.Anthropic.APIKey = envKey
	}
	if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
		cfg.Providers.OpenAI.APIKey = envKey
	}
	if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" {
		cfg.Providers.Gemini.APIKey = envKey
	}

	// Workspace override from environment.
	if envWS := os.Getenv("AKILI_WORKSPACE"); envWS != "" {
		cfg.Workspace = envWS
	}

	// Data directory override from environment.
	if envDD := os.Getenv("AKILI_DATA_DIR"); envDD != "" {
		cfg.DataDir = envDD
	}

	// Gateway token overrides from environment.
	if envKey := os.Getenv("SLACK_SIGNING_SECRET"); envKey != "" {
		if cfg.Gateways.Slack == nil {
			cfg.Gateways.Slack = &SlackGatewayConfig{}
		}
		cfg.Gateways.Slack.SigningSecret = envKey
	}
	if envKey := os.Getenv("SLACK_BOT_TOKEN"); envKey != "" {
		if cfg.Gateways.Slack == nil {
			cfg.Gateways.Slack = &SlackGatewayConfig{}
		}
		cfg.Gateways.Slack.BotToken = envKey
	}
	if envKey := os.Getenv("TELEGRAM_BOT_TOKEN"); envKey != "" {
		if cfg.Gateways.Telegram == nil {
			cfg.Gateways.Telegram = &TelegramGatewayConfig{}
		}
		cfg.Gateways.Telegram.BotToken = envKey
	}

	// WhatsApp notification sender override.
	if envKey := os.Getenv("WHATSAPP_ACCESS_TOKEN"); envKey != "" {
		if cfg.Notification == nil {
			cfg.Notification = &NotificationConfig{}
		}
		if cfg.Notification.WhatsApp == nil {
			cfg.Notification.WhatsApp = &WhatsAppConfig{}
		}
		cfg.Notification.WhatsApp.AccessToken = envKey
	}

	// Signal notification sender overrides.
	if envKey := os.Getenv("SIGNAL_API_URL"); envKey != "" {
		if cfg.Notification == nil {
			cfg.Notification = &NotificationConfig{}
		}
		if cfg.Notification.Signal == nil {
			cfg.Notification.Signal = &SignalConfig{}
		}
		cfg.Notification.Signal.APIURL = envKey
	}
	if envKey := os.Getenv("SIGNAL_SENDER_NUMBER"); envKey != "" {
		if cfg.Notification != nil && cfg.Notification.Signal != nil {
			cfg.Notification.Signal.SenderNumber = envKey
		}
	}

	// Signal gateway overrides.
	if envKey := os.Getenv("SIGNAL_GATEWAY_API_URL"); envKey != "" {
		if cfg.Gateways.Signal == nil {
			cfg.Gateways.Signal = &SignalGatewayConfig{}
		}
		cfg.Gateways.Signal.APIURL = envKey
	}
	if envKey := os.Getenv("SIGNAL_GATEWAY_SENDER_NUMBER"); envKey != "" {
		if cfg.Gateways.Signal == nil {
			cfg.Gateways.Signal = &SignalGatewayConfig{}
		}
		cfg.Gateways.Signal.SenderNumber = envKey
	}

	// Resolve DataDir default.
	if cfg.DataDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			cfg.DataDir = filepath.Join(home, ".akili", "data")
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// resolvePath expands ~ to the user home directory and returns an absolute path.
func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	return filepath.Abs(path)
}

// HasDatabase returns true if any database backend (SQLite or PostgreSQL) is configured.
// This replaces the old check of `c.Database != nil`.
func (c *Config) HasDatabase() bool {
	// Always true now — SQLite is the default.
	return true
}

// ResolvedDataDir returns the data directory, resolving ~ if needed.
func (c *Config) ResolvedDataDir() string {
	if c.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "data"
		}
		return filepath.Join(home, ".akili", "data")
	}
	resolved, err := resolvePath(c.DataDir)
	if err != nil {
		return c.DataDir
	}
	return resolved
}

// DatabasePath returns the default SQLite database path under the data directory.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.ResolvedDataDir(), "akili.db")
}

// AuditLogPath returns the default audit log path under the data directory.
func (c *Config) AuditLogPath() string {
	return filepath.Join(c.ResolvedDataDir(), "audit.jsonl")
}

// StorageDriverName returns the effective storage driver name.
func (c *Config) StorageDriverName() string {
	if c.Storage != nil {
		return c.Storage.StorageDriver()
	}
	return "sqlite"
}

func (c *Config) validate() error {
	// Default provider to anthropic for backward compatibility.
	if c.Providers.Default == "" {
		c.Providers.Default = "anthropic"
	}
	if err := c.validateProvider(); err != nil {
		return err
	}
	if c.Budget.DefaultLimitUSD <= 0 {
		return fmt.Errorf("budget.default_limit_usd must be positive")
	}
	// audit_log_path is now optional — derived from workspace if empty.
	// Kept for backward compat: if set, it's used as-is.
	if c.Security.DefaultRole != "" {
		if _, ok := c.Security.Roles[c.Security.DefaultRole]; !ok {
			return fmt.Errorf("security.default_role %q not found in roles", c.Security.DefaultRole)
		}
	}
	for name, role := range c.Security.Roles {
		if role.MaxRiskLevel == "" {
			return fmt.Errorf("security.roles.%s.max_risk_level is required", name)
		}
	}
	if c.Sandbox.MaxMemoryMB < 0 {
		return fmt.Errorf("sandbox.max_memory_mb must not be negative")
	}
	if c.Sandbox.MaxExecutionSeconds < 0 {
		return fmt.Errorf("sandbox.max_execution_seconds must not be negative")
	}
	// Agent mode: WebSocket-based agents need a gateway_url. Legacy DB-polling agents need postgres + orchestrator.
	if c.Runtime != nil && c.Runtime.Mode == "agent" {
		if c.Runtime.Agent != nil && c.Runtime.Agent.UseWebSocket() {
			// WebSocket mode — no database required on the agent side.
			if c.Runtime.Agent.GatewayURL == "" {
				return fmt.Errorf("runtime.agent.gateway_url is required for WebSocket agent mode")
			}
		} else {

			if c.Orchestrator == nil || !c.Orchestrator.Enabled {
				return fmt.Errorf("runtime.mode=agent requires orchestrator to be enabled")
			}
		}
	}
	// Storage driver validation.
	if c.Storage != nil && c.Storage.Driver != "" {
		switch c.Storage.Driver {
		case "sqlite", "postgres":
			// valid
		default:
			return fmt.Errorf("storage.driver %q is not supported (use sqlite or postgres)", c.Storage.Driver)
		}
	}
	// Scheduler requires orchestrator.
	if c.Scheduler != nil && c.Scheduler.Enabled {
		if c.Orchestrator == nil || !c.Orchestrator.Enabled {
			return fmt.Errorf("scheduler requires orchestrator to be enabled")
		}
	}
	// Alerting requires notification.
	if c.Alerting != nil && c.Alerting.Enabled {
		if c.Notification == nil || !c.Notification.Enabled {
			return fmt.Errorf("alerting requires notification to be enabled")
		}
	}
	// Heartbeat tasks require at least one tasks directory.
	if c.HeartbeatTasks != nil && c.HeartbeatTasks.Enabled {
		if len(c.HeartbeatTasks.TasksDirs) == 0 {
			return fmt.Errorf("heartbeat_tasks.tasks_dirs must contain at least one directory when enabled")
		}
	}
	// MCP server config validation.
	mcpNames := make(map[string]bool, len(c.Tools.MCP))
	for i, srv := range c.Tools.MCP {
		if srv.Name == "" {
			return fmt.Errorf("tools.mcp[%d].name is required", i)
		}
		if mcpNames[srv.Name] {
			return fmt.Errorf("tools.mcp[%d]: duplicate server name %q", i, srv.Name)
		}
		mcpNames[srv.Name] = true
		switch srv.Transport {
		case "stdio":
			if srv.Command == "" {
				return fmt.Errorf("tools.mcp[%d] (%q): command is required for stdio transport", i, srv.Name)
			}
		case "sse", "streamable_http":
			if srv.URL == "" {
				return fmt.Errorf("tools.mcp[%d] (%q): url is required for %s transport", i, srv.Name, srv.Transport)
			}
		default:
			return fmt.Errorf("tools.mcp[%d] (%q): transport must be stdio, sse, or streamable_http", i, srv.Name)
		}
	}
	return nil
}

// validateProvider checks that the selected LLM provider has the required fields.
func (c *Config) validateProvider() error {
	switch c.Providers.Default {
	case "anthropic":
		if c.Providers.Anthropic.Model == "" {
			return fmt.Errorf("providers.anthropic.model is required")
		}
		if c.Providers.Anthropic.APIKey == "" {
			return fmt.Errorf("providers.anthropic.api_key is required (set ANTHROPIC_API_KEY env var)")
		}
	case "openai":
		if c.Providers.OpenAI.Model == "" {
			return fmt.Errorf("providers.openai.model is required")
		}
		if c.Providers.OpenAI.APIKey == "" {
			return fmt.Errorf("providers.openai.api_key is required (set OPENAI_API_KEY env var)")
		}
	case "gemini":
		if c.Providers.Gemini.Model == "" {
			return fmt.Errorf("providers.gemini.model is required")
		}
		if c.Providers.Gemini.APIKey == "" {
			return fmt.Errorf("providers.gemini.api_key is required (set GEMINI_API_KEY env var)")
		}
	case "ollama":
		if c.Providers.Ollama.Model == "" {
			return fmt.Errorf("providers.ollama.model is required")
		}
	default:
		return fmt.Errorf("providers.default %q is not supported (use anthropic, openai, gemini, or ollama)", c.Providers.Default)
	}
	return nil
}
