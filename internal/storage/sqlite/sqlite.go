// Package sqlite implements the unified Store interface using SQLite via GORM.
// Uses modernc.org/sqlite (pure Go, no CGO) through the glebarez/sqlite GORM driver.
//
// Key differences from the PostgreSQL backend:
//   - WAL mode enabled by default for concurrent reads
//   - BEGIN IMMEDIATE replaces SELECT FOR UPDATE SKIP LOCKED
//   - JSONB columns use TEXT type (SQLite stores JSON as text natively)
//   - No connection pooling (single file, WAL handles concurrency)
package sqlite

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

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
	"github.com/jkaninda/akili/internal/soul"
	"github.com/jkaninda/akili/internal/storage"
	pgstore "github.com/jkaninda/akili/internal/storage/postgres"
)

// Config holds SQLite-specific configuration.
type Config struct {
	Path        string // Database file path.
	JournalMode string // WAL mode by default.
}

// Store implements storage.Store backed by SQLite.
type Store struct {
	db     *gorm.DB
	logger *slog.Logger
	path   string

	// Sub-store instances (created lazily on first access).
	mu               sync.Mutex
	conversations    agent.ConversationStore
	workflows        orchestrator.WorkflowStore
	skills           orchestrator.SkillStore
	cronJobs         scheduler.CronJobStore
	alertRules       alerting.AlertRuleStore
	alertHistory     alerting.AlertHistoryStore
	channels         notification.ChannelStore
	infraNodes       infra.Store
	approvals        approval.ApprovalStore
	identities       identity.IdentityStore
	heartbeats       heartbeat.HeartbeatStore
	heartbeatTasks   heartbeattask.HeartbeatTaskStore
	heartbeatTaskRes heartbeattask.HeartbeatTaskResultStore
	roles            security.RoleStore
	budgets          security.BudgetStore
	audit            security.AuditStore
	soulStore        soul.SoulStore

	// In-memory agent registry (not persisted to SQLite).
	agentsMu sync.RWMutex
	agents   map[string]*storage.AgentInfo
}

// Open creates a new SQLite-backed Store.
func Open(cfg Config, slogger *slog.Logger) (*Store, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating database directory %s: %w", dir, err)
	}

	journalMode := cfg.JournalMode
	if journalMode == "" {
		journalMode = "wal"
	}

	// Build DSN with pragmas.
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(%s)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", cfg.Path, journalMode)

	gormLogger := logger.New(
		slogAdapter{slogger},
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:  gormLogger,
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	s := &Store{
		db:     db,
		logger: slogger,
		path:   cfg.Path,
		agents: make(map[string]*storage.AgentInfo),
	}

	slogger.Info("sqlite store opened", slog.String("path", cfg.Path), slog.String("journal_mode", journalMode))
	return s, nil
}

// Migrate runs GORM AutoMigrate to create/update tables.
// Uses the same models as the PostgreSQL backend.
func (s *Store) Migrate(_ context.Context) error {
	if err := s.db.AutoMigrate(
		&pgstore.OrgModel{},
		&pgstore.UserModel{},
		&pgstore.RoleModel{},
		&pgstore.PermissionModel{},
		&pgstore.UserRoleModel{},
		&pgstore.BudgetModel{},
		&pgstore.BudgetReservationModel{},
		&pgstore.ApprovalModel{},
		&pgstore.AuditEventModel{},
		&pgstore.WorkflowModel{},
		&pgstore.TaskModel{},
		&pgstore.AgentMessageModel{},
		&pgstore.AgentSkillModel{},
		&pgstore.CronJobModel{},
		&pgstore.ConversationModel{},
		&pgstore.ConversationMessageModel{},
		&pgstore.InfraNodeModel{},
		&pgstore.NotificationChannelModel{},
		&pgstore.AlertRuleModel{},
		&pgstore.AlertHistoryModel{},
		&pgstore.AgentIdentityModel{},
		&pgstore.AgentHeartbeatModel{},
		&pgstore.HeartbeatTaskModel{},
		&pgstore.HeartbeatTaskResultModel{},
		&pgstore.SoulEventModel{},
	); err != nil {
		return err
	}

	// Ensure indexes that AutoMigrate may not add on pre-existing tables.
	skillRepo := pgstore.NewSkillRepository(s.db)
	return skillRepo.EnsureSkillIndex()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Driver returns "sqlite".
func (s *Store) Driver() string {
	return storage.DriverSQLite
}

// GormDB returns the underlying GORM DB for sub-store construction.
func (s *Store) GormDB() *gorm.DB {
	return s.db
}

// EnsureOrg creates or retrieves an organization by name.
func (s *Store) EnsureOrg(ctx context.Context, name string) (uuid.UUID, error) {
	repo := pgstore.NewOrgRepository(s.db)
	return repo.EnsureDefaultOrg(ctx, name)
}

// --- Sub-store accessors ---
// All sub-stores reuse the existing PostgreSQL repository implementations
// since they operate on the same GORM models. GORM's SQLite dialect
// handles the SQL differences transparently.

func (s *Store) Conversations() agent.ConversationStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conversations == nil {
		s.conversations = pgstore.NewConversationRepository(s.db)
	}
	return s.conversations
}

func (s *Store) Workflows() orchestrator.WorkflowStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workflows == nil {
		s.workflows = pgstore.NewWorkflowRepository(s.db)
	}
	return s.workflows
}

func (s *Store) Skills() orchestrator.SkillStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skills == nil {
		s.skills = pgstore.NewSkillRepository(s.db)
	}
	return s.skills
}

func (s *Store) CronJobs() scheduler.CronJobStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cronJobs == nil {
		s.cronJobs = pgstore.NewCronJobRepository(s.db)
	}
	return s.cronJobs
}

func (s *Store) AlertRules() alerting.AlertRuleStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alertRules == nil {
		s.alertRules = pgstore.NewAlertRuleRepository(s.db)
	}
	return s.alertRules
}

func (s *Store) AlertHistory() alerting.AlertHistoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alertHistory == nil {
		s.alertHistory = pgstore.NewAlertHistoryRepository(s.db)
	}
	return s.alertHistory
}

func (s *Store) NotificationChannels() notification.ChannelStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channels == nil {
		s.channels = pgstore.NewNotificationChannelRepository(s.db)
	}
	return s.channels
}

func (s *Store) InfraNodes() infra.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infraNodes == nil {
		s.infraNodes = pgstore.NewInfraNodeRepository(s.db)
	}
	return s.infraNodes
}

func (s *Store) Approvals() approval.ApprovalStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvals == nil {
		s.approvals = pgstore.NewApprovalRepository(s.db)
	}
	return s.approvals
}

func (s *Store) Identities() identity.IdentityStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.identities == nil {
		s.identities = pgstore.NewIdentityRepository(s.db)
	}
	return s.identities
}

func (s *Store) Heartbeats() heartbeat.HeartbeatStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeats == nil {
		s.heartbeats = pgstore.NewHeartbeatRepository(s.db)
	}
	return s.heartbeats
}

func (s *Store) HeartbeatTasks() heartbeattask.HeartbeatTaskStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeatTasks == nil {
		s.heartbeatTasks = pgstore.NewHeartbeatTaskRepository(s.db)
	}
	return s.heartbeatTasks
}

func (s *Store) HeartbeatTaskResults() heartbeattask.HeartbeatTaskResultStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeatTaskRes == nil {
		s.heartbeatTaskRes = pgstore.NewHeartbeatTaskResultRepository(s.db)
	}
	return s.heartbeatTaskRes
}

func (s *Store) Roles() security.RoleStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		userRepo := pgstore.NewUserRepository(s.db)
		s.roles = pgstore.NewRoleRepository(s.db, userRepo)
	}
	return s.roles
}

func (s *Store) Budgets() security.BudgetStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budgets == nil {
		userRepo := pgstore.NewUserRepository(s.db)
		s.budgets = pgstore.NewBudgetRepository(s.db, userRepo)
	}
	return s.budgets
}

func (s *Store) Audit() security.AuditStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audit == nil {
		s.audit = pgstore.NewAuditRepository(s.db)
	}
	return s.audit
}

func (s *Store) Soul() soul.SoulStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.soulStore == nil {
		s.soulStore = pgstore.NewSoulRepository(s.db)
	}
	return s.soulStore
}

// --- Agent Registry (in-memory) ---

func (s *Store) RegisterAgent(_ context.Context, a *storage.AgentInfo) error {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	s.agents[a.AgentID] = a
	return nil
}

func (s *Store) DeregisterAgent(_ context.Context, agentID string) error {
	s.agentsMu.Lock()
	defer s.agentsMu.Unlock()
	delete(s.agents, agentID)
	return nil
}

func (s *Store) ListAgents(_ context.Context) ([]*storage.AgentInfo, error) {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	result := make([]*storage.AgentInfo, 0, len(s.agents))
	for _, a := range s.agents {
		result = append(result, a)
	}
	return result, nil
}

// slogAdapter wraps *slog.Logger for GORM's logger.Writer interface.
type slogAdapter struct {
	logger *slog.Logger
}

func (s slogAdapter) Printf(format string, args ...any) {
	s.logger.Info(fmt.Sprintf(format, args...))
}

// compile-time interface check
var _ storage.Store = (*Store)(nil)
