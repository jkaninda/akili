// Package httpapi implements an HTTP API gateway for Akili.
//
// Security:
//   - API key authentication on every request (constant-time comparison)
//   - Request body size limits (default 1 MB)
//   - Per-user rate limiting via token bucket
//   - Strict JSON validation (disallow unknown fields)
//   - All requests logged with correlation IDs
//   - TLS expected via reverse proxy (not handled here)
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/alerting"
	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/observability"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/ratelimit"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/okapi"
)

const defaultMaxRequestSize = 1 << 20 // 1 MB

// ErrorBody is the standard error response used in OpenAPI documentation.
type ErrorBody struct {
	Error string `json:"error"`
}

// Config configures the HTTP API gateway.
type Config struct {
	ListenAddr     string // e.g., ":8080"
	EnableDocs     bool
	APIKeys        map[string]string // API key → user ID mapping. Keys from env.
	MaxRequestSize int64             // Maximum request body in bytes. 0 = 1 MB default.

	// Observability
	MetricsRegistry *prometheus.Registry            // Custom Prometheus registry for /metrics.
	MetricsPath     string                          // Path for metrics endpoint. Default: "/metrics".
	HealthChecker   *observability.HealthChecker    // Health checker for /ready endpoint.
	Metrics         *observability.MetricsCollector // Metrics collector for HTTP middleware.
	Tracer          trace.Tracer                    // OTel tracer for HTTP middleware.
}

// Gateway is the HTTP API gateway.
type Gateway struct {
	config       Config
	agent        agent.Agent
	approvalMgr  approval.ApprovalManager
	limiter      *ratelimit.Limiter
	logger       *slog.Logger
	server       *http.Server
	wfEngine     orchestrator.WorkflowEngine // nil = multi-agent disabled.
	cronStore    scheduler.CronJobStore      // nil = cron endpoints disabled.
	cronOrgID    uuid.UUID
	alertStore   alerting.AlertRuleStore // nil = alert endpoints disabled.
	historyStore alerting.AlertHistoryStore
	channelStore notification.ChannelStore
	dispatcher   *notification.Dispatcher
	alertOrgID   uuid.UUID

	// Streaming support.
	sseEnabled bool // Enable SSE streaming endpoint.

	// Extra handlers mounted on the HTTP mux (e.g., WebSocket agent endpoint).
	extraRoutes []extraRoute
	// o
	okapi *okapi.Okapi
	group *okapi.Group
}

// extraRoute stores an additional handler to be mounted on the HTTP mux.
type extraRoute struct {
	pattern string
	handler http.Handler
}

// NewGateway creates an HTTP API gateway.
func NewGateway(cfg Config, a agent.Agent, am approval.ApprovalManager, rl *ratelimit.Limiter, logger *slog.Logger) *Gateway {
	return &Gateway{
		config:      cfg,
		agent:       a,
		approvalMgr: am,
		limiter:     rl,
		logger:      logger,
		okapi:       okapi.New(okapi.WithMaxMultipartMemory(defaultMaxRequestSize)),
	}
}

// WithWorkflowEngine attaches a multi-agent workflow engine to the gateway.
func (g *Gateway) WithWorkflowEngine(engine orchestrator.WorkflowEngine) *Gateway {
	g.wfEngine = engine
	return g
}

// WithCronJobs attaches cron job management to the gateway.
func (g *Gateway) WithCronJobs(store scheduler.CronJobStore, orgID uuid.UUID) *Gateway {
	g.cronStore = store
	g.cronOrgID = orgID
	return g
}
func (g *Gateway) WithOpenAPIDocs() *Gateway {
	g.okapi.WithOpenAPIDocs(
		okapi.OpenAPI{
			Title:   "Akili",
			Version: "v0.0.1",
		},
	)
	return g
}

// WithAlerts attaches alert rule, history, and notification channel management to the gateway.
func (g *Gateway) WithAlerts(
	alertStore alerting.AlertRuleStore,
	historyStore alerting.AlertHistoryStore,
	channelStore notification.ChannelStore,
	dispatcher *notification.Dispatcher,
	orgID uuid.UUID,
) *Gateway {
	g.alertStore = alertStore
	g.historyStore = historyStore
	g.channelStore = channelStore
	g.dispatcher = dispatcher
	g.alertOrgID = orgID
	return g
}

// WithSSE enables the SSE streaming endpoint.
func (g *Gateway) WithSSE(enabled bool) *Gateway {
	g.sseEnabled = enabled
	return g
}

// WithHandler mounts an additional handler on the HTTP mux at the given pattern.
// Useful for adding the WebSocket agent endpoint alongside the API routes.
func (g *Gateway) WithHandler(pattern string, handler http.Handler) *Gateway {
	g.extraRoutes = append(g.extraRoutes, extraRoute{pattern: pattern, handler: handler})
	return g
}

// Start launches the HTTP server and blocks until it exits or ctx is canceled.
func (g *Gateway) Start(ctx context.Context) error {
	// Metrics/tracing middleware (applied globally).
	if g.config.Metrics != nil || g.config.Tracer != nil {
		g.okapi.UseMiddleware(func(next http.Handler) http.Handler {
			return observability.HTTPMetricsMiddleware(g.config.Metrics, g.config.Tracer, next)
		})
	}

	// Authenticated /v1 group.
	g.group = g.okapi.Group("/v1", g.authenticate)

	// Query endpoints.
	g.group.Post("/query", g.handleQuery,
		okapi.DocSummary("Send a query to the AI agent"),
		okapi.DocTags("Query"),
		okapi.DocRequestBody(QueryRequest{}),
		okapi.DocResponse(QueryResponse{}),
		okapi.DocResponse(http.StatusAccepted, ApprovalPendingResponse{}),
		okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		okapi.DocResponse(http.StatusUnauthorized, ErrorBody{}),
		okapi.DocResponse(http.StatusTooManyRequests, ErrorBody{}),
	)
	g.group.Post("/approve", g.handleApprove,
		okapi.DocSummary("Approve or deny a pending action"),
		okapi.DocTags("Query"),
		okapi.DocRequestBody(ApproveRequest{}),
		okapi.DocResponse(ApproveResponse{}),
		okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		okapi.DocResponse(http.StatusUnauthorized, ErrorBody{}),
		okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
	)
	g.group.Get("/healthz", g.handleHealth,
		okapi.DocSummary("Authenticated health check"),
		okapi.DocTags("Health"),
		okapi.DocResponse(HealthResponse{}),
	)

	// SSE streaming endpoint.
	if g.sseEnabled {
		g.group.Post("/query/stream", g.handleQueryStream,
			okapi.DocSummary("Stream a query response via SSE"),
			okapi.DocTags("Query"),
			okapi.DocRequestBody(QueryRequest{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
			okapi.DocResponse(http.StatusUnauthorized, ErrorBody{}),
		)
	}

	// Workflow endpoints (only if workflow engine is configured).
	if g.wfEngine != nil {
		g.group.Post("/workflows", g.handleWorkflowSubmit,
			okapi.DocSummary("Submit a new multi-agent workflow"),
			okapi.DocTags("Workflows"),
			okapi.DocRequestBody(WorkflowSubmitRequest{}),
			okapi.DocResponse(http.StatusAccepted, WorkflowResponse{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
			okapi.DocResponse(http.StatusUnauthorized, ErrorBody{}),
		)
		g.group.Get("/workflows/{id}", g.handleWorkflowStatus,
			okapi.DocSummary("Get workflow status"),
			okapi.DocTags("Workflows"),
			okapi.DocPathParam("id", "string", "Workflow ID (UUID)"),
			okapi.DocResponse(WorkflowResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Post("/workflows/{id}/cancel", g.handleWorkflowCancel,
			okapi.DocSummary("Cancel a running workflow"),
			okapi.DocTags("Workflows"),
			okapi.DocPathParam("id", "string", "Workflow ID (UUID)"),
			okapi.DocResponse(map[string]string{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		)
		g.group.Get("/workflows/{id}/tasks", g.handleWorkflowTasks,
			okapi.DocSummary("List tasks in a workflow"),
			okapi.DocTags("Workflows"),
			okapi.DocPathParam("id", "string", "Workflow ID (UUID)"),
			okapi.DocResponse([]TaskResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
	}

	// CronJob endpoints (only if cron store is configured).
	if g.cronStore != nil {
		g.group.Post("/cronjobs", g.handleCronJobCreate,
			okapi.DocSummary("Create a new cron job"),
			okapi.DocTags("CronJobs"),
			okapi.DocRequestBody(CronJobRequest{}),
			okapi.DocResponse(http.StatusCreated, CronJobResponse{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		)
		g.group.Get("/cronjobs", g.handleCronJobList,
			okapi.DocSummary("List all cron jobs"),
			okapi.DocTags("CronJobs"),
			okapi.DocResponse([]CronJobResponse{}),
		)
		g.group.Get("/cronjobs/{id}", g.handleCronJobGet,
			okapi.DocSummary("Get a cron job by ID"),
			okapi.DocTags("CronJobs"),
			okapi.DocPathParam("id", "string", "CronJob ID (UUID)"),
			okapi.DocResponse(CronJobResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Put("/cronjobs/{id}", g.handleCronJobUpdate,
			okapi.DocSummary("Update a cron job"),
			okapi.DocTags("CronJobs"),
			okapi.DocPathParam("id", "string", "CronJob ID (UUID)"),
			okapi.DocRequestBody(CronJobRequest{}),
			okapi.DocResponse(CronJobResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Delete("/cronjobs/{id}", g.handleCronJobDelete,
			okapi.DocSummary("Delete a cron job"),
			okapi.DocTags("CronJobs"),
			okapi.DocPathParam("id", "string", "CronJob ID (UUID)"),
			okapi.DocResponse(map[string]string{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Post("/cronjobs/{id}/trigger", g.handleCronJobTrigger,
			okapi.DocSummary("Manually trigger a cron job"),
			okapi.DocTags("CronJobs"),
			okapi.DocPathParam("id", "string", "CronJob ID (UUID)"),
			okapi.DocResponse(http.StatusAccepted, WorkflowResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
	}

	// Alert rule endpoints (only if alert store is configured).
	if g.alertStore != nil {
		g.group.Post("/alerts", g.handleAlertCreate,
			okapi.DocSummary("Create a new alert rule"),
			okapi.DocTags("Alerts"),
			okapi.DocRequestBody(AlertRuleRequest{}),
			okapi.DocResponse(http.StatusCreated, AlertRuleResponse{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		)
		g.group.Get("/alerts", g.handleAlertList,
			okapi.DocSummary("List all alert rules"),
			okapi.DocTags("Alerts"),
			okapi.DocResponse([]AlertRuleResponse{}),
		)
		g.group.Get("/alerts/{id}", g.handleAlertGet,
			okapi.DocSummary("Get an alert rule by ID"),
			okapi.DocTags("Alerts"),
			okapi.DocPathParam("id", "string", "Alert rule ID (UUID)"),
			okapi.DocResponse(AlertRuleResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Put("/alerts/{id}", g.handleAlertUpdate,
			okapi.DocSummary("Update an alert rule"),
			okapi.DocTags("Alerts"),
			okapi.DocPathParam("id", "string", "Alert rule ID (UUID)"),
			okapi.DocRequestBody(AlertRuleRequest{}),
			okapi.DocResponse(AlertRuleResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Delete("/alerts/{id}", g.handleAlertDelete,
			okapi.DocSummary("Delete an alert rule"),
			okapi.DocTags("Alerts"),
			okapi.DocPathParam("id", "string", "Alert rule ID (UUID)"),
			okapi.DocResponse(map[string]string{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Get("/alerts/{id}/history", g.handleAlertHistory,
			okapi.DocSummary("Get alert rule history"),
			okapi.DocTags("Alerts"),
			okapi.DocPathParam("id", "string", "Alert rule ID (UUID)"),
			okapi.DocResponse([]AlertHistoryResponse{}),
		)
	}

	// Notification channel endpoints (only if channel store is configured).
	if g.channelStore != nil {
		g.group.Post("/notification-channels", g.handleChannelCreate,
			okapi.DocSummary("Create a notification channel"),
			okapi.DocTags("Notification Channels"),
			okapi.DocRequestBody(NotificationChannelRequest{}),
			okapi.DocResponse(http.StatusCreated, NotificationChannelResponse{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		)
		g.group.Get("/notification-channels", g.handleChannelList,
			okapi.DocSummary("List all notification channels"),
			okapi.DocTags("Notification Channels"),
			okapi.DocResponse([]NotificationChannelResponse{}),
		)
		g.group.Get("/notification-channels/{id}", g.handleChannelGet,
			okapi.DocSummary("Get a notification channel by ID"),
			okapi.DocTags("Notification Channels"),
			okapi.DocPathParam("id", "string", "Channel ID (UUID)"),
			okapi.DocResponse(NotificationChannelResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Put("/notification-channels/{id}", g.handleChannelUpdate,
			okapi.DocSummary("Update a notification channel"),
			okapi.DocTags("Notification Channels"),
			okapi.DocPathParam("id", "string", "Channel ID (UUID)"),
			okapi.DocRequestBody(NotificationChannelRequest{}),
			okapi.DocResponse(NotificationChannelResponse{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Delete("/notification-channels/{id}", g.handleChannelDelete,
			okapi.DocSummary("Delete a notification channel"),
			okapi.DocTags("Notification Channels"),
			okapi.DocPathParam("id", "string", "Channel ID (UUID)"),
			okapi.DocResponse(map[string]string{}),
			okapi.DocResponse(http.StatusNotFound, ErrorBody{}),
		)
		g.group.Post("/notification-channels/{id}/test", g.handleChannelTest,
			okapi.DocSummary("Send a test notification"),
			okapi.DocTags("Notification Channels"),
			okapi.DocPathParam("id", "string", "Channel ID (UUID)"),
			okapi.DocResponse(map[string]string{}),
			okapi.DocResponse(http.StatusBadRequest, ErrorBody{}),
		)
	}

	// Extra handlers (e.g., WebSocket agent endpoint).
	for _, er := range g.extraRoutes {
		g.okapi.HandleStd("GET", er.pattern, er.handler.ServeHTTP)
	}

	// Observability endpoints (unauthenticated).
	g.okapi.Get("/healthz", g.handleLiveness)
	g.okapi.Get("/readyz", g.handleReadiness)

	if g.config.MetricsRegistry != nil {
		path := g.config.MetricsPath
		if path == "" {
			path = "/metrics"
		}
		g.okapi.HandleStd("GET", path, promhttp.HandlerFor(g.config.MetricsRegistry, promhttp.HandlerOpts{}).ServeHTTP)
	}
	if g.config.EnableDocs {
		g.WithOpenAPIDocs()
	}

	g.server = &http.Server{
		Addr:              g.config.ListenAddr,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}

	g.logger.Info("http api gateway starting", slog.String("addr", g.config.ListenAddr))

	// err := g.server.ListenAndServe()
	// if errors.Is(err, http.ErrServerClosed) {
	// 	return nil
	// }
	return g.okapi.StartServer(g.server)
}

// Stop gracefully shuts down the HTTP server.
func (g *Gateway) Stop(ctx context.Context) error {
	if g.server == nil {
		return nil
	}
	g.logger.Info("http api gateway stopping")
	return g.okapi.Shutdown(g.server)
	// g.server.Shutdown(ctx)
}

// --- Handlers ---

// QueryRequest is the JSON body for POST /v1/query.
type QueryRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"` // Empty = new conversation.
}

// QueryResponse is the JSON response for POST /v1/query.
type QueryResponse struct {
	Message        string `json:"message"`
	CorrelationID  string `json:"correlation_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	TokensUsed     int    `json:"tokens_used,omitempty"`
}

// ApprovalPendingResponse is returned with HTTP 202 when approval is needed.
type ApprovalPendingResponse struct {
	ApprovalID    string `json:"approval_id"`
	Action        string `json:"action"`
	Tool          string `json:"tool"`
	RiskLevel     string `json:"risk_level"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id"`
}

func (g *Gateway) handleQuery(c *okapi.Context) error {
	// Authenticate.
	userID := c.GetString("userID")

	// Rate limit.
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	// Parse request.
	var req QueryRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("message is required")

	}
	if req.Message == "" {
		return c.AbortBadRequest("message is required")
	}

	correlationID := newCorrelationID()

	// Resolve conversation ID: use client-provided or generate new.
	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = uuid.New().String()
	}

	g.logger.Info("http query",
		slog.String("user_id", userID),
		slog.String("correlation_id", correlationID),
		slog.String("conversation_id", conversationID),
	)

	// Process through agent.
	resp, err := g.agent.Process(c.Context(), &agent.Input{
		UserID:         userID,
		Message:        req.Message,
		CorrelationID:  correlationID,
		ConversationID: conversationID,
	})
	if err != nil {
		// Check if approval is pending.
		var approvalErr *agent.ErrApprovalPending
		if errors.As(err, &approvalErr) {
			return c.JSON(http.StatusAccepted, ApprovalPendingResponse{
				ApprovalID:    approvalErr.ApprovalID,
				Action:        approvalErr.ActionName,
				Tool:          approvalErr.ToolName,
				RiskLevel:     approvalErr.RiskLevel,
				Message:       "Approval required. POST to /v1/approve to approve or deny.",
				CorrelationID: correlationID,
			})
		}

		g.logger.Error("agent processing failed",
			slog.String("correlation_id", correlationID),
			slog.String("error", err.Error()),
		)
		return c.AbortInternalServerError("processing failed")
	}

	return c.OK(QueryResponse{
		Message:        resp.Message,
		CorrelationID:  correlationID,
		ConversationID: conversationID,
		TokensUsed:     resp.TokensUsed,
	})
}

// ApproveRequest is the JSON body for POST /v1/approve.
type ApproveRequest struct {
	ApprovalID string `json:"approval_id"`
	Decision   string `json:"decision"` // "approve" or "deny"
}

// ApproveResponse is the JSON response after approval.
type ApproveResponse struct {
	ApprovalID string         `json:"approval_id"`
	Status     string         `json:"status"`
	Output     string         `json:"output,omitempty"`
	Success    *bool          `json:"success,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func (g *Gateway) handleApprove(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	var req ApproveRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}
	if req.ApprovalID == "" {
		return c.AbortBadRequest("approval_id is required")
	}
	if req.Decision != "approve" && req.Decision != "deny" {
		return c.AbortBadRequest("decision must be \"approve\" or \"deny\"")
	}

	g.logger.Info("http approval",
		slog.String("user_id", userID),
		slog.String("approval_id", req.ApprovalID),
		slog.String("decision", req.Decision),
	)

	if g.approvalMgr == nil {
		return c.AbortServiceUnavailable("approval manager not configured")
	}

	if req.Decision == "deny" {
		if err := g.approvalMgr.Deny(c.Context(), req.ApprovalID, userID); err != nil {
			return approvalError(c, err)
		}
		return c.OK(ApproveResponse{
			ApprovalID: req.ApprovalID,
			Status:     "denied",
		})
	}

	// Approve and resume execution.
	if err := g.approvalMgr.Approve(c.Context(), req.ApprovalID, userID); err != nil {
		return approvalError(c, err)
	}

	result, err := g.agent.ResumeWithApproval(c.Context(), req.ApprovalID)
	if err != nil {
		g.logger.Error("execution after approval failed",
			slog.String("approval_id", req.ApprovalID),
			slog.String("error", err.Error()),
		)
		return c.AbortInternalServerError("execution after approval failed")
	}

	return c.OK(ApproveResponse{
		ApprovalID: req.ApprovalID,
		Status:     "approved",
		Output:     result.Output,
		Success:    &result.Success,
		Metadata:   result.Metadata,
	})
}

// HealthResponse is the JSON response for GET /v1/healthz.
type HealthResponse struct {
	Status string `json:"status"`
}

func (g *Gateway) handleHealth(c *okapi.Context) error {
	return c.OK(&HealthResponse{Status: "ok"})
}

// handleLiveness is the Kubernetes liveness probe
func (g *Gateway) handleLiveness(c *okapi.Context) error {
	return c.OK(&HealthResponse{Status: "ok"})
}

// handleReadiness checks all registered dependencies and returns 200 or 503.
func (g *Gateway) handleReadiness(c *okapi.Context) error {
	if g.config.HealthChecker == nil {
		return c.OK(&HealthResponse{Status: "ok"})

	}

	status := g.config.HealthChecker.CheckReady(c.Context())
	code := http.StatusOK
	if status.Status != "ok" {
		code = http.StatusServiceUnavailable
	}
	return c.JSON(code, status)

}

// --- Workflow Handlers ---

// WorkflowSubmitRequest is the JSON body for POST /v1/workflows.
type WorkflowSubmitRequest struct {
	Goal           string  `json:"goal"`
	BudgetLimitUSD float64 `json:"budget_limit_usd,omitempty"`
	MaxDepth       int     `json:"max_depth,omitempty"`
	MaxTasks       int     `json:"max_tasks,omitempty"`
}

// WorkflowResponse is the JSON response for workflow endpoints.
type WorkflowResponse struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	Goal           string  `json:"goal"`
	CorrelationID  string  `json:"correlation_id"`
	TaskCount      int     `json:"task_count"`
	BudgetSpentUSD float64 `json:"budget_spent_usd"`
	Error          string  `json:"error,omitempty"`
}

func (g *Gateway) handleWorkflowSubmit(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	var req WorkflowSubmitRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}
	if req.Goal == "" {
		return c.AbortBadRequest("goal is required")
	}

	correlationID := newCorrelationID()

	wf, err := g.wfEngine.Submit(c.Context(), &orchestrator.WorkflowRequest{
		UserID:         userID,
		Goal:           req.Goal,
		CorrelationID:  correlationID,
		BudgetLimitUSD: req.BudgetLimitUSD,
		MaxDepth:       req.MaxDepth,
		MaxTasks:       req.MaxTasks,
	})
	if err != nil {
		g.logger.Error("workflow submission failed",
			slog.String("correlation_id", correlationID),
			slog.String("error", err.Error()),
		)
		return c.AbortInternalServerError("workflow submission failed")
	}

	return c.JSON(http.StatusAccepted, WorkflowResponse{
		ID:            wf.ID.String(),
		Status:        string(wf.Status),
		Goal:          wf.Goal,
		CorrelationID: wf.CorrelationID,
		TaskCount:     wf.TaskCount,
	})
}

func (g *Gateway) handleWorkflowStatus(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid workflow ID")
	}

	wf, err := g.wfEngine.Status(c.Context(), id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workflow not found"})
	}

	return c.OK(WorkflowResponse{
		ID:             wf.ID.String(),
		Status:         string(wf.Status),
		Goal:           wf.Goal,
		CorrelationID:  wf.CorrelationID,
		TaskCount:      wf.TaskCount,
		BudgetSpentUSD: wf.BudgetSpentUSD,
		Error:          wf.Error,
	})
}

func (g *Gateway) handleWorkflowCancel(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid workflow ID")
	}

	if err := g.wfEngine.Cancel(c.Context(), id); err != nil {
		return c.AbortInternalServerError("cancellation failed")
	}

	return c.OK(map[string]string{"status": "cancelled"})
}

// TaskResponse is a single task in the workflow task list.
type TaskResponse struct {
	ID          string  `json:"id"`
	AgentRole   string  `json:"agent_role"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	Depth       int     `json:"depth"`
	CostUSD     float64 `json:"cost_usd,omitempty"`
	Error       string  `json:"error,omitempty"`
}

func (g *Gateway) handleWorkflowTasks(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid workflow ID")
	}

	tasks, err := g.wfEngine.ListTasks(c.Context(), id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workflow not found"})
	}

	resp := make([]TaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = TaskResponse{
			ID:          t.ID.String(),
			AgentRole:   string(t.AgentRole),
			Description: t.Description,
			Status:      string(t.Status),
			Depth:       t.Depth,
			CostUSD:     t.CostUSD,
			Error:       t.Error,
		}
	}

	return c.OK(resp)
}

// --- Authentication ---

// authenticate validates the API key and returns the mapped user ID.
// Writes an error response and returns false if authentication fails.
func (g *Gateway) authenticate(next okapi.HandlerFunc) okapi.HandlerFunc {
	return func(c *okapi.Context) error {
		authHeader := c.Header("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			return c.AbortUnauthorized("missing or invalid Authorization header")
		}
		apiKey := strings.TrimPrefix(authHeader, "Bearer ")

		userID := ""

		for key, userId := range g.config.APIKeys {
			if subtle.ConstantTimeCompare([]byte(apiKey), []byte(key)) == 1 {
				userID = userId
			}
		}
		if userID == "" {
			return c.AbortUnauthorized("invalid API key")

		}
		c.Set("userID", userID)
		return next(c)

	}
}

// --- Helpers ---

// approvalError maps approval errors to appropriate HTTP responses.
func approvalError(c *okapi.Context, err error) error {
	switch {
	case errors.Is(err, approval.ErrNotFound):
		return c.JSON(http.StatusNotFound, okapi.M{"error": "approval not found"})
	case errors.Is(err, approval.ErrExpired):
		return c.JSON(http.StatusGone, okapi.M{"error": "approval expired"})
	case errors.Is(err, approval.ErrAlreadyResolved):
		return c.JSON(http.StatusConflict, okapi.M{"error": "approval already resolved"})
	default:
		return c.AbortInternalServerError("approval error")
	}
}

func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
