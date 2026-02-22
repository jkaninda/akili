package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/notification"
	"github.com/jkaninda/akili/internal/scheduler"
	"github.com/jkaninda/okapi"
)

// **** Alert Rule request/response types ****

type AlertRuleRequest struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Target         string            `json:"target"`
	CheckType      string            `json:"check_type"`
	CheckConfig    map[string]string `json:"check_config"`
	CronExpression string            `json:"cron_expression"`
	Channels       []string          `json:"channels"`
	CooldownS      int               `json:"cooldown_seconds,omitempty"`
	Enabled        *bool             `json:"enabled,omitempty"`
}

type AlertRuleResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Target         string            `json:"target"`
	CheckType      string            `json:"check_type"`
	CheckConfig    map[string]string `json:"check_config"`
	CronExpression string            `json:"cron_expression"`
	ChannelIDs     []string          `json:"channel_ids"`
	UserID         string            `json:"user_id"`
	Enabled        bool              `json:"enabled"`
	CooldownS      int               `json:"cooldown_seconds"`
	LastCheckedAt  *time.Time        `json:"last_checked_at,omitempty"`
	LastAlertedAt  *time.Time        `json:"last_alerted_at,omitempty"`
	LastStatus     string            `json:"last_status"`
	NextRunAt      *time.Time        `json:"next_run_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func toAlertRuleResponse(r *domain.AlertRule) AlertRuleResponse {
	chIDs := make([]string, len(r.ChannelIDs))
	for i, id := range r.ChannelIDs {
		chIDs[i] = id.String()
	}
	return AlertRuleResponse{
		ID:             r.ID.String(),
		Name:           r.Name,
		Description:    r.Description,
		Target:         r.Target,
		CheckType:      r.CheckType,
		CheckConfig:    r.CheckConfig,
		CronExpression: r.CronExpression,
		ChannelIDs:     chIDs,
		UserID:         r.UserID,
		Enabled:        r.Enabled,
		CooldownS:      r.CooldownS,
		LastCheckedAt:  r.LastCheckedAt,
		LastAlertedAt:  r.LastAlertedAt,
		LastStatus:     r.LastStatus,
		NextRunAt:      r.NextRunAt,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
}

// **** Notification Channel request/response types ****

type NotificationChannelRequest struct {
	Name          string            `json:"name"`
	ChannelType   string            `json:"channel_type"`
	Config        map[string]string `json:"config"`
	CredentialRef string            `json:"credential_ref,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
}

type NotificationChannelResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	ChannelType string            `json:"channel_type"`
	Config      map[string]string `json:"config"`
	Enabled     bool              `json:"enabled"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func toNotificationChannelResponse(ch *domain.NotificationChannel) NotificationChannelResponse {
	return NotificationChannelResponse{
		ID:          ch.ID.String(),
		Name:        ch.Name,
		ChannelType: ch.ChannelType,
		Config:      ch.Config,
		Enabled:     ch.Enabled,
		CreatedAt:   ch.CreatedAt,
		UpdatedAt:   ch.UpdatedAt,
	}
}

// **** Alert History response type ****

type AlertHistoryResponse struct {
	ID              string    `json:"id"`
	AlertRuleID     string    `json:"alert_rule_id"`
	Status          string    `json:"status"`
	PreviousStatus  string    `json:"previous_status"`
	Message         string    `json:"message"`
	NotifiedVia     []string  `json:"notified_via"`
	NotifyErrors    []string  `json:"notify_errors,omitempty"`
	CheckDurationMS int64     `json:"check_duration_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

func toAlertHistoryResponse(h *domain.AlertHistory) AlertHistoryResponse {
	return AlertHistoryResponse{
		ID:              h.ID.String(),
		AlertRuleID:     h.AlertRuleID.String(),
		Status:          h.Status,
		PreviousStatus:  h.PreviousStatus,
		Message:         h.Message,
		NotifiedVia:     h.NotifiedVia,
		NotifyErrors:    h.NotifyErrors,
		CheckDurationMS: h.CheckDurationMS,
		CreatedAt:       h.CreatedAt,
	}
}

// **** Alert Rule Handlers ****

func (g *Gateway) handleAlertCreate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	if g.limiter != nil {
		if err := g.limiter.Allow(userID); err != nil {
			return c.AbortTooManyRequests("rate limit exceeded")
		}
	}

	var req AlertRuleRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}
	if req.Name == "" {
		return c.AbortBadRequest("name is required")
	}
	if req.Target == "" {
		return c.AbortBadRequest("target is required")
	}
	if req.CheckType == "" {
		return c.AbortBadRequest("check_type is required")
	}
	if req.CronExpression == "" {
		return c.AbortBadRequest("cron_expression is required")
	}
	if len(req.Channels) == 0 {
		return c.AbortBadRequest("channels is required")
	}

	nextRun, err := scheduler.ComputeNextRunFrom(req.CronExpression, time.Now().UTC())
	if err != nil {
		return c.AbortBadRequest(fmt.Sprintf("invalid cron_expression: %v", err))
	}

	// Resolve channel names to IDs.
	var channelIDs []uuid.UUID
	for _, name := range req.Channels {
		ch, err := g.channelStore.GetByName(c.Context(), g.alertOrgID, name)
		if err != nil {
			return c.AbortBadRequest(fmt.Sprintf("notification channel %q not found", name))
		}
		channelIDs = append(channelIDs, ch.ID)
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	cooldownS := 300
	if req.CooldownS > 0 {
		cooldownS = req.CooldownS
	}

	// Default: set url from target for http_status checks.
	checkConfig := req.CheckConfig
	if checkConfig == nil {
		checkConfig = make(map[string]string)
	}
	if req.CheckType == "http_status" && checkConfig["url"] == "" {
		checkConfig["url"] = req.Target
	}

	now := time.Now().UTC()
	rule := &domain.AlertRule{
		ID:             uuid.New(),
		OrgID:          g.alertOrgID,
		Name:           req.Name,
		Description:    req.Description,
		Target:         req.Target,
		CheckType:      req.CheckType,
		CheckConfig:    checkConfig,
		CronExpression: req.CronExpression,
		ChannelIDs:     channelIDs,
		UserID:         userID,
		Enabled:        enabled,
		CooldownS:      cooldownS,
		LastStatus:     "ok",
		NextRunAt:      &nextRun,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := g.alertStore.Create(c.Context(), rule); err != nil {
		g.logger.Error("alert rule creation failed", slog.String("error", err.Error()))
		return c.AbortInternalServerError("failed to create alert rule")
	}

	g.logger.Info("alert rule created",
		slog.String("rule_id", rule.ID.String()),
		slog.String("user_id", userID),
	)

	return c.JSON(http.StatusCreated, toAlertRuleResponse(rule))
}

func (g *Gateway) handleAlertList(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	rules, err := g.alertStore.List(c.Context(), g.alertOrgID)
	if err != nil {
		return c.AbortInternalServerError("failed to list alert rules")
	}

	resp := make([]AlertRuleResponse, len(rules))
	for i := range rules {
		resp[i] = toAlertRuleResponse(&rules[i])
	}
	return c.OK(resp)
}

func (g *Gateway) handleAlertGet(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid alert rule ID")
	}

	rule, err := g.alertStore.Get(c.Context(), g.alertOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alert rule not found"})
	}

	return c.OK(toAlertRuleResponse(rule))
}

func (g *Gateway) handleAlertUpdate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid alert rule ID")
	}

	rule, err := g.alertStore.Get(c.Context(), g.alertOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alert rule not found"})
	}

	var req AlertRuleRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}

	if req.Name != "" {
		rule.Name = req.Name
	}
	if req.Description != "" {
		rule.Description = req.Description
	}
	if req.Target != "" {
		rule.Target = req.Target
	}
	if req.CheckType != "" {
		rule.CheckType = req.CheckType
	}
	if req.CheckConfig != nil {
		rule.CheckConfig = req.CheckConfig
	}
	if req.CronExpression != "" {
		nextRun, cronErr := scheduler.ComputeNextRunFrom(req.CronExpression, time.Now().UTC())
		if cronErr != nil {
			return c.AbortBadRequest(fmt.Sprintf("invalid cron_expression: %v", cronErr))
		}
		rule.CronExpression = req.CronExpression
		rule.NextRunAt = &nextRun
	}
	if len(req.Channels) > 0 {
		var channelIDs []uuid.UUID
		for _, name := range req.Channels {
			ch, chErr := g.channelStore.GetByName(c.Context(), g.alertOrgID, name)
			if chErr != nil {
				return c.AbortBadRequest(fmt.Sprintf("notification channel %q not found", name))
			}
			channelIDs = append(channelIDs, ch.ID)
		}
		rule.ChannelIDs = channelIDs
	}
	if req.CooldownS > 0 {
		rule.CooldownS = req.CooldownS
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}

	rule.UpdatedAt = time.Now().UTC()

	if err := g.alertStore.Update(c.Context(), rule); err != nil {
		return c.AbortInternalServerError("failed to update alert rule")
	}

	g.logger.Info("alert rule updated",
		slog.String("rule_id", rule.ID.String()),
		slog.String("updated_by", userID),
	)

	return c.OK(toAlertRuleResponse(rule))
}

func (g *Gateway) handleAlertDelete(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid alert rule ID")
	}

	if err := g.alertStore.Delete(c.Context(), g.alertOrgID, id); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alert rule not found"})
	}

	g.logger.Info("alert rule deleted",
		slog.String("rule_id", id.String()),
		slog.String("deleted_by", userID),
	)

	return c.OK(map[string]string{"status": "deleted"})
}

func (g *Gateway) handleAlertHistory(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid alert rule ID")
	}

	history, err := g.historyStore.ListByRule(c.Context(), g.alertOrgID, id, 50)
	if err != nil {
		return c.AbortInternalServerError("failed to list alert history")
	}

	resp := make([]AlertHistoryResponse, len(history))
	for i := range history {
		resp[i] = toAlertHistoryResponse(&history[i])
	}
	return c.OK(resp)
}

// **** Notification Channel Handlers ****

func (g *Gateway) handleChannelCreate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	var req NotificationChannelRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}
	if req.Name == "" {
		return c.AbortBadRequest("name is required")
	}
	if req.ChannelType == "" {
		return c.AbortBadRequest("channel_type is required")
	}
	switch req.ChannelType {
	case "telegram", "slack", "email", "webhook":
		// OK.
	default:
		return c.AbortBadRequest("channel_type must be telegram, slack, email, or webhook")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	now := time.Now().UTC()
	ch := &domain.NotificationChannel{
		ID:            uuid.New(),
		OrgID:         g.alertOrgID,
		Name:          req.Name,
		ChannelType:   req.ChannelType,
		Config:        req.Config,
		CredentialRef: req.CredentialRef,
		Enabled:       enabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := g.channelStore.Create(c.Context(), ch); err != nil {
		g.logger.Error("notification channel creation failed", slog.String("error", err.Error()))
		return c.AbortInternalServerError("failed to create notification channel")
	}

	g.logger.Info("notification channel created",
		slog.String("channel_id", ch.ID.String()),
		slog.String("created_by", userID),
	)

	return c.JSON(http.StatusCreated, toNotificationChannelResponse(ch))
}

func (g *Gateway) handleChannelList(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	channels, err := g.channelStore.List(c.Context(), g.alertOrgID)
	if err != nil {
		return c.AbortInternalServerError("failed to list notification channels")
	}

	resp := make([]NotificationChannelResponse, len(channels))
	for i := range channels {
		resp[i] = toNotificationChannelResponse(&channels[i])
	}
	return c.OK(resp)
}

func (g *Gateway) handleChannelGet(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}
	_ = userID

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid channel ID")
	}

	ch, err := g.channelStore.Get(c.Context(), g.alertOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "notification channel not found"})
	}

	return c.OK(toNotificationChannelResponse(ch))
}

func (g *Gateway) handleChannelUpdate(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid channel ID")
	}

	ch, err := g.channelStore.Get(c.Context(), g.alertOrgID, id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "notification channel not found"})
	}

	var req NotificationChannelRequest
	if err := c.Bind(&req); err != nil {
		return c.AbortBadRequest("invalid request body")
	}

	if req.Name != "" {
		ch.Name = req.Name
	}
	if req.ChannelType != "" {
		ch.ChannelType = req.ChannelType
	}
	if req.Config != nil {
		ch.Config = req.Config
	}
	if req.CredentialRef != "" {
		ch.CredentialRef = req.CredentialRef
	}
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}
	ch.UpdatedAt = time.Now().UTC()

	if err := g.channelStore.Update(c.Context(), ch); err != nil {
		return c.AbortInternalServerError("failed to update notification channel")
	}

	g.logger.Info("notification channel updated",
		slog.String("channel_id", ch.ID.String()),
		slog.String("updated_by", userID),
	)

	return c.OK(toNotificationChannelResponse(ch))
}

func (g *Gateway) handleChannelDelete(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid channel ID")
	}

	if err := g.channelStore.Delete(c.Context(), g.alertOrgID, id); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "notification channel not found"})
	}

	g.logger.Info("notification channel deleted",
		slog.String("channel_id", id.String()),
		slog.String("deleted_by", userID),
	)

	return c.OK(map[string]string{"status": "deleted"})
}

func (g *Gateway) handleChannelTest(c *okapi.Context) error {
	userID := c.GetString("userID")
	if userID == "" {
		return c.AbortUnauthorized("Unauthorized")
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.AbortBadRequest("invalid channel ID")
	}

	msg := &notification.Message{
		Subject: "[Akili] Test Notification",
		Body:    "This is a test notification from Akili. If you receive this, your notification channel is configured correctly.",
		Metadata: map[string]string{
			"type": "test",
		},
	}

	results := g.dispatcher.Notify(c.Context(), g.alertOrgID, []uuid.UUID{id}, msg, userID)
	if err := results[id]; err != nil {
		return c.OK(map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
	}

	return c.OK(map[string]string{"status": "sent"})
}
