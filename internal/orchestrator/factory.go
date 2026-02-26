package orchestrator

import (
	"fmt"
	"log/slog"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/observability"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// readOnlyTools are the tool names available to the researcher role.
var readOnlyTools = []string{"file_read", "web_fetch", "git_read", "browser"}

// DefaultAgentFactory creates role-specific agent.Orchestrator instances.
type DefaultAgentFactory struct {
	provider             llm.Provider
	security             security.SecurityManager
	approvalMgr          approval.ApprovalManager
	obs                  *observability.Observability
	toolReg              *tools.Registry
	logger               *slog.Logger
	recoveryAllowedTools []string // Extra tools for the diagnostician role.
}

// NewDefaultAgentFactory creates an AgentFactory backed by the given components.
func NewDefaultAgentFactory(
	provider llm.Provider,
	security security.SecurityManager,
	approvalMgr approval.ApprovalManager,
	obs *observability.Observability,
	toolReg *tools.Registry,
	logger *slog.Logger,
) *DefaultAgentFactory {
	return &DefaultAgentFactory{
		provider:    provider,
		security:    security,
		approvalMgr: approvalMgr,
		obs:         obs,
		toolReg:     toolReg,
		logger:      logger,
	}
}

// WithRecoveryTools configures extra tools available to the diagnostician role.
func (f *DefaultAgentFactory) WithRecoveryTools(toolNames []string) *DefaultAgentFactory {
	f.recoveryAllowedTools = toolNames
	return f
}

func (f *DefaultAgentFactory) Create(role AgentRole) (RoleAgent, error) {
	prompt := roleSystemPrompt(role)
	reg := f.toolRegistryForRole(role)

	orch := agent.NewOrchestrator(f.provider, prompt, f.logger)

	if f.security != nil {
		orch = orch.WithSecurity(f.security)
	}
	if reg != nil {
		orch = orch.WithTools(reg)
	}
	if f.approvalMgr != nil {
		orch = orch.WithApproval(f.approvalMgr)
	}
	if f.obs != nil {
		orch = orch.WithObservability(f.obs)
	}

	return &roleAgentWrapper{
		role:  role,
		inner: orch,
	}, nil
}

// toolRegistryForRole returns the appropriate tool subset for a role.
func (f *DefaultAgentFactory) toolRegistryForRole(role AgentRole) *tools.Registry {
	if f.toolReg == nil {
		return nil
	}

	switch role {
	case RoleExecutor:
		// Executor gets all tools.
		return f.toolReg

	case RoleResearcher:
		// Researcher gets read-only tools only.
		reg := tools.NewRegistry()
		for _, name := range readOnlyTools {
			if t := f.toolReg.Get(name); t != nil {
				reg.Register(t)
			}
		}
		return reg

	case RoleDiagnostician:
		// Diagnostician gets read-only tools plus any configured recovery tools.
		reg := tools.NewRegistry()
		for _, name := range readOnlyTools {
			if t := f.toolReg.Get(name); t != nil {
				reg.Register(t)
			}
		}
		for _, name := range f.recoveryAllowedTools {
			if t := f.toolReg.Get(name); t != nil {
				reg.Register(t)
			}
		}
		return reg

	case RoleOrchestrator, RolePlanner, RoleCompliance:
		// No tools — LLM-only roles.
		return nil

	default:
		f.logger.Warn("unknown agent role, defaulting to no tools",
			slog.String("role", string(role)))
		return nil
	}
}

// Compile-time check.
var _ AgentFactory = (*DefaultAgentFactory)(nil)

// ValidateRole checks if the given role is a known agent role.
func ValidateRole(role AgentRole) error {
	switch role {
	case RoleOrchestrator, RolePlanner, RoleResearcher, RoleExecutor, RoleCompliance, RoleDiagnostician:
		return nil
	default:
		return fmt.Errorf("unknown agent role: %s", role)
	}
}
