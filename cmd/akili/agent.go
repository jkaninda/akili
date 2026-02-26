package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	agentpkg "github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/heartbeat"
	"github.com/jkaninda/akili/internal/identity"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/protocol"
	"github.com/jkaninda/akili/internal/skillloader"
)

var (
	agentConfigPath  string
	agentID          string
	agentPollInt     int
	agentConcurrency int
	agentGatewayURL  string
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start in agent mode (WebSocket or legacy DB poller)",
	Long: `Start Akili in agent mode. By default, the agent connects to a gateway
via WebSocket and receives task assignments in real-time.

For legacy setups, the agent can also poll the database for pending
workflow tasks (requires PostgreSQL and the orchestrator).`,
	RunE: runAgent,
}

func init() {
	agentCmd.Flags().StringVar(&agentConfigPath, "config", config.DefaultConfigPath(), "path to config file")
	agentCmd.Flags().StringVar(&agentID, "agent-id", "", "override agent ID (default: hostname-pid)")
	agentCmd.Flags().IntVar(&agentPollInt, "poll-interval", 0, "override poll interval in seconds (legacy mode)")
	agentCmd.Flags().IntVar(&agentConcurrency, "concurrency", 0, "override max concurrent tasks")
	agentCmd.Flags().StringVar(&agentGatewayURL, "gateway-url", "", "override WebSocket gateway URL (e.g., ws://localhost:8080/ws/agents)")

}

// runAgent starts Akili in agent mode.
func runAgent(cmd *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Resolve config path: explicit --config flag takes priority over AKILI_CONFIG env var.
	configPath := agentConfigPath
	if !cmd.Flags().Changed("config") {
		if envCfg := os.Getenv("AKILI_CONFIG"); envCfg != "" {
			configPath = envCfg
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Info("config loaded", slog.String("path", configPath))

	// Force agent mode.
	if cfg.Runtime == nil {
		cfg.Runtime = &config.RuntimeConfig{}
	}
	cfg.Runtime.Mode = "agent"

	// Apply CLI overrides.
	if cfg.Runtime.Agent == nil {
		cfg.Runtime.Agent = &config.AgentConfig{}
	}
	if agentID != "" {
		cfg.Runtime.Agent.AgentID = agentID
	}
	if agentPollInt > 0 {
		cfg.Runtime.Agent.PollIntervalSeconds = agentPollInt
	}
	if agentConcurrency > 0 {
		cfg.Runtime.Agent.MaxConcurrentTasks = agentConcurrency
	}
	if agentGatewayURL != "" {
		cfg.Runtime.Agent.GatewayURL = agentGatewayURL
	}

	// Default agent ID.
	if cfg.Runtime.Agent.AgentID == "" {
		hostname, _ := os.Hostname()
		cfg.Runtime.Agent.AgentID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	agentCfg := cfg.Runtime.Agent

	logger.Info("agent mode resolved",
		slog.String("agent_id", agentCfg.AgentID),
		slog.String("gateway_url", agentCfg.GatewayURL),
		slog.Bool("websocket_mode", agentCfg.UseWebSocket()),
	)

	// Choose mode: WebSocket or legacy DB polling.
	if agentCfg.UseWebSocket() {
		return runAgentWebSocket(cfg, agentCfg, logger)
	}

	if cfg.Orchestrator == nil || !cfg.Orchestrator.Enabled {
		return fmt.Errorf("legacy agent mode requires orchestrator to be enabled")
	}

	return runAgentDBPoll(cfg, agentCfg, logger)
}

// runAgentWebSocket starts the agent in WebSocket mode — connects to the gateway,
// receives tasks in real-time, no database required on the agent side.
func runAgentWebSocket(cfg *config.Config, agentCfg *config.AgentConfig, logger *slog.Logger) error {
	logger.Info("starting in agent mode (websocket)",
		slog.String("agent_id", agentCfg.AgentID),
		slog.String("gateway_url", agentCfg.GatewayURL),
	)

	// WebSocket agents do not need database access — all persistence
	// and authoritative security enforcement happens on the gateway side.
	sc, err := initSharedForWSAgent(cfg, logger)
	if err != nil {
		return err
	}
	defer sc.Cleanup()

	// Signal-aware context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start approval cleanup goroutine.
	cancelCleanup := sc.ApprovalMgr.StartCleanup(ctx, 1*time.Minute)
	defer cancelCleanup()

	// Collect skill names for registration.
	var skills []string
	if cfg.Orchestrator != nil {
		skillDirs := cfg.Orchestrator.AllSkillsDirs()
		if len(skillDirs) > 0 {
			loader := skillloader.NewLoader(sc.ToolReg, logger)
			for _, dir := range skillDirs {
				defs, _, err := loader.LoadDir(dir)
				if err != nil {
					logger.Warn("failed to load skills", slog.String("dir", dir), slog.String("error", err.Error()))
					continue
				}
				for _, d := range defs {
					skills = append(skills, d.SkillKey)
				}
			}
		}
	}

	// Build WebSocket client.
	wsClient := agentpkg.NewWSClient(agentpkg.WSClientConfig{
		GatewayURL:        agentCfg.GatewayURL,
		Token:             agentCfg.Token,
		AgentID:           agentCfg.AgentID,
		Skills:            skills,
		Model:             cfg.Providers.Default,
		MaxParallel:       agentCfg.Concurrency(),
		Version:           agentCfg.Version,
		HeartbeatInterval: agentCfg.HeartbeatInterval(),
		ReconnectInterval: agentCfg.ReconnectInterval(),
	}, logger)

	// Set task handler: execute tasks using the agent core.
	wsClient.OnTask(func(ctx context.Context, assignment protocol.TaskAssignment) (*protocol.TaskResultPayload, error) {
		start := time.Now()

		// Send progress update.
		wsClient.SendProgress(ctx, assignment.TaskID, protocol.TaskProgress{
			Message: "executing task",
		})

		result, err := sc.AgentCore.Process(ctx, &agentpkg.Input{
			UserID:        assignment.UserID,
			Message:       assignment.Goal,
			CorrelationID: assignment.TaskID,
		})
		if err != nil {
			return nil, err
		}

		return &protocol.TaskResultPayload{
			Output:     result.Message,
			TokensUsed: result.TokensUsed,
			Duration:   time.Since(start).String(),
		}, nil
	})

	logger.Info("connecting to gateway",
		slog.String("url", agentCfg.GatewayURL),
		slog.String("agent_id", agentCfg.AgentID),
		slog.Int("max_parallel", agentCfg.Concurrency()),
	)

	return wsClient.Run(ctx)
}

// runAgentDBPoll starts the agent in legacy DB-polling mode.
// This is the original behavior: poll PostgreSQL for pending workflow tasks.
func runAgentDBPoll(cfg *config.Config, agentCfg *config.AgentConfig, logger *slog.Logger) error {
	logger.Info("starting in agent mode (db polling)",
		slog.String("agent_id", agentCfg.AgentID),
		slog.String("config", agentConfigPath),
	)

	sc, err := initShared(cfg, logger)
	if err != nil {
		return err
	}
	defer sc.Cleanup()

	// Signal-aware context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start approval cleanup goroutine.
	cancelCleanup := sc.ApprovalMgr.StartCleanup(ctx, 1*time.Minute)
	defer cancelCleanup()

	// --- Agent identity ---
	var agentIdentity *identity.IdentityConfig
	agentName := agentCfg.Name
	if agentName == "" {
		agentName, _ = os.Hostname()
	}
	if agentCfg.KeyFile != "" {
		agentIdentity, err = identity.LoadIdentity(
			agentCfg.KeyFile,
			agentCfg.AgentID,
			agentName,
			agentCfg.Version,
			agentCfg.EnvironmentScope,
			agentCfg.Capabilities,
		)
		if err != nil {
			return fmt.Errorf("loading agent identity: %w", err)
		}
		logger.Debug("agent identity loaded from key file",
			slog.String("agent_id", agentIdentity.AgentID),
			slog.String("public_key", agentIdentity.PublicKeyHex()),
		)
	} else {
		agentIdentity, err = identity.NewIdentity(
			agentName,
			agentCfg.Version,
			agentCfg.EnvironmentScope,
			agentCfg.Capabilities,
		)
		if err != nil {
			return fmt.Errorf("generating agent identity: %w", err)
		}
		// Use the config agent ID as the identity agent ID (for consistency).
		agentIdentity.AgentID = agentCfg.AgentID
		logger.Debug("agent identity generated (ephemeral keypair)",
			slog.String("agent_id", agentIdentity.AgentID),
			slog.String("public_key", agentIdentity.PublicKeyHex()),
		)
	}

	// Register identity in database.
	identityStore := sc.Store.Identities()
	if err := identityStore.Register(ctx, agentIdentity); err != nil {
		logger.Warn("failed to register agent identity in database",
			slog.String("error", err.Error()),
		)
	}

	// --- Heartbeat sender ---
	hbStore := sc.Store.Heartbeats()
	hbSender := heartbeat.NewHeartbeatSender(
		agentIdentity,
		hbStore,
		agentCfg.HeartbeatInterval(),
		agentCfg.Concurrency(),
		nil,
		logger,
	)
	go hbSender.Run(ctx)

	// Build workflow store from unified store.
	wfStore := sc.Store.Workflows()

	// Build agent factory.
	factory := orchestrator.NewDefaultAgentFactory(
		sc.LLMProvider, sc.Security, sc.ApprovalMgr, sc.Obs, sc.ToolReg, sc.Logger,
	)

	// Workflow metrics.
	var wfMetrics *orchestrator.WorkflowMetrics
	if sc.Obs != nil && sc.Obs.Metrics != nil {
		wfMetrics = orchestrator.NewWorkflowMetrics(sc.Obs.Metrics.Registry)
	}

	// Skill intelligence layer.
	skillStore := sc.Store.Skills()
	var skillMetrics *orchestrator.SkillMetrics
	if sc.Obs != nil && sc.Obs.Metrics != nil {
		skillMetrics = orchestrator.NewSkillMetrics(sc.Obs.Metrics.Registry)
	}
	skillTracker := orchestrator.NewSkillTracker(skillStore, skillMetrics, logger)

	skillDirs := cfg.Orchestrator.AllSkillsDirs()
	if len(skillDirs) > 0 {
		pollInterval := cfg.Orchestrator.SkillPollInterval()

		if pollInterval > 0 {
			// Polling mode: auto-reload on file changes.
			onReload := func(defs []skillloader.SkillDefinition) {
				if summary := skillloader.RenderSkillSummary(defs); summary != "" {
					if orch, ok := sc.AgentCore.(*agentpkg.Orchestrator); ok {
						orch.WithSkillSummary(summary)
					}
				}
				// Update heartbeat skills.
				keys := make([]string, len(defs))
				for i, d := range defs {
					keys[i] = d.SkillKey
				}
				hbSender.SetSkills(keys)
			}

			skillPoller := skillloader.NewSkillPoller(
				skillloader.SkillPollerConfig{
					SkillsDirs:   skillDirs,
					PollInterval: pollInterval,
					OrgID:        sc.OrgID,
				},
				sc.ToolReg,
				skillStore,
				skillMetrics,
				onReload,
				logger,
			)
			go skillPoller.Run(ctx)
			logger.Debug("skill poller started",
				slog.String("interval", pollInterval.String()),
				slog.Int("dirs", len(skillDirs)),
			)
		} else {
			loader := skillloader.NewLoader(sc.ToolReg, logger)
			for _, dir := range skillDirs {
				defs, result, err := loader.LoadDir(dir)
				if err != nil {
					logger.Warn("failed to load skill definitions",
						slog.String("dir", dir),
						slog.String("error", err.Error()),
					)
					continue
				}
				if skillMetrics != nil {
					for _, le := range result.Errors {
						skillMetrics.SkillDefsErrors.WithLabelValues(le.File).Inc()
					}
				}
				if len(defs) > 0 {
					seedResult := skillloader.SeedSkills(ctx, skillStore, sc.OrgID, defs, skillMetrics, logger)
					logger.Debug("skill definitions seeded",
						slog.Int("seeded", seedResult.Seeded),
						slog.Int("skipped", seedResult.Skipped),
					)
					if summary := skillloader.RenderSkillSummary(defs); summary != "" {
						if orch, ok := sc.AgentCore.(*agentpkg.Orchestrator); ok {
							orch.WithSkillSummary(summary)
							logger.Debug("skill summary injected into agent system prompt",
								slog.Int("skill_count", len(defs)),
							)
						}
					}
				}
			}
		}
	}

	// --- Community skill packs ---
	if len(cfg.Orchestrator.CommunityPacksDirs) > 0 {
		loader := skillloader.NewLoader(sc.ToolReg, logger)
		packLoader := skillloader.NewPackLoader(loader, sc.PolicyEnforcer, agentCfg.AgentID, logger)
		for _, dir := range cfg.Orchestrator.CommunityPacksDirs {
			manifests, result, err := packLoader.LoadPack(ctx, dir)
			if err != nil {
				logger.Warn("failed to load community pack",
					slog.String("dir", dir),
					slog.String("error", err.Error()),
				)
				continue
			}
			if len(manifests) > 0 {
				// Convert manifests to SkillDefinitions and seed.
				defs := make([]skillloader.SkillDefinition, len(manifests))
				for i, m := range manifests {
					defs[i] = *m.ToSkillDefinition()
				}
				seedResult := skillloader.SeedSkills(ctx, skillStore, sc.OrgID, defs, skillMetrics, logger)
				logger.Debug("community pack loaded",
					slog.String("dir", dir),
					slog.Int("loaded", result.Loaded),
					slog.Int("seeded", seedResult.Seeded),
					slog.Int("errors", len(result.Errors)),
				)
			}
		}
	}

	// Convert role strings to AgentRole.
	var roles []orchestrator.AgentRole
	for _, r := range agentCfg.Roles {
		roles = append(roles, orchestrator.AgentRole(r))
	}

	agentPoller := orchestrator.NewAgentPoller(
		wfStore,
		factory,
		wfMetrics,
		logger,
		orchestrator.AgentPollerConfig{
			AgentID:            agentCfg.AgentID,
			Roles:              roles,
			PollInterval:       agentCfg.PollInterval(),
			MaxConcurrentTasks: agentCfg.Concurrency(),
			TaskTimeout:        agentCfg.TaskTimeout(),
			HeartbeatInterval:  agentCfg.HeartbeatInterval(),
		},
	).WithSkillTracker(skillTracker)

	return agentPoller.Run(ctx)
}
