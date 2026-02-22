package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/alerting"
	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/gateway"
	"github.com/jkaninda/akili/internal/gateway/cli"
	"github.com/jkaninda/akili/internal/gateway/httpapi"
	signalgw "github.com/jkaninda/akili/internal/gateway/signal"
	"github.com/jkaninda/akili/internal/gateway/slack"
	"github.com/jkaninda/akili/internal/gateway/telegram"
	"github.com/jkaninda/akili/internal/gateway/ws"
	"github.com/jkaninda/akili/internal/heartbeat"
	"github.com/jkaninda/akili/internal/heartbeattask"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/ratelimit"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/akili/internal/skillloader"
	"github.com/jkaninda/akili/internal/storage"
	pgstore "github.com/jkaninda/akili/internal/storage/postgres"
	sqlitestore "github.com/jkaninda/akili/internal/storage/sqlite"
	alerttool "github.com/jkaninda/akili/internal/tools/alert"
	cronjobtool "github.com/jkaninda/akili/internal/tools/cronjob"
	goutils "github.com/jkaninda/go-utils"
)

var (
	gatewayConfigPath string
	gatewayPort       string
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Start in gateway mode (HTTP, CLI, Slack, Telegram)",
	RunE:  runGateway,
}

func init() {
	// Register flags on both root and gateway so that
	// `akili --config path` and `akili gateway --config path` both work.
	for _, cmd := range []*cobra.Command{rootCmd, gatewayCmd} {
		cmd.Flags().StringVar(&gatewayConfigPath, "config", config.DefaultConfigPath(), "path to config file")
		cmd.Flags().StringVar(&gatewayPort, "port", "", "override HTTP listen port (e.g. :8080)")

	}
}

// runGateway starts Akili in gateway mode (HTTP server, CLI, Slack, Telegram).
func runGateway(_ *cobra.Command, _ []string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(goutils.Env("AKILI_CONFIG", gatewayConfigPath))
	if err != nil {
		return err
	}

	// Apply CLI overrides.
	if gatewayPort != "" {
		if cfg.Gateways.HTTP == nil {
			cfg.Gateways.HTTP = &config.HTTPGatewayConfig{Enabled: true}
		}
		cfg.Gateways.HTTP.ListenAddr = gatewayPort
	}

	logger.Info("starting in gateway mode", slog.String("config", gatewayConfigPath))

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

	// Start heartbeat stale checker (marks unresponsive agents).
	hbStore := sc.Store.Heartbeats()
	go heartbeat.RunStaleChecker(ctx, hbStore, 60*time.Second, 3*time.Minute, logger)
	logger.Debug("heartbeat stale checker started")

	// Initialize WebSocket server for remote agents (optional).
	var wsServer *ws.Server
	if cfg.Gateways.WebSocket != nil && cfg.Gateways.WebSocket.Enabled {
		registry := agent.NewRegistry(logger)
		wsServer = ws.NewServer(registry, cfg.Gateways.WebSocket, logger)
		logger.Debug("websocket server initialized",
			slog.String("path", cfg.Gateways.WebSocket.WSPath()),
		)
	}

	// Initialize workflow engine (optional).
	var workflowEngine orchestrator.WorkflowEngine
	if cfg.Orchestrator != nil && cfg.Orchestrator.Enabled {
		workflowEngine = buildWorkflowEngine(ctx, cfg, sc)
	}

	// Initialize cron scheduler (optional, requires orchestrator + GORM DB for transactions).
	var cronStore scheduler.CronJobStore
	var cronOrgID uuid.UUID
	gormDB := storeGormDB(sc.Store)
	if cfg.Scheduler != nil && cfg.Scheduler.Enabled && workflowEngine != nil && gormDB != nil {
		cronStore = sc.Store.CronJobs()
		cronOrgID = sc.OrgID

		var schedMetrics *scheduler.Metrics
		if sc.Obs != nil && sc.Obs.Metrics != nil {
			schedMetrics = scheduler.NewMetrics(sc.Obs.Metrics.Registry)
		}

		cronScheduler := scheduler.New(
			cronStore,
			func(db *gorm.DB) scheduler.CronJobStore {
				return pgstore.NewCronJobRepository(db)
			},
			gormDB,
			workflowEngine,
			sc.Security,
			schedMetrics,
			sc.Logger,
			cfg.Scheduler,
			cronOrgID,
		)
		cancelScheduler := cronScheduler.Start(ctx)
		defer cancelScheduler()

		logger.Debug("cron scheduler initialized",
			slog.String("poll_interval", cfg.Scheduler.PollInterval().String()),
			slog.Int("max_concurrent", cfg.Scheduler.MaxConcurrent()),
		)
	}

	// Register create_cronjob tool (requires cron store + scheduler).
	if cronStore != nil {
		sc.ToolReg.Register(cronjobtool.NewTool(cronStore, cronOrgID, sc.Logger))
		logger.Debug("create_cronjob tool registered")
	}

	// Initialize alert checker and tools (optional, requires notification dispatcher).
	var alertStore alerting.AlertRuleStore
	var historyStore alerting.AlertHistoryStore
	var channelStore notification.ChannelStore
	if sc.Dispatcher != nil {
		alertStore = sc.Store.AlertRules()
		historyStore = sc.Store.AlertHistory()
		channelStore = sc.Dispatcher.Store()

		// Register alert LLM tools.
		sc.ToolReg.Register(alerttool.NewCreateAlertTool(alertStore, channelStore, sc.OrgID, sc.Logger))
		sc.ToolReg.Register(alerttool.NewSendNotificationTool(sc.Dispatcher, sc.OrgID, sc.Logger))
		logger.Debug("alert tools registered")

		// Start alert checker if alerting config is enabled.
		if cfg.Alerting != nil && cfg.Alerting.Enabled && gormDB != nil {
			var alertMetrics *alerting.Metrics
			if sc.Obs != nil && sc.Obs.Metrics != nil {
				alertMetrics = alerting.NewMetrics(sc.Obs.Metrics.Registry)
			}

			checker := alerting.NewChecker(
				alertStore,
				historyStore,
				func(db *gorm.DB) alerting.AlertRuleStore {
					return pgstore.NewAlertRuleRepository(db)
				},
				gormDB,
				sc.Dispatcher,
				sc.Security,
				alertMetrics,
				sc.Logger,
				&alerting.CheckerConfig{
					PollIntervalSeconds: cfg.Alerting.PollIntervalSeconds,
					MaxConcurrentChecks: cfg.Alerting.MaxConcurrentChecks,
				},
				sc.OrgID,
			)
			cancelChecker := checker.Start(ctx)
			defer cancelChecker()

			logger.Debug("alert checker initialized",
				slog.String("poll_interval", cfg.Alerting.AlertPollInterval().String()),
				slog.Int("max_concurrent", cfg.Alerting.AlertMaxConcurrent()),
			)
		}
	}

	// Initialize heartbeat task runner (optional, requires agent core + GORM DB).
	if cfg.HeartbeatTasks != nil && cfg.HeartbeatTasks.Enabled && gormDB != nil {
		hbtStore := sc.Store.HeartbeatTasks()
		hbtResultStore := sc.Store.HeartbeatTaskResults()

		// File poller: watches task directories for changes and syncs to DB.
		taskPoller := heartbeattask.NewTaskPoller(
			heartbeattask.TaskPollerConfig{
				TasksDirs:    cfg.HeartbeatTasks.TasksDirs,
				PollInterval: cfg.HeartbeatTasks.FilePollInterval(),
				OrgID:        sc.OrgID,
				DefaultUser:  cfg.HeartbeatTasks.DefaultUser(),
			},
			hbtStore,
			sc.Logger,
		)
		go taskPoller.Run(ctx)

		// Metrics (optional).
		var hbtMetrics *heartbeattask.Metrics
		if sc.Obs != nil && sc.Obs.Metrics != nil {
			hbtMetrics = heartbeattask.NewMetrics(sc.Obs.Metrics.Registry)
		}

		// Runner: polls for due tasks and executes them via agent.Process().
		hbtRunner := heartbeattask.New(
			hbtStore,
			hbtResultStore,
			func(db *gorm.DB) heartbeattask.HeartbeatTaskStore {
				return pgstore.NewHeartbeatTaskRepository(db)
			},
			gormDB,
			sc.AgentCore,
			sc.Security,
			sc.Dispatcher,
			hbtMetrics,
			sc.Logger,
			cfg.HeartbeatTasks,
			sc.OrgID,
		)
		// Proactive alerting on task failure.
		if cfg.HeartbeatTasks.ProactiveAlerts && sc.Dispatcher != nil {
			proactive := heartbeattask.NewProactiveHandler(sc.Dispatcher, sc.OrgID, sc.Logger)
			hbtRunner.WithProactiveHandler(proactive)
			sc.Logger.Debug("proactive heartbeat alerts enabled")
		}

		cancelHBTRunner := hbtRunner.Start(ctx)
		defer cancelHBTRunner()

		logger.Debug("heartbeat task runner initialized",
			slog.String("poll_interval", cfg.HeartbeatTasks.PollInterval().String()),
			slog.Int("max_concurrent", cfg.HeartbeatTasks.MaxConcurrent()),
			slog.Int("task_dirs", len(cfg.HeartbeatTasks.TasksDirs)),
		)
	}

	// Build enabled gateways.
	gateways := buildGateways(cfg, sc, wsServer, workflowEngine, cronStore, cronOrgID, alertStore, historyStore, channelStore)
	if len(gateways) == 0 {
		return fmt.Errorf("no gateways enabled in config")
	}
	logger.Info("gateways configured", slog.Int("count", len(gateways)))

	// Start all gateways in goroutines.
	errs := make(chan error, len(gateways))
	for _, gw := range gateways {
		go func(g gateway.Gateway) {
			errs <- g.Start(ctx)
		}(gw)
	}

	// Wait for signal or first gateway error.
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errs:
		if err != nil {
			logger.Error("gateway exited with error", slog.String("error", err.Error()))
		}
	}

	// Graceful shutdown with deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := len(gateways) - 1; i >= 0; i-- {
		if err := gateways[i].Stop(shutdownCtx); err != nil {
			logger.Error("stopping gateway", slog.String("error", err.Error()))
		}
	}

	return nil
}

// buildWorkflowEngine creates the orchestrator engine from shared components.
func buildWorkflowEngine(ctx context.Context, cfg *config.Config, sc *SharedComponents) orchestrator.WorkflowEngine {
	wfStore := sc.Store.Workflows()

	var wfMetrics *orchestrator.WorkflowMetrics
	if sc.Obs != nil && sc.Obs.Metrics != nil {
		wfMetrics = orchestrator.NewWorkflowMetrics(sc.Obs.Metrics.Registry)
	}

	factory := orchestrator.NewDefaultAgentFactory(
		sc.LLMProvider, sc.Security, sc.ApprovalMgr, sc.Obs, sc.ToolReg, sc.Logger,
	)

	engineCfg := orchestrator.EngineConfig{
		DefaultBudgetLimitUSD: cfg.Orchestrator.DefaultBudgetUSD,
		DefaultMaxDepth:       cfg.Orchestrator.DefaultMaxDepth,
		DefaultMaxTasks:       cfg.Orchestrator.DefaultMaxTasks,
		MaxConcurrentTasks:    cfg.Orchestrator.MaxConcurrentTasks,
		TaskTimeout:           time.Duration(cfg.Orchestrator.TaskTimeoutSeconds) * time.Second,
	}

	// Skill intelligence layer.
	skillStore := sc.Store.Skills()

	var skillMetrics *orchestrator.SkillMetrics
	if sc.Obs != nil && sc.Obs.Metrics != nil {
		skillMetrics = orchestrator.NewSkillMetrics(sc.Obs.Metrics.Registry)
	}

	skillTracker := orchestrator.NewSkillTracker(skillStore, skillMetrics, sc.Logger)
	skillScorer := orchestrator.NewSkillScorer(skillStore, orchestrator.DefaultSkillWeights())

	// Load skill definitions (polling or one-shot).
	skillDirs := cfg.Orchestrator.AllSkillsDirs()
	if len(skillDirs) > 0 {
		pollInterval := cfg.Orchestrator.SkillPollInterval()

		if pollInterval > 0 {
			onReload := func(defs []skillloader.SkillDefinition) {
				if orch, ok := sc.AgentCore.(*agent.Orchestrator); ok {
					if summary := skillloader.RenderSkillSummary(defs); summary != "" {
						orch.WithSkillSummary(summary)
					}
					orch.WithRunbooks(defsToRunbooks(defs))
				}
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
				sc.Logger,
			)
			go skillPoller.Run(ctx)
			sc.Logger.Debug("skill poller started (gateway)",
				slog.String("interval", pollInterval.String()),
				slog.Int("dirs", len(skillDirs)),
			)
		} else {
			loader := skillloader.NewLoader(sc.ToolReg, sc.Logger)
			for _, dir := range skillDirs {
				defs, result, err := loader.LoadDir(dir)
				if err != nil {
					sc.Logger.Warn("failed to load skill definitions",
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
					seedResult := skillloader.SeedSkills(ctx, skillStore, sc.OrgID, defs, skillMetrics, sc.Logger)
					sc.Logger.Debug("skill definitions seeded",
						slog.Int("seeded", seedResult.Seeded),
						slog.Int("skipped", seedResult.Skipped),
					)
					if orch, ok := sc.AgentCore.(*agent.Orchestrator); ok {
						if summary := skillloader.RenderSkillSummary(defs); summary != "" {
							orch.WithSkillSummary(summary)
							sc.Logger.Debug("skill summary injected into agent system prompt",
								slog.Int("skill_count", len(defs)),
							)
						}
						orch.WithRunbooks(defsToRunbooks(defs))
					}
				}
			}
		}
	}

	// Community skill packs.
	if len(cfg.Orchestrator.CommunityPacksDirs) > 0 {
		loader := skillloader.NewLoader(sc.ToolReg, sc.Logger)
		packLoader := skillloader.NewPackLoader(loader, sc.PolicyEnforcer, "", sc.Logger)
		for _, dir := range cfg.Orchestrator.CommunityPacksDirs {
			manifests, result, err := packLoader.LoadPack(ctx, dir)
			if err != nil {
				sc.Logger.Warn("failed to load community pack",
					slog.String("dir", dir),
					slog.String("error", err.Error()),
				)
				continue
			}
			if len(manifests) > 0 {
				defs := make([]skillloader.SkillDefinition, len(manifests))
				for i, m := range manifests {
					defs[i] = *m.ToSkillDefinition()
				}
				seedResult := skillloader.SeedSkills(ctx, skillStore, sc.OrgID, defs, skillMetrics, sc.Logger)
				sc.Logger.Debug("community pack loaded",
					slog.String("dir", dir),
					slog.Int("loaded", result.Loaded),
					slog.Int("seeded", seedResult.Seeded),
					slog.Int("errors", len(result.Errors)),
				)
			}
		}
	}

	engine := orchestrator.NewEngine(
		wfStore, factory, sc.Security, wfMetrics, sc.Logger, engineCfg,
	).WithSkills(skillTracker, skillScorer)

	sc.Logger.Debug("multi-agent orchestrator initialized",
		slog.Int("max_depth", engineCfg.DefaultMaxDepth),
		slog.Int("max_tasks", engineCfg.DefaultMaxTasks),
		slog.Int("max_concurrent", engineCfg.MaxConcurrentTasks),
	)

	return engine
}

// buildGateways creates all enabled gateways from config.
func buildGateways(cfg *config.Config, sc *SharedComponents, wsServer *ws.Server, wfEngine orchestrator.WorkflowEngine, cronStore scheduler.CronJobStore, cronOrgID uuid.UUID, alertStore alerting.AlertRuleStore, historyStore alerting.AlertHistoryStore, channelStore notification.ChannelStore) []gateway.Gateway {
	var gws []gateway.Gateway
	gwCfg := cfg.Gateways

	// Default to CLI if no gateways section configured.
	hasAnyGateway := gwCfg.CLI != nil || gwCfg.HTTP != nil || gwCfg.Slack != nil || gwCfg.Telegram != nil || gwCfg.Signal != nil || gwCfg.WebSocket != nil
	if !hasAnyGateway {
		gws = append(gws, cli.NewGateway(sc.AgentCore, sc.ApprovalMgr, sc.Logger))
		sc.Logger.Debug("gateway enabled", slog.String("type", "cli"), slog.String("reason", "default"))
		return gws
	}

	// CLI gateway.
	if gwCfg.CLI != nil && gwCfg.CLI.Enabled {
		gws = append(gws, cli.NewGateway(sc.AgentCore, sc.ApprovalMgr, sc.Logger))
		sc.Logger.Debug("gateway enabled", slog.String("type", "cli"))
	}

	// HTTP API gateway.
	var httpGW *httpapi.Gateway
	if gwCfg.HTTP != nil && gwCfg.HTTP.Enabled {
		limiter := ratelimit.NewLimiter(ratelimit.Config{
			RequestsPerMinute: gwCfg.HTTP.RateLimit.RequestsPerMinute,
			BurstSize:         gwCfg.HTTP.RateLimit.BurstSize,
		})

		// Build API key → user ID mapping from config + env override.
		apiKeys := gwCfg.HTTP.APIKeyUserMapping
		if apiKeys == nil {
			apiKeys = make(map[string]string)
		}
		if envKeys := os.Getenv("AKILI_API_KEYS"); envKeys != "" {
			for _, entry := range strings.Split(envKeys, ",") {
				parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
				if len(parts) == 2 {
					apiKeys[parts[0]] = parts[1]
				}
			}
		}

		httpCfg := httpapi.Config{
			ListenAddr:     gwCfg.HTTP.ListenAddr,
			EnableDocs:     gwCfg.HTTP.EnableDocs,
			APIKeys:        apiKeys,
			MaxRequestSize: gwCfg.HTTP.MaxRequestSizeBytes,
		}
		if sc.Obs != nil {
			httpCfg.Metrics = sc.Obs.Metrics
			httpCfg.HealthChecker = sc.Obs.Health
			if sc.Obs.Metrics != nil {
				httpCfg.MetricsRegistry = sc.Obs.Metrics.Registry
			}
			if sc.Obs.Tracer != nil {
				httpCfg.Tracer = sc.Obs.Tracer.Tracer()
			}
			if cfg.Observability != nil && cfg.Observability.Metrics != nil {
				httpCfg.MetricsPath = cfg.Observability.Metrics.Path
			}
		}
		httpGW = httpapi.NewGateway(httpCfg, sc.AgentCore, sc.ApprovalMgr, limiter, sc.Logger)
		if wfEngine != nil {
			httpGW.WithWorkflowEngine(wfEngine)
		}
		if cronStore != nil {
			httpGW.WithCronJobs(cronStore, cronOrgID)
		}
		if alertStore != nil && sc.Dispatcher != nil {
			httpGW.WithAlerts(alertStore, historyStore, channelStore, sc.Dispatcher, sc.OrgID)
		}
		if gwCfg.HTTP.SSE {
			httpGW.WithSSE(true)
			sc.Logger.Debug("SSE streaming endpoint enabled")
		}
	}

	// Mount WebSocket agent handler on the HTTP gateway if both are enabled.
	// Otherwise, start a standalone HTTP server for the WebSocket endpoint.
	if wsServer != nil && gwCfg.WebSocket != nil && gwCfg.WebSocket.Enabled {
		wsPath := gwCfg.WebSocket.WSPath()

		if httpGW != nil {
			// Mount on the HTTP gateway.
			httpGW.WithHandler(wsPath, wsServer.Handler())
			sc.Logger.Debug("websocket agent endpoint mounted on http gateway",
				slog.String("path", wsPath),
			)
		} else {
			// Start standalone WebSocket listener.
			addr := gwCfg.WebSocket.ListenAddr
			if addr == "" {
				addr = ":8081"
			}
			gws = append(gws, newStandaloneWSGateway(wsServer, addr, wsPath, sc.Logger))
			sc.Logger.Debug("gateway enabled",
				slog.String("type", "websocket"),
				slog.String("addr", addr),
				slog.String("path", wsPath),
			)
		}
	}

	if httpGW != nil {
		gws = append(gws, httpGW)
		sc.Logger.Debug("gateway enabled",
			slog.String("type", "http"),
			slog.String("addr", gwCfg.HTTP.ListenAddr),
			slog.Bool("workflows", wfEngine != nil),
			slog.Bool("cronjobs", cronStore != nil),
			slog.Bool("alerts", alertStore != nil),
			slog.Bool("websocket", wsServer != nil),
		)
	}

	// Slack gateway.
	if gwCfg.Slack != nil && gwCfg.Slack.Enabled {
		limiter := ratelimit.NewLimiter(ratelimit.Config{
			RequestsPerMinute: gwCfg.Slack.RateLimit.RequestsPerMinute,
			BurstSize:         gwCfg.Slack.RateLimit.BurstSize,
		})

		gws = append(gws, slack.NewGateway(slack.Config{
			SigningSecret:   gwCfg.Slack.SigningSecret,
			BotToken:        gwCfg.Slack.BotToken,
			ListenAddr:      gwCfg.Slack.ListenAddr,
			UserMapping:     gwCfg.Slack.UserMapping,
			ApprovalChannel: gwCfg.Slack.ApprovalChannel,
		}, sc.AgentCore, sc.ApprovalMgr, limiter, sc.Logger))
		sc.Logger.Debug("gateway enabled",
			slog.String("type", "slack"),
			slog.String("addr", gwCfg.Slack.ListenAddr),
		)
	}

	// Telegram gateway.
	if gwCfg.Telegram != nil && gwCfg.Telegram.Enabled {
		limiter := ratelimit.NewLimiter(ratelimit.Config{
			RequestsPerMinute: gwCfg.Telegram.RateLimit.RequestsPerMinute,
			BurstSize:         gwCfg.Telegram.RateLimit.BurstSize,
		})

		gws = append(gws, telegram.NewGateway(telegram.Config{
			BotToken:     gwCfg.Telegram.BotToken,
			WebhookURL:   gwCfg.Telegram.WebhookURL,
			ListenAddr:   gwCfg.Telegram.ListenAddr,
			AllowedUsers: gwCfg.Telegram.AllowedUsers,
			UserMapping:  gwCfg.Telegram.UserMapping,
			PollTimeout:  gwCfg.Telegram.PollTimeoutSeconds,
		}, sc.AgentCore, sc.ApprovalMgr, limiter, sc.Logger))

		mode := "long-polling"
		if gwCfg.Telegram.WebhookURL != "" {
			mode = "webhook"
		}
		sc.Logger.Debug("gateway enabled",
			slog.String("type", "telegram"),
			slog.String("mode", mode),
		)
	}

	// Signal gateway.
	if gwCfg.Signal != nil && gwCfg.Signal.Enabled {
		limiter := ratelimit.NewLimiter(ratelimit.Config{
			RequestsPerMinute: gwCfg.Signal.RateLimit.RequestsPerMinute,
			BurstSize:         gwCfg.Signal.RateLimit.BurstSize,
		})

		gws = append(gws, signalgw.NewGateway(signalgw.Config{
			APIURL:              gwCfg.Signal.APIURL,
			SenderNumber:        gwCfg.Signal.SenderNumber,
			AllowedUsers:        gwCfg.Signal.AllowedUsers,
			UserMapping:         gwCfg.Signal.UserMapping,
			PollIntervalSeconds: gwCfg.Signal.PollIntervalSeconds,
		}, sc.AgentCore, sc.ApprovalMgr, limiter, sc.Logger))
		sc.Logger.Debug("gateway enabled",
			slog.String("type", "signal"),
			slog.String("api_url", gwCfg.Signal.APIURL),
		)
	}

	return gws
}

// defsToRunbooks converts skill definitions to agent runbooks for dynamic injection.
func defsToRunbooks(defs []skillloader.SkillDefinition) []agent.Runbook {
	runbooks := make([]agent.Runbook, len(defs))
	for i, d := range defs {
		runbooks[i] = agent.Runbook{
			Name:        d.Name,
			Category:    d.Category,
			Description: d.Description,
		}
	}
	return runbooks
}

// storeGormDB extracts the underlying *gorm.DB from the unified store.
// Components like the scheduler and alerting checker need direct GORM access
// for transaction-based job claiming (SELECT FOR UPDATE SKIP LOCKED).
func storeGormDB(store storage.Store) *gorm.DB {
	switch s := store.(type) {
	case *pgstore.Store:
		return s.GormDB().GormDB()
	case *sqlitestore.Store:
		return s.GormDB()
	default:
		return nil
	}
}

// standaloneWSGateway wraps a ws.Server as a gateway.Gateway for cases
// where the HTTP gateway is not enabled and the WebSocket endpoint needs
// its own HTTP listener.
type standaloneWSGateway struct {
	wsServer   *ws.Server
	addr       string
	path       string
	logger     *slog.Logger
	httpServer *http.Server
}

func newStandaloneWSGateway(wsServer *ws.Server, addr, path string, logger *slog.Logger) *standaloneWSGateway {
	return &standaloneWSGateway{
		wsServer: wsServer,
		addr:     addr,
		path:     path,
		logger:   logger,
	}
}

func (g *standaloneWSGateway) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle(g.path, g.wsServer.Handler())

	g.httpServer = &http.Server{
		Addr:              g.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	g.logger.Info("standalone websocket gateway starting", slog.String("addr", g.addr))
	if err := g.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("websocket gateway: %w", err)
	}
	return nil
}

func (g *standaloneWSGateway) Stop(ctx context.Context) error {
	if g.httpServer != nil {
		return g.httpServer.Shutdown(ctx)
	}
	return nil
}
