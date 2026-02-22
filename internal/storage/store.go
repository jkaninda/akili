// Package storage defines the unified Store interface that abstracts all persistence operations.
// Two backends are provided: SQLite (default, zero-config) and PostgreSQL (production/multi-tenant).
package storage

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/alerting"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/heartbeat"
	"github.com/jkaninda/akili/internal/heartbeattask"
	"github.com/jkaninda/akili/internal/identity"
	"github.com/jkaninda/akili/internal/infra"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/akili/internal/security"
)

// Store is the unified persistence interface for Akili.
// It provides access to all domain-specific sub-stores through accessor methods.
// Both SQLite and PostgreSQL backends implement this interface.
type Store interface {
	// Sub-store accessors — each returns a domain-specific store interface.
	// The returned stores share the same underlying connection/transaction scope.
	Conversations() agent.ConversationStore
	Workflows() orchestrator.WorkflowStore
	Skills() orchestrator.SkillStore
	CronJobs() scheduler.CronJobStore
	AlertRules() alerting.AlertRuleStore
	AlertHistory() alerting.AlertHistoryStore
	NotificationChannels() notification.ChannelStore
	InfraNodes() infra.Store
	Approvals() approval.ApprovalStore
	Identities() identity.IdentityStore
	Heartbeats() heartbeat.HeartbeatStore
	HeartbeatTasks() heartbeattask.HeartbeatTaskStore
	HeartbeatTaskResults() heartbeattask.HeartbeatTaskResultStore

	// Security sub-stores.
	Roles() security.RoleStore
	Budgets() security.BudgetStore
	Audit() security.AuditStore

	// Organization management.
	EnsureOrg(ctx context.Context, name string) (uuid.UUID, error)

	// AgentRegistry tracks WebSocket-connected agents (in-memory or persisted).
	RegisterAgent(ctx context.Context, agent *AgentInfo) error
	DeregisterAgent(ctx context.Context, agentID string) error
	ListAgents(ctx context.Context) ([]*AgentInfo, error)

	// Lifecycle.
	Migrate(ctx context.Context) error
	Close() error

	// Driver returns the storage driver name ("sqlite" or "postgres").
	Driver() string
}

// AgentInfo describes a connected agent in the registry.
type AgentInfo struct {
	AgentID     string    `json:"agent_id"`
	Skills      []string  `json:"skills"`
	Model       string    `json:"model"`
	MaxParallel int       `json:"max_parallel"`
	ActiveTasks int       `json:"active_tasks"`
	Version     string    `json:"version"`
	Status      string    `json:"status"` // "idle", "busy", "draining"
	ConnectedAt time.Time `json:"connected_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// Config holds storage configuration for driver selection.
type Config struct {
	Driver   string         `json:"driver"` // "sqlite" (default) or "postgres"
	SQLite   SQLiteConfig   `json:"sqlite"`
	Postgres PostgresConfig `json:"postgres"`
}

// SQLiteConfig holds SQLite-specific settings.
type SQLiteConfig struct {
	Path        string `json:"path,omitempty"` // Database file path. Default: derived from workspace.
	JournalMode string `json:"journal_mode"`   // "wal" (default), "delete", "truncate", etc.
}

// PostgresConfig holds PostgreSQL-specific settings.
type PostgresConfig struct {
	DSN              string `json:"dsn"`
	MaxOpenConns     int    `json:"max_open_conns"`
	MaxIdleConns     int    `json:"max_idle_conns"`
	ConnMaxLifetimeS int    `json:"conn_max_lifetime_s"`
	DefaultOrgName   string `json:"default_org_name"`
}

// DefaultDriver is the default storage driver.
const DefaultDriver = "sqlite"

// DriverSQLite is the SQLite driver name.
const DriverSQLite = "sqlite"

// DriverPostgres is the PostgreSQL driver name.
const DriverPostgres = "postgres"
