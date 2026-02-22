package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/orchestrator"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/okapi"
)

// **** CronJob request/response types ****

// CronJobRequest is the JSON body for POST/PUT /v1/cronjobs.
type CronJobRequest struct {
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	CronExpression string  `json:"cron_expression"`
	Goal           string  `json:"goal"`
	BudgetLimitUSD float64 `json:"budget_limit_usd,omitempty"`
	MaxDepth       int     `json:"max_depth,omitempty"`
	MaxTasks       int     `json:"max_tasks,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"` // Pointer to distinguish absent from false.
}

// CronJobResponse is the JSON response for cron job endpoints.
type CronJobResponse struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	CronExpression string     `json:"cron_expression"`
	Goal           string     `json:"goal"`
	UserID         string     `json:"user_id"`
	BudgetLimitUSD float64    `json:"budget_limit_usd"`
	MaxDepth       int        `json:"max_depth"`
	MaxTasks       int        `json:"max_tasks"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	LastWorkflowID string     `json:"last_workflow_id,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toCronJobResponse(cj *domain.CronJob) CronJobResponse {
	resp := CronJobResponse{
		ID:             cj.ID.String(),
		Name:           cj.Name,
		Description:    cj.Description,
		CronExpression: cj.CronExpression,
		Goal:           cj.Goal,
		UserID:         cj.UserID,
		BudgetLimitUSD: cj.BudgetLimitUSD,
		MaxDepth:       cj.MaxDepth,
		MaxTasks:       cj.MaxTasks,
		Enabled:        cj.Enabled,
		NextRunAt:      cj.NextRunAt,
		LastRunAt:      cj.LastRunAt,
		LastError:      cj.LastError,
		CreatedAt:      cj.CreatedAt,
		UpdatedAt:      cj.UpdatedAt,
	}
	if cj.LastWorkflowID != nil {
		resp.LastWorkflowID = cj.LastWorkflowID.String()
	}
	return resp
}

// **** Handlers ****

func (g *Gateway) handleCronJobCreate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	var req CronJobRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}
	if req.Name == "" {
		return c.AbortBadRequest("name is required")
	}
	if req.CronExpression == "" {
		return c.AbortBadRequest("cron_expression is required")
	}
	if req.Goal == "" {
		return c.AbortBadRequest("goal is required")
	}

	// Validate cron expression.
	nextRun, err := scheduler.ComputeNextRunFrom(req.CronExpression, time.Now().UTC())
	if err != nil {
		return c.AbortBadRequest(fmt.Sprintf("invalid cron_expression: %v", err))
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	now := time.Now().UTC()
	cj := &domain.CronJob{
		ID:             uuid.New(),
		OrgID:          g.cronOrgID,
		Name:           req.Name,
		Description:    req.Description,
		CronExpression: req.CronExpression,
		Goal:           req.Goal,
		// Job runs as the creator's identity.
		UserID:         userID,
		BudgetLimitUSD: req.BudgetLimitUSD,
		MaxDepth:       req.MaxDepth,
		MaxTasks:       req.MaxTasks,
		Enabled:        enabled,
		NextRunAt:      &nextRun,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := g.cronStore.Create(c.Context(), cj); err != nil {
		g.logger.Error("cron job creation failed", slog.String("error", err.Error()))
		return c.AbortInternalServerError("failed to create cron job")
	}

	g.logger.Info("cron job created",
		slog.String("cronjob_id", cj.ID.String()),
		slog.String("user_id", userID),
		slog.String("cron_expression", cj.CronExpression),
	)

	return c.JSON(http.StatusCreated, toCronJobResponse(cj))
}

func (g *Gateway) handleCronJobList(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	jobs, err := g.cronStore.List(c.Context(), g.cronOrgID)
	if err != nil {
		return c.AbortInternalServerError("failed to list cron jobs")
	}

	resp := make([]CronJobResponse, len(jobs))
	for i := range jobs {
		resp[i] = toCronJobResponse(&jobs[i])
	}
	return c.OK(resp)
}

func (g *Gateway) handleCronJobGet(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid cron job ID")
	}

	cj, err := g.cronStore.Get(c.Context(), g.cronOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "cron job not found"})
	}

	return c.OK(toCronJobResponse(cj))
}

func (g *Gateway) handleCronJobUpdate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid cron job ID")
	}

	cj, err := g.cronStore.Get(c.Context(), g.cronOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "cron job not found"})
	}

	var req CronJobRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}

	if req.Name != "" {
		cj.Name = req.Name
	}
	if req.Description != "" {
		cj.Description = req.Description
	}
	if req.Goal != "" {
		cj.Goal = req.Goal
	}
	if req.CronExpression != "" {
		nextRun, cronErr := scheduler.ComputeNextRunFrom(req.CronExpression, time.Now().UTC())
		if cronErr != nil {
			return c.AbortBadRequest(fmt.Sprintf("invalid cron_expression: %v", cronErr))
		}
		cj.CronExpression = req.CronExpression
		cj.NextRunAt = &nextRun
	}
	if req.BudgetLimitUSD > 0 {
		cj.BudgetLimitUSD = req.BudgetLimitUSD
	}
	if req.MaxDepth > 0 {
		cj.MaxDepth = req.MaxDepth
	}
	if req.MaxTasks > 0 {
		cj.MaxTasks = req.MaxTasks
	}
	if req.Enabled != nil {
		cj.Enabled = *req.Enabled
	}

	// Security: UserID is NOT updatable. The job always runs as its creator.
	// This prevents privilege escalation through schedule manipulation.
	cj.UpdatedAt = time.Now().UTC()

	if err := g.cronStore.Update(c.Context(), cj); err != nil {
		return c.AbortInternalServerError("failed to update cron job")
	}

	g.logger.Info("cron job updated",
		slog.String("cronjob_id", cj.ID.String()),
		slog.String("updated_by", userID),
	)

	return c.OK(toCronJobResponse(cj))
}

func (g *Gateway) handleCronJobDelete(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid cron job ID")
	}

	if err := g.cronStore.Delete(c.Context(), g.cronOrgID, id); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "cron job not found"})
	}

	g.logger.Info("cron job deleted",
		slog.String("cronjob_id", id.String()),
		slog.String("deleted_by", userID),
	)

	return c.OK(map[string]string{"status": "deleted"})
}

func (g *Gateway) handleCronJobTrigger(c *okapi.Context) error {
	// Manual trigger uses the REQUESTING user's identity, not the job creator.
	// This ensures manual triggers are subject to the triggering user's RBAC.
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	if g.wfEngine == nil {
		return c.AbortServiceUnavailable("workflow engine not available")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid cron job ID")
	}

	cj, err := g.cronStore.Get(c.Context(), g.cronOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "cron job not found"})
	}

	correlationID := newCorrelationID()

	wf, err := g.wfEngine.Submit(c.Context(), &orchestrator.WorkflowRequest{
		UserID:         userID,
		OrgID:          cj.OrgID,
		Goal:           cj.Goal,
		CorrelationID:  correlationID,
		BudgetLimitUSD: cj.BudgetLimitUSD,
		MaxDepth:       cj.MaxDepth,
		MaxTasks:       cj.MaxTasks,
	})
	if err != nil {
		g.logger.Error("cron job manual trigger failed",
			slog.String("cronjob_id", id.String()),
			slog.String("error", err.Error()),
		)
		return c.AbortInternalServerError("workflow submission failed")
	}

	g.logger.Info("cron job manually triggered",
		slog.String("cronjob_id", id.String()),
		slog.String("triggered_by", userID),
		slog.String("workflow_id", wf.ID.String()),
	)

	return c.JSON(http.StatusAccepted, WorkflowResponse{
		ID:            wf.ID.String(),
		Status:        string(wf.Status),
		Goal:          wf.Goal,
		CorrelationID: wf.CorrelationID,
		TaskCount:     wf.TaskCount,
	})
}
