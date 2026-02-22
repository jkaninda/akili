package postgres

import (
	"context"
	"sync"

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
	"github.com/jkaninda/akili/internal/storage"
)

// Store implements storage.Store backed by PostgreSQL.
// It wraps the existing DB and lazily creates sub-store repositories.
type Store struct {
	pgDB *DB

	mu            sync.Mutex
	conversations agent.ConversationStore
	workflows     orchestrator.WorkflowStore
	skills        orchestrator.SkillStore
	cronJobs      scheduler.CronJobStore
	alertRules    alerting.AlertRuleStore
	alertHistory  alerting.AlertHistoryStore
	channels      notification.ChannelStore
	infraNodes    infra.Store
	approvals     approval.ApprovalStore
	identities    identity.IdentityStore
	heartbeats         heartbeat.HeartbeatStore
	heartbeatTasks     heartbeattask.HeartbeatTaskStore
	heartbeatTaskRes   heartbeattask.HeartbeatTaskResultStore
	roles              security.RoleStore
	budgets       security.BudgetStore
	audit         security.AuditStore

	// In-memory agent registry.
	agentsMu sync.RWMutex
	agents   map[string]*storage.AgentInfo
}

// NewStore wraps an existing DB as a unified Store.
func NewStore(pgDB *DB) *Store {
	return &Store{
		pgDB:   pgDB,
		agents: make(map[string]*storage.AgentInfo),
	}
}

func (s *Store) Migrate(_ context.Context) error {
	// PostgreSQL migration is done in Open() via autoMigrate.
	return nil
}

func (s *Store) Close() error {
	return s.pgDB.Close()
}

func (s *Store) Driver() string {
	return storage.DriverPostgres
}

// GormDB returns the underlying GORM DB for direct access when needed.
func (s *Store) GormDB() *DB {
	return s.pgDB
}

func (s *Store) EnsureOrg(ctx context.Context, name string) (uuid.UUID, error) {
	repo := NewOrgRepository(s.pgDB.GormDB())
	return repo.EnsureDefaultOrg(ctx, name)
}

// --- Sub-store accessors ---

func (s *Store) Conversations() agent.ConversationStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conversations == nil {
		s.conversations = NewConversationRepository(s.pgDB.GormDB())
	}
	return s.conversations
}

func (s *Store) Workflows() orchestrator.WorkflowStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workflows == nil {
		s.workflows = NewWorkflowRepository(s.pgDB.GormDB())
	}
	return s.workflows
}

func (s *Store) Skills() orchestrator.SkillStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skills == nil {
		s.skills = NewSkillRepository(s.pgDB.GormDB())
	}
	return s.skills
}

func (s *Store) CronJobs() scheduler.CronJobStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cronJobs == nil {
		s.cronJobs = NewCronJobRepository(s.pgDB.GormDB())
	}
	return s.cronJobs
}

func (s *Store) AlertRules() alerting.AlertRuleStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alertRules == nil {
		s.alertRules = NewAlertRuleRepository(s.pgDB.GormDB())
	}
	return s.alertRules
}

func (s *Store) AlertHistory() alerting.AlertHistoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alertHistory == nil {
		s.alertHistory = NewAlertHistoryRepository(s.pgDB.GormDB())
	}
	return s.alertHistory
}

func (s *Store) NotificationChannels() notification.ChannelStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channels == nil {
		s.channels = NewNotificationChannelRepository(s.pgDB.GormDB())
	}
	return s.channels
}

func (s *Store) InfraNodes() infra.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.infraNodes == nil {
		s.infraNodes = NewInfraNodeRepository(s.pgDB.GormDB())
	}
	return s.infraNodes
}

func (s *Store) Approvals() approval.ApprovalStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvals == nil {
		s.approvals = NewApprovalRepository(s.pgDB.GormDB())
	}
	return s.approvals
}

func (s *Store) Identities() identity.IdentityStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.identities == nil {
		s.identities = NewIdentityRepository(s.pgDB.GormDB())
	}
	return s.identities
}

func (s *Store) Heartbeats() heartbeat.HeartbeatStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeats == nil {
		s.heartbeats = NewHeartbeatRepository(s.pgDB.GormDB())
	}
	return s.heartbeats
}

func (s *Store) HeartbeatTasks() heartbeattask.HeartbeatTaskStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeatTasks == nil {
		s.heartbeatTasks = NewHeartbeatTaskRepository(s.pgDB.GormDB())
	}
	return s.heartbeatTasks
}

func (s *Store) HeartbeatTaskResults() heartbeattask.HeartbeatTaskResultStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeatTaskRes == nil {
		s.heartbeatTaskRes = NewHeartbeatTaskResultRepository(s.pgDB.GormDB())
	}
	return s.heartbeatTaskRes
}

func (s *Store) Roles() security.RoleStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		userRepo := NewUserRepository(s.pgDB.GormDB())
		s.roles = NewRoleRepository(s.pgDB.GormDB(), userRepo)
	}
	return s.roles
}

func (s *Store) Budgets() security.BudgetStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.budgets == nil {
		userRepo := NewUserRepository(s.pgDB.GormDB())
		s.budgets = NewBudgetRepository(s.pgDB.GormDB(), userRepo)
	}
	return s.budgets
}

func (s *Store) Audit() security.AuditStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audit == nil {
		s.audit = NewAuditRepository(s.pgDB.GormDB())
	}
	return s.audit
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

// compile-time interface check
var _ storage.Store = (*Store)(nil)
