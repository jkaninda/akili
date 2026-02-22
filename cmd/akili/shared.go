package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/llm/anthropic"
	"github.com/jkaninda/akili/internal/llm/gemini"
	"github.com/jkaninda/akili/internal/llm/openai"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/observability"
	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/secrets"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/storage"
	pgstore "github.com/jkaninda/akili/internal/storage/postgres"
	sqlitestore "github.com/jkaninda/akili/internal/storage/sqlite"
	"github.com/jkaninda/akili/internal/tools"
	"github.com/jkaninda/akili/internal/tools/browser"
	"github.com/jkaninda/akili/internal/tools/code"
	"github.com/jkaninda/akili/internal/tools/database"
	"github.com/jkaninda/akili/internal/tools/file"
	"github.com/jkaninda/akili/internal/tools/git"
	infratools "github.com/jkaninda/akili/internal/tools/infra"
	mcptools "github.com/jkaninda/akili/internal/tools/mcp"
	"github.com/jkaninda/akili/internal/tools/shell"
	"github.com/jkaninda/akili/internal/tools/web"
	"github.com/jkaninda/akili/internal/workspace"
)

const systemPrompt = `You are Akili, a security-first AI assistant for DevOps, SRE and Platform teams.
You help with incident triage, infrastructure automation, and operational tasks.
Always prioritize safety: prefer read-only actions, explain risks before suggesting changes,
and never execute destructive operations without explicit confirmation.

You have access to tools for executing shell commands, reading/writing files, querying databases,
fetching web content, executing code, and performing git operations. Use tools when the user's
request requires interacting with external systems. Always explain what you intend to do before
using a tool.

When a user refers to infrastructure by name or alias (e.g., "VM10", "prod-k8s", "staging-db"),
use infra_lookup to resolve the node first, then infra_exec to run commands on it.
Never ask for passwords, SSH keys, or other credentials — they are resolved automatically.
Never include raw credentials in any command parameter.`

// SharedComponents holds all initialized subsystems that both gateway and
// agent modes require. Built once by initShared, torn down by Cleanup.
type SharedComponents struct {
	Config    *config.Config
	Logger    *slog.Logger
	Workspace *workspace.Workspace
	Store     storage.Store // Unified store (SQLite or PostgreSQL).

	Obs            *observability.Observability
	LLMProvider    llm.Provider
	PgDB           *pgstore.DB // Non-nil only when storage.driver=postgres (for backward compat).
	Security       security.SecurityManager
	Sandbox        sandbox.Sandbox
	ToolReg        *tools.Registry
	ApprovalMgr    approval.ApprovalManager
	AgentCore      agent.Agent
	Dispatcher     *notification.Dispatcher // nil = notifications disabled.
	PolicyEnforcer *security.PolicyEnforcer // nil = no policy enforcement.
	OrgID          uuid.UUID                // Resolved org ID.

	cleanups []func()
}

// Cleanup runs all deferred cleanup functions in reverse order.
func (sc *SharedComponents) Cleanup() {
	for i := len(sc.cleanups) - 1; i >= 0; i-- {
		sc.cleanups[i]()
	}
}

func (sc *SharedComponents) addCleanup(fn func()) {
	sc.cleanups = append(sc.cleanups, fn)
}

// initShared performs all common initialization shared between gateway and agent modes.
// Callers must call sc.Cleanup() when done.
func initShared(cfg *config.Config, logger *slog.Logger) (*SharedComponents, error) {
	sc := &SharedComponents{
		Config: cfg,
		Logger: logger,
	}

	//  Workspace.
	ws, err := initWorkspace(cfg)
	if err != nil {
		return nil, fmt.Errorf("initializing workspace: %w", err)
	}
	sc.Workspace = ws
	logger.Debug("workspace initialized", slog.String("root", ws.Root))

	// Ensure data directory exists.
	dataDir := cfg.ResolvedDataDir()
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, fmt.Errorf("creating data directory %s: %w", dataDir, err)
	}
	logger.Debug("data directory initialized", slog.String("path", dataDir))

	// Resolve audit log path from data directory if not set in config.
	if cfg.Security.AuditLogPath == "" {
		cfg.Security.AuditLogPath = cfg.AuditLogPath()
	}

	// Observability.
	obs, err := observability.New(cfg.Observability, logger)
	if err != nil {
		return nil, fmt.Errorf("initializing observability: %w", err)
	}
	sc.Obs = obs
	sc.addCleanup(func() {
		if obs != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			obs.Shutdown(shutdownCtx)
		}
	})
	if obs != nil {
		logger.Debug("observability initialized",
			slog.Bool("metrics", obs.Metrics != nil),
			slog.Bool("tracing", obs.Tracer != nil),
			slog.Bool("anomaly", obs.Anomaly != nil),
		)
	}

	// LLM provider.
	llmProvider, err := newLLMProvider(cfg, logger)
	if err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("initializing LLM provider: %w", err)
	}
	logger.Debug("llm provider initialized", slog.String("provider", llmProvider.Name()))

	if obs != nil && obs.Metrics != nil {
		llmProvider = observability.NewInstrumentedProvider(
			llmProvider, obs.Metrics, obs.TracerOrNil(), obs.Anomaly,
		)
	}
	sc.LLMProvider = llmProvider

	// Storage (unified: SQLite default, PostgreSQL optional).
	store, err := initStore(cfg, ws, logger)
	if err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("initializing storage: %w", err)
	}
	sc.Store = store
	sc.addCleanup(func() {
		if err := store.Close(); err != nil {
			logger.Error("closing store", slog.String("error", err.Error()))
		}
	})

	// Run migrations.
	if err := store.Migrate(context.Background()); err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Backward compat: expose PgDB if using PostgreSQL.
	if pgStore, ok := store.(*pgstore.Store); ok {
		sc.PgDB = pgStore.GormDB()
	}

	orgName := "default"
	if cfg.Storage != nil && cfg.Storage.Postgres != nil && cfg.Storage.Postgres.DefaultOrgName != "" {
		orgName = cfg.Storage.Postgres.DefaultOrgName
	}
	orgID, err := store.EnsureOrg(context.Background(), orgName)
	if err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("ensuring default org: %w", err)
	}
	sc.OrgID = orgID
	logger.Debug("org initialized",
		slog.String("org_name", orgName),
		slog.String("org_id", orgID.String()),
	)

	// Security.
	secMgr, secCleanup, err := initSecurityFromStore(cfg, orgID, store, logger)
	if err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("initializing security: %w", err)
	}
	sc.addCleanup(secCleanup)

	var securityIface security.SecurityManager = secMgr
	if obs != nil && obs.Metrics != nil {
		securityIface = observability.NewInstrumentedSecurityManager(secMgr, obs.Metrics, obs.TracerOrNil())
	}
	sc.Security = securityIface

	// Security policies.
	if cfg.Policies != nil && len(cfg.Policies.Policies) > 0 {
		policies := make([]security.SecurityPolicy, len(cfg.Policies.Policies))
		for i, p := range cfg.Policies.Policies {
			policies[i] = security.SecurityPolicy{
				Name:            p.Name,
				AllowedTools:    p.AllowedTools,
				DeniedTools:     p.DeniedTools,
				AllowedPaths:    p.AllowedPaths,
				DeniedPaths:     p.DeniedPaths,
				AllowedDomains:  p.AllowedDomains,
				DeniedDomains:   p.DeniedDomains,
				AllowedCommands: p.AllowedCommands,
				DeniedCommands:  p.DeniedCommands,
				MaxRiskLevel:    p.MaxRiskLevel,
				RequireApproval: p.RequireApproval,
				AllowedSkills:   p.AllowedSkills,
				DeniedSkills:    p.DeniedSkills,
			}
		}
		sc.PolicyEnforcer = security.NewPolicyEnforcer(policies, cfg.Policies.Bindings, logger)
		logger.Debug("policy enforcer initialized", slog.Int("policies", len(policies)))
	}

	// Sandbox.
	sbx, err := initSandbox(cfg, logger)
	if err != nil {
		sc.Cleanup()
		return nil, fmt.Errorf("initializing sandbox: %w", err)
	}
	logger.Debug("sandbox initialized",
		slog.String("type", cfg.Sandbox.Type),
		slog.Int("max_memory_mb", cfg.Sandbox.MaxMemoryMB),
		slog.Int("max_execution_seconds", cfg.Sandbox.MaxExecutionSeconds),
	)

	var sbxIface sandbox.Sandbox = sbx
	if obs != nil && obs.Metrics != nil {
		sbxType := cfg.Sandbox.Type
		if sbxType == "" {
			sbxType = "process"
		}
		sbxIface = observability.NewInstrumentedSandbox(sbx, sbxType, obs.Metrics, obs.TracerOrNil(), obs.Anomaly)
	}
	sc.Sandbox = sbxIface

	// Tool registry.
	toolReg := tools.NewRegistry()
	toolReg.Register(shell.NewTool(sbxIface, logger))
	toolReg.Register(file.NewReadTool(file.Config{
		AllowedPaths:     cfg.Tools.File.AllowedPaths,
		MaxFileSizeBytes: cfg.Tools.File.MaxFileSizeBytes,
	}, logger))
	toolReg.Register(file.NewWriteTool(file.Config{
		AllowedPaths:     cfg.Tools.File.AllowedPaths,
		MaxFileSizeBytes: cfg.Tools.File.MaxFileSizeBytes,
	}, logger))
	toolReg.Register(web.NewTool(web.Config{
		AllowedDomains:   cfg.Tools.Web.AllowedDomains,
		MaxResponseBytes: cfg.Tools.Web.MaxResponseBytes,
		TimeoutSeconds:   cfg.Tools.Web.TimeoutSeconds,
	}, logger))
	toolReg.Register(browser.NewTool(browser.Config{
		AllowedDomains:        cfg.Tools.Browser.AllowedDomains,
		MaxResponseBytes:      cfg.Tools.Browser.MaxResponseBytes,
		TimeoutSeconds:        cfg.Tools.Browser.TimeoutSeconds,
		MaxNavigationsPerCall: cfg.Tools.Browser.MaxNavigationsPerCall,
	}, logger))
	toolReg.Register(git.NewTool(sbxIface, logger))
	toolReg.Register(git.NewWriteTool(sbxIface, logger))

	// Database read-only tool.
	dbDSN := cfg.Tools.Database.DSN
	if envDSN := os.Getenv("AKILI_TOOL_DB_DSN"); envDSN != "" {
		dbDSN = envDSN
	}
	if dbDSN != "" {
		toolReg.Register(database.NewTool(database.Config{
			DSN:            dbDSN,
			MaxRows:        cfg.Tools.Database.MaxRows,
			TimeoutSeconds: cfg.Tools.Database.TimeoutSeconds,
		}, logger))
	}

	toolReg.Register(code.NewTool(code.Config{
		AllowedLanguages: cfg.Tools.Code.AllowedLanguages,
	}, sbxIface, logger))
	logger.Debug("tools registered", slog.Any("tools", toolReg.List()))

	// MCP tool servers.
	if len(cfg.Tools.MCP) > 0 {
		mcpBridge := mcptools.NewBridge(logger)
		mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		for _, mcpCfg := range cfg.Tools.MCP {
			mcpToolList, mcpErr := mcpBridge.ConnectAndDiscover(mcpCtx, mcpCfg)
			if mcpErr != nil {
				logger.Error("MCP server failed, skipping",
					slog.String("server", mcpCfg.Name),
					slog.String("error", mcpErr.Error()),
				)
				continue
			}
			for _, t := range mcpToolList {
				toolReg.Register(t)
			}
		}
		mcpCancel()
		sc.addCleanup(mcpBridge.Close)
		logger.Debug("tools registered (with MCP)", slog.Any("tools", toolReg.List()))
	}

	// Infrastructure tools.
	if cfg.Infrastructure != nil && cfg.Infrastructure.Enabled {
		var secretProvider secrets.Provider = secrets.NewEnvProvider()
		if cfg.Secrets != nil && len(cfg.Secrets.Providers) > 0 {
			providers := make([]secrets.Provider, 0, len(cfg.Secrets.Providers))
			for _, sp := range cfg.Secrets.Providers {
				switch sp.Type {
				case "env":
					providers = append(providers, secrets.NewEnvProvider())
				case "vault":
					vp, err := secrets.NewVaultProvider(sp.Config)
					if err != nil {
						logger.Error("failed to create vault secret provider", slog.String("error", err.Error()))
					} else {
						providers = append(providers, vp)
					}
				default:
					logger.Warn("unknown secret provider type, skipping", slog.String("type", sp.Type))
				}
			}
			if len(providers) > 0 {
				secretProvider = secrets.NewCompositeProvider(providers...)
			}
		}

		// Use infra store from the unified store.
		infraStore := store.InfraNodes()
		logger.Debug("infrastructure store initialized", slog.String("mode", store.Driver()))

		toolReg.Register(infratools.NewLookupTool(infraStore, sc.OrgID, logger))
		toolReg.Register(infratools.NewManageTool(infraStore, sc.OrgID, logger))
		toolReg.Register(infratools.NewExecTool(infraStore, secretProvider, toolReg, sc.OrgID, logger))
		logger.Debug("infrastructure tools registered")
	}

	sc.ToolReg = toolReg

	// Notification subsystem.
	if cfg.Notification != nil && cfg.Notification.Enabled {
		channelStore := store.NotificationChannels()
		dispatcher := notification.NewDispatcher(channelStore, securityIface, logger)

		if cfg.Gateways.Telegram != nil && cfg.Gateways.Telegram.BotToken != "" {
			dispatcher.RegisterSender(notification.NewTelegramSender(cfg.Gateways.Telegram.BotToken, logger))
		}
		if cfg.Gateways.Slack != nil && cfg.Gateways.Slack.BotToken != "" {
			dispatcher.RegisterSender(notification.NewSlackSender(cfg.Gateways.Slack.BotToken, logger))
		}
		if cfg.Notification.Email != nil {
			dispatcher.RegisterSender(notification.NewEmailSender(notification.SMTPConfig{
				Host:     cfg.Notification.Email.Host,
				Port:     cfg.Notification.Email.Port,
				Username: cfg.Notification.Email.Username,
				From:     cfg.Notification.Email.From,
				TLS:      cfg.Notification.Email.TLS,
			}, logger))
		}
		// WhatsApp sender.
		if cfg.Notification.WhatsApp != nil && cfg.Notification.WhatsApp.AccessToken != "" {
			dispatcher.RegisterSender(notification.NewWhatsAppSender(cfg.Notification.WhatsApp.AccessToken, logger))
		}
		// Signal sender.
		if cfg.Notification.Signal != nil && cfg.Notification.Signal.APIURL != "" && cfg.Notification.Signal.SenderNumber != "" {
			dispatcher.RegisterSender(notification.NewSignalSender(
				cfg.Notification.Signal.APIURL,
				cfg.Notification.Signal.SenderNumber,
				logger,
			))
		}
		dispatcher.RegisterSender(notification.NewWebhookSender(logger))

		sc.Dispatcher = dispatcher
		logger.Debug("notification dispatcher initialized")
	}

	// Approval manager.
	approvalTTL := 5 * time.Minute
	if cfg.Approval.TTLSeconds > 0 {
		approvalTTL = time.Duration(cfg.Approval.TTLSeconds) * time.Second
	}
	approvalMgr := approval.NewManager(approvalTTL, logger)
	logger.Debug("approval manager initialized", slog.String("ttl", approvalTTL.String()))
	sc.ApprovalMgr = approvalMgr

	// Health checks.
	if obs != nil && obs.Health != nil {
		if sc.PgDB != nil {
			obs.Health.AddCheck("database", sc.PgDB.Ping)
		}
	}

	// Agent core.
	agentCore := agent.NewOrchestrator(llmProvider, systemPrompt, logger).
		WithSecurity(securityIface).
		WithTools(toolReg).
		WithApproval(approvalMgr).
		WithObservability(obs).
		WithPolicyEnforcer(sc.PolicyEnforcer)

	// Conversation store (persistent memory).
	if cfg.Memory != nil && cfg.Memory.Enabled {
		convStore := store.Conversations()
		logger.Debug("conversation store initialized", slog.String("mode", store.Driver()))
		agentCore.WithConversationStore(convStore, sc.OrgID, cfg.Memory.MaxHistory(), cfg.Memory.MaxMsgBytes())

		// Conversation summarization.
		if cfg.Memory.SummarizeOnTruncate {
			agentCore.WithSummarization(true)
			logger.Debug("conversation summarization enabled")
		}
	}

	// Tool result caching for read-only tools.
	readOnlyTools := []string{"file_read", "web_fetch", "git_read", "database_read", "infra_lookup", "browser_fetch"}
	agentCore.WithToolCache(agent.DefaultToolCacheTTL, readOnlyTools)
	logger.Debug("tool result caching enabled", slog.Int("read_only_tools", len(readOnlyTools)))

	// ReAct planning.
	if cfg.Orchestrator != nil && cfg.Orchestrator.PlanningEnabled {
		agentCore.WithPlanning(true)
		logger.Debug("ReAct planning enabled")
	}

	// Smart auto approval.
	if cfg.Approval.AutoApproval != nil && cfg.Approval.AutoApproval.Enabled {
		autoApprover := approval.NewAutoApprover(approval.AutoApprovalConfig{
			Enabled:           cfg.Approval.AutoApproval.Enabled,
			MaxAutoApprovals:  cfg.Approval.AutoApproval.MaxAutoApprovals,
			AllowedTools:      cfg.Approval.AutoApproval.AllowedTools,
			RequiredApprovals: cfg.Approval.AutoApproval.RequiredApprovals,
			WindowHours:       cfg.Approval.AutoApproval.WindowHours,
		}, logger)
		agentCore.WithAutoApprover(autoApprover)
		logger.Debug("smart auto-approval enabled",
			slog.Int("required_approvals", cfg.Approval.AutoApproval.RequiredApprovals),
		)
	}

	sc.AgentCore = agentCore

	return sc, nil
}

// initWorkspace creates and returns the workspace, resolving the root from config or defaults.
func initWorkspace(cfg *config.Config) (*workspace.Workspace, error) {
	root := cfg.Workspace
	if root == "" {
		return workspace.Default()
	}
	return workspace.New(root)
}

// initStore creates the appropriate storage backend from config.
func initStore(cfg *config.Config, ws *workspace.Workspace, logger *slog.Logger) (storage.Store, error) {
	driver := cfg.StorageDriverName()

	switch driver {
	case "postgres":
		return initPostgresStore(cfg, logger)
	case "sqlite":
		return initSQLiteStore(cfg, ws, logger)
	default:
		return nil, fmt.Errorf("unknown storage driver: %q", driver)
	}
}

func initSQLiteStore(cfg *config.Config, ws *workspace.Workspace, logger *slog.Logger) (storage.Store, error) {
	dbPath := cfg.DatabasePath()
	journalMode := "wal"

	if cfg.Storage != nil && cfg.Storage.SQLite != nil {
		if cfg.Storage.SQLite.Path != "" {
			dbPath = cfg.Storage.SQLite.Path
		}
		if cfg.Storage.SQLite.JournalMode != "" {
			journalMode = cfg.Storage.SQLite.JournalMode
		}
	}

	return sqlitestore.Open(sqlitestore.Config{
		Path:        dbPath,
		JournalMode: journalMode,
	}, logger)
}

func initPostgresStore(cfg *config.Config, logger *slog.Logger) (storage.Store, error) {
	var dsn string
	if cfg.Storage != nil && cfg.Storage.Postgres != nil {
		dsn = cfg.Storage.Postgres.DSN
	}

	if envDSN := os.Getenv("AKILI_DB_DSN"); envDSN != "" {
		dsn = envDSN
	}
	if dsn == "" {
		return nil, fmt.Errorf("postgres DSN is required (set storage.postgres.dsn or AKILI_DB_DSN)")
	}

	pgCfg := pgstore.Config{DSN: dsn}
	if cfg.Storage != nil && cfg.Storage.Postgres != nil {
		pgCfg.MaxOpenConns = cfg.Storage.Postgres.MaxOpenConns
		pgCfg.MaxIdleConns = cfg.Storage.Postgres.MaxIdleConns
		pgCfg.ConnMaxLifetime = time.Duration(cfg.Storage.Postgres.ConnMaxLifetimeS) * time.Second
	}

	pgDB, err := pgstore.Open(pgCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("opening postgres: %w", err)
	}

	return pgstore.NewStore(pgDB), nil
}

// initSecurityFromStore creates the security layer using the unified store.
func initSecurityFromStore(cfg *config.Config, orgID uuid.UUID, store storage.Store, logger *slog.Logger) (security.SecurityManager, func(), error) {
	rbacCfg := buildRBACConfig(&cfg.Security)

	if store.Driver() == "postgres" || store.Driver() == "sqlite" {
		pgRBAC := security.NewPGRBAC(store.Roles(), orgID, rbacCfg, logger)
		pgBudget := security.NewPGBudgetManager(store.Budgets(), orgID, cfg.Budget.DefaultLimitUSD, logger)
		pgAudit := security.NewPGAuditLogger(store.Audit(), orgID, logger)

		mgr := security.NewManager(pgRBAC, pgAudit, pgBudget, logger)
		logger.Debug("security initialized",
			slog.String("mode", store.Driver()),
			slog.Float64("default_limit_usd", cfg.Budget.DefaultLimitUSD),
		)

		cleanup := func() {
			if err := mgr.Close(); err != nil {
				logger.Error("closing security manager", slog.String("error", err.Error()))
			}
		}
		return mgr, cleanup, nil
	}

	// Fallback: in-memory security
	return initInMemorySecurity(cfg, logger)
}

// initInMemorySecurity creates the security layer using in-memory state.
func initInMemorySecurity(cfg *config.Config, logger *slog.Logger) (security.SecurityManager, func(), error) {
	auditDir := filepath.Dir(cfg.Security.AuditLogPath)
	if err := os.MkdirAll(auditDir, 0750); err != nil {
		return nil, nil, fmt.Errorf("creating audit log directory %s: %w", auditDir, err)
	}

	rbacCfg := buildRBACConfig(&cfg.Security)
	rbac := security.NewRBAC(rbacCfg, logger)

	auditLogger, err := security.NewAuditLogger(cfg.Security.AuditLogPath, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("initializing audit logger: %w", err)
	}

	budgetMgr := security.NewBudgetManager(cfg.Budget.DefaultLimitUSD, logger)
	mgr := security.NewManager(rbac, auditLogger, budgetMgr, logger)

	cleanup := func() {
		if err := mgr.Close(); err != nil {
			logger.Error("closing security manager", slog.String("error", err.Error()))
		}
	}
	return mgr, cleanup, nil
}

// initSandbox creates the appropriate sandbox based on config type.
func initSandbox(cfg *config.Config, logger *slog.Logger) (sandbox.Sandbox, error) {
	switch cfg.Sandbox.Type {
	case "docker":
		if cfg.Sandbox.Docker.Image == "" {
			return nil, fmt.Errorf("sandbox.docker.image is required when type is \"docker\"")
		}
		return sandbox.NewDockerSandbox(sandbox.DockerConfig{
			Image:          cfg.Sandbox.Docker.Image,
			DefaultTimeout: time.Duration(cfg.Sandbox.MaxExecutionSeconds) * time.Second,
			MemoryMB:       cfg.Sandbox.MaxMemoryMB,
			CPUCores:       cfg.Sandbox.Docker.CPUCores,
			PIDsLimit:      cfg.Sandbox.Docker.PIDsLimit,
			NetworkAllowed: cfg.Sandbox.NetworkAllowed,
		}, logger), nil
	case "process", "":
		return sandbox.NewProcessSandbox(sandbox.ProcessConfig{
			DefaultTimeout: time.Duration(cfg.Sandbox.MaxExecutionSeconds) * time.Second,
			DefaultLimits: sandbox.ResourceLimits{
				MaxCPUSeconds: cfg.Sandbox.MaxCPUCores * 60,
				MaxMemoryMB:   cfg.Sandbox.MaxMemoryMB,
			},
		}, logger), nil
	default:
		return nil, fmt.Errorf("unknown sandbox type: %q (supported: process, docker)", cfg.Sandbox.Type)
	}
}

// buildRBACConfig converts config types to security types.
func buildRBACConfig(sc *config.SecurityConfig) security.RBACConfig {
	roles := make(map[string]security.Role, len(sc.Roles))
	for name, rc := range sc.Roles {
		roles[name] = security.Role{
			Name:            name,
			Permissions:     rc.Permissions,
			MaxRiskLevel:    rc.MaxRiskLevel,
			RequireApproval: rc.RequireApproval,
		}
	}
	return security.RBACConfig{
		Roles:       roles,
		UserRoles:   sc.UserRoles,
		DefaultRole: sc.DefaultRole,
	}
}

// newLLMProvider creates the LLM provider based on the configured default.
func newLLMProvider(cfg *config.Config, logger *slog.Logger) (llm.Provider, error) {
	primary, err := buildProvider(cfg.Providers.Default, cfg, logger)
	if err != nil {
		return nil, err
	}

	// Build fallback chain if configured.
	if len(cfg.Providers.Fallback) > 0 {
		providers := []llm.Provider{primary}
		for _, name := range cfg.Providers.Fallback {
			fb, err := buildProvider(name, cfg, logger)
			if err != nil {
				logger.Warn("skipping fallback provider",
					slog.String("provider", name),
					slog.String("error", err.Error()),
				)
				continue
			}
			providers = append(providers, fb)
		}
		if len(providers) > 1 {
			return llm.NewFallbackProvider(providers, logger), nil
		}
	}

	return primary, nil
}

// buildProvider creates a single LLM provider by name.
func buildProvider(name string, cfg *config.Config, logger *slog.Logger) (llm.Provider, error) {
	switch name {
	case "anthropic", "":
		return anthropic.NewClient(
			cfg.Providers.Anthropic.APIKey,
			cfg.Providers.Anthropic.Model,
			logger,
		), nil
	case "openai":
		var opts []openai.Option
		if cfg.Providers.OpenAI.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.Providers.OpenAI.BaseURL))
		}
		return openai.NewClient(
			cfg.Providers.OpenAI.APIKey,
			cfg.Providers.OpenAI.Model,
			logger,
			opts...,
		), nil
	case "gemini":
		var opts []gemini.Option
		if cfg.Providers.Gemini.BaseURL != "" {
			opts = append(opts, gemini.WithBaseURL(cfg.Providers.Gemini.BaseURL))
		}
		return gemini.NewClient(
			cfg.Providers.Gemini.APIKey,
			cfg.Providers.Gemini.Model,
			logger,
			opts...,
		), nil
	case "ollama":
		baseURL := cfg.Providers.Ollama.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return openai.NewClient(
			"",
			cfg.Providers.Ollama.Model,
			logger,
			openai.WithBaseURL(baseURL),
			openai.WithName("ollama"),
		), nil
	default:
		return nil, fmt.Errorf("unknown provider: %q", name)
	}
}
