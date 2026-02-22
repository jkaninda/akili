package postgres

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/security"
)

// --- Organization ---

func toOrgDomain(m *OrgModel) *domain.Organization {
	return &domain.Organization{
		ID:        m.ID,
		Name:      m.Name,
		Slug:      m.Slug,
		CreatedAt: m.CreatedAt,
	}
}

// --- User ---

func toUserDomain(m *UserModel) *domain.User {
	return &domain.User{
		ID:         m.ID,
		OrgID:      m.OrgID,
		ExternalID: m.ExternalID,
		Email:      m.Email,
		CreatedAt:  m.CreatedAt,
	}
}

// --- Role / Permission ---

func toSecurityRole(m *RoleModel) security.Role {
	var perms []string
	var requireApproval []string
	for _, p := range m.Permissions {
		perms = append(perms, p.ActionName)
		if p.RequiresApproval {
			requireApproval = append(requireApproval, p.ActionName)
		}
	}
	return security.Role{
		Name:            m.Name,
		Permissions:     perms,
		MaxRiskLevel:    m.MaxRiskLevel,
		RequireApproval: requireApproval,
	}
}

// --- Audit ---

func toAuditModel(orgID uuid.UUID, event security.AuditEvent) AuditEventModel {
	params, _ := json.Marshal(event.Parameters)
	if params == nil {
		params = []byte("{}")
	}
	return AuditEventModel{
		ID:            uuid.New(),
		OrgID:         orgID,
		CorrelationID: event.CorrelationID,
		UserID:        event.UserID,
		Action:        event.Action,
		Tool:          event.Tool,
		Parameters:    JSONB(params),
		Result:        event.Result,
		TokensUsed:    event.TokensUsed,
		CostUSD:       event.CostUSD,
		ApprovedBy:    event.ApprovedBy,
		Error:         event.Error,
		CreatedAt:     event.Timestamp,
	}
}

func toAuditDomain(m *AuditEventModel) security.AuditEvent {
	var params map[string]any
	if len(m.Parameters) > 0 {
		_ = json.Unmarshal(m.Parameters, &params)
	}
	return security.AuditEvent{
		Timestamp:     m.CreatedAt,
		CorrelationID: m.CorrelationID,
		UserID:        m.UserID,
		Action:        m.Action,
		Tool:          m.Tool,
		Parameters:    params,
		Result:        m.Result,
		TokensUsed:    m.TokensUsed,
		CostUSD:       m.CostUSD,
		ApprovedBy:    m.ApprovedBy,
		Error:         m.Error,
	}
}

// --- Approval ---

func toApprovalModel(orgID uuid.UUID, req *approval.CreateRequest, id string, ttl time.Duration) ApprovalModel {
	params, _ := json.Marshal(req.Parameters)
	if params == nil {
		params = []byte("{}")
	}
	now := time.Now().UTC()
	return ApprovalModel{
		ID:             id,
		OrgID:          orgID,
		UserID:         req.UserID,
		ToolName:       req.ToolName,
		Parameters:     JSONB(params),
		ActionName:     req.ActionName,
		RiskLevel:      req.RiskLevel,
		EstimatedCost:  req.EstimatedCost,
		CorrelationID:  req.CorrelationID,
		ConversationID: req.ConversationID,
		ToolUseID:      req.ToolUseID,
		Status:         int16(approval.StatusPending),
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}
}

func toApprovalDomain(m *ApprovalModel) *approval.PendingApproval {
	var params map[string]any
	if len(m.Parameters) > 0 {
		_ = json.Unmarshal(m.Parameters, &params)
	}
	pa := &approval.PendingApproval{
		ID:             m.ID,
		UserID:         m.UserID,
		ToolName:       m.ToolName,
		Parameters:     params,
		ActionName:     m.ActionName,
		RiskLevel:      m.RiskLevel,
		EstimatedCost:  m.EstimatedCost,
		CorrelationID:  m.CorrelationID,
		ConversationID: m.ConversationID,
		ToolUseID:      m.ToolUseID,
		Status:         approval.Status(m.Status),
		ApprovedBy:     m.ApprovedBy,
		CreatedAt:      m.CreatedAt,
		ExpiresAt:      m.ExpiresAt,
	}
	if m.ResolvedAt != nil {
		pa.ResolvedAt = *m.ResolvedAt
	}
	return pa
}

// --- InfraNode ---

func toInfraNodeModel(n *domain.InfraNode) InfraNodeModel {
	aliases, _ := json.Marshal(n.Aliases)
	if aliases == nil {
		aliases = []byte("[]")
	}
	tags, _ := json.Marshal(n.Tags)
	if tags == nil {
		tags = []byte("{}")
	}
	return InfraNodeModel{
		ID:            n.ID,
		OrgID:         n.OrgID,
		Name:          n.Name,
		Aliases:       JSONB(aliases),
		NodeType:      n.NodeType,
		Host:          n.Host,
		Port:          n.Port,
		User:          n.User,
		CredentialRef: n.CredentialRef,
		Tags:          JSONB(tags),
		MCPServer:     n.MCPServer,
		Enabled:       n.Enabled,
		CreatedAt:     n.CreatedAt,
		UpdatedAt:     n.UpdatedAt,
	}
}

func toInfraNodeDomain(m *InfraNodeModel) *domain.InfraNode {
	var aliases []string
	_ = json.Unmarshal(m.Aliases, &aliases)
	var tags map[string]string
	_ = json.Unmarshal(m.Tags, &tags)
	return &domain.InfraNode{
		ID:            m.ID,
		OrgID:         m.OrgID,
		Name:          m.Name,
		Aliases:       aliases,
		NodeType:      m.NodeType,
		Host:          m.Host,
		Port:          m.Port,
		User:          m.User,
		CredentialRef: m.CredentialRef,
		Tags:          tags,
		MCPServer:     m.MCPServer,
		Enabled:       m.Enabled,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

// --- CronJob ---

func toCronJobModel(cj *domain.CronJob) CronJobModel {
	return CronJobModel{
		ID:             cj.ID,
		OrgID:          cj.OrgID,
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
		LastWorkflowID: cj.LastWorkflowID,
		LastError:      cj.LastError,
		CreatedAt:      cj.CreatedAt,
		UpdatedAt:      cj.UpdatedAt,
	}
}

func toCronJobDomain(m *CronJobModel) *domain.CronJob {
	return &domain.CronJob{
		ID:             m.ID,
		OrgID:          m.OrgID,
		Name:           m.Name,
		Description:    m.Description,
		CronExpression: m.CronExpression,
		Goal:           m.Goal,
		UserID:         m.UserID,
		BudgetLimitUSD: m.BudgetLimitUSD,
		MaxDepth:       m.MaxDepth,
		MaxTasks:       m.MaxTasks,
		Enabled:        m.Enabled,
		NextRunAt:      m.NextRunAt,
		LastRunAt:      m.LastRunAt,
		LastWorkflowID: m.LastWorkflowID,
		LastError:      m.LastError,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// --- NotificationChannel ---

func toNotificationChannelModel(ch *domain.NotificationChannel) NotificationChannelModel {
	cfg, _ := json.Marshal(ch.Config)
	if cfg == nil {
		cfg = []byte("{}")
	}
	return NotificationChannelModel{
		ID:            ch.ID,
		OrgID:         ch.OrgID,
		Name:          ch.Name,
		ChannelType:   ch.ChannelType,
		Config:        JSONB(cfg),
		CredentialRef: ch.CredentialRef,
		Enabled:       ch.Enabled,
		CreatedAt:     ch.CreatedAt,
		UpdatedAt:     ch.UpdatedAt,
	}
}

func toNotificationChannelDomain(m *NotificationChannelModel) *domain.NotificationChannel {
	var cfg map[string]string
	_ = json.Unmarshal(m.Config, &cfg)
	return &domain.NotificationChannel{
		ID:            m.ID,
		OrgID:         m.OrgID,
		Name:          m.Name,
		ChannelType:   m.ChannelType,
		Config:        cfg,
		CredentialRef: m.CredentialRef,
		Enabled:       m.Enabled,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

// --- AlertRule ---

func toAlertRuleModel(r *domain.AlertRule) AlertRuleModel {
	checkCfg, _ := json.Marshal(r.CheckConfig)
	if checkCfg == nil {
		checkCfg = []byte("{}")
	}
	channelIDs, _ := json.Marshal(r.ChannelIDs)
	if channelIDs == nil {
		channelIDs = []byte("[]")
	}
	return AlertRuleModel{
		ID:             r.ID,
		OrgID:          r.OrgID,
		Name:           r.Name,
		Description:    r.Description,
		Target:         r.Target,
		CheckType:      r.CheckType,
		CheckConfig:    JSONB(checkCfg),
		CronExpression: r.CronExpression,
		ChannelIDs:     JSONB(channelIDs),
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

func toAlertRuleDomain(m *AlertRuleModel) *domain.AlertRule {
	var checkCfg map[string]string
	_ = json.Unmarshal(m.CheckConfig, &checkCfg)
	var channelIDs []uuid.UUID
	_ = json.Unmarshal(m.ChannelIDs, &channelIDs)
	return &domain.AlertRule{
		ID:             m.ID,
		OrgID:          m.OrgID,
		Name:           m.Name,
		Description:    m.Description,
		Target:         m.Target,
		CheckType:      m.CheckType,
		CheckConfig:    checkCfg,
		CronExpression: m.CronExpression,
		ChannelIDs:     channelIDs,
		UserID:         m.UserID,
		Enabled:        m.Enabled,
		CooldownS:      m.CooldownS,
		LastCheckedAt:  m.LastCheckedAt,
		LastAlertedAt:  m.LastAlertedAt,
		LastStatus:     m.LastStatus,
		NextRunAt:      m.NextRunAt,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// --- AlertHistory ---

func toAlertHistoryModel(h *domain.AlertHistory) AlertHistoryModel {
	notifiedVia, _ := json.Marshal(h.NotifiedVia)
	if notifiedVia == nil {
		notifiedVia = []byte("[]")
	}
	notifyErrors, _ := json.Marshal(h.NotifyErrors)
	if notifyErrors == nil {
		notifyErrors = []byte("[]")
	}
	return AlertHistoryModel{
		ID:              h.ID,
		OrgID:           h.OrgID,
		AlertRuleID:     h.AlertRuleID,
		Status:          h.Status,
		PreviousStatus:  h.PreviousStatus,
		Message:         h.Message,
		NotifiedVia:     JSONB(notifiedVia),
		NotifyErrors:    JSONB(notifyErrors),
		CheckDurationMS: h.CheckDurationMS,
		CreatedAt:       h.CreatedAt,
	}
}

func toAlertHistoryDomain(m *AlertHistoryModel) *domain.AlertHistory {
	var notifiedVia []string
	_ = json.Unmarshal(m.NotifiedVia, &notifiedVia)
	var notifyErrors []string
	_ = json.Unmarshal(m.NotifyErrors, &notifyErrors)
	return &domain.AlertHistory{
		ID:              m.ID,
		OrgID:           m.OrgID,
		AlertRuleID:     m.AlertRuleID,
		Status:          m.Status,
		PreviousStatus:  m.PreviousStatus,
		Message:         m.Message,
		NotifiedVia:     notifiedVia,
		NotifyErrors:    notifyErrors,
		CheckDurationMS: m.CheckDurationMS,
		CreatedAt:       m.CreatedAt,
	}
}

// --- Heartbeat Task converters ---

func toHeartbeatTaskModel(t *domain.HeartbeatTask) HeartbeatTaskModel {
	channelIDs, _ := json.Marshal(t.NotificationChannelIDs)
	if channelIDs == nil {
		channelIDs = []byte("[]")
	}
	return HeartbeatTaskModel{
		ID:                     t.ID,
		OrgID:                  t.OrgID,
		Name:                   t.Name,
		Description:            t.Description,
		SourceFile:             t.SourceFile,
		FileGroup:              t.FileGroup,
		CronExpression:         t.CronExpression,
		Mode:                   t.Mode,
		UserID:                 t.UserID,
		BudgetLimitUSD:         t.BudgetLimitUSD,
		NotificationChannelIDs: JSONB(channelIDs),
		Enabled:                t.Enabled,
		NextRunAt:              t.NextRunAt,
		LastRunAt:              t.LastRunAt,
		LastStatus:             t.LastStatus,
		LastError:              t.LastError,
		LastResultSummary:      t.LastResultSummary,
		CreatedAt:              t.CreatedAt,
		UpdatedAt:              t.UpdatedAt,
	}
}

func toHeartbeatTaskDomain(m *HeartbeatTaskModel) *domain.HeartbeatTask {
	var channelIDs []uuid.UUID
	_ = json.Unmarshal(m.NotificationChannelIDs, &channelIDs)
	return &domain.HeartbeatTask{
		ID:                     m.ID,
		OrgID:                  m.OrgID,
		Name:                   m.Name,
		Description:            m.Description,
		SourceFile:             m.SourceFile,
		FileGroup:              m.FileGroup,
		CronExpression:         m.CronExpression,
		Mode:                   m.Mode,
		UserID:                 m.UserID,
		BudgetLimitUSD:         m.BudgetLimitUSD,
		NotificationChannelIDs: channelIDs,
		Enabled:                m.Enabled,
		NextRunAt:              m.NextRunAt,
		LastRunAt:              m.LastRunAt,
		LastStatus:             m.LastStatus,
		LastError:              m.LastError,
		LastResultSummary:      m.LastResultSummary,
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
	}
}

func toHeartbeatTaskResultModel(r *domain.HeartbeatTaskResult) HeartbeatTaskResultModel {
	notifiedVia, _ := json.Marshal(r.NotifiedVia)
	if notifiedVia == nil {
		notifiedVia = []byte("[]")
	}
	notifyErrors, _ := json.Marshal(r.NotifyErrors)
	if notifyErrors == nil {
		notifyErrors = []byte("[]")
	}
	return HeartbeatTaskResultModel{
		ID:            r.ID,
		OrgID:         r.OrgID,
		TaskID:        r.TaskID,
		Status:        r.Status,
		Output:        r.Output,
		OutputSummary: r.OutputSummary,
		TokensUsed:    r.TokensUsed,
		CostUSD:       r.CostUSD,
		DurationMS:    r.DurationMS,
		CorrelationID: r.CorrelationID,
		NotifiedVia:   JSONB(notifiedVia),
		NotifyErrors:  JSONB(notifyErrors),
		CreatedAt:     r.CreatedAt,
	}
}

func toHeartbeatTaskResultDomain(m *HeartbeatTaskResultModel) *domain.HeartbeatTaskResult {
	var notifiedVia []string
	_ = json.Unmarshal(m.NotifiedVia, &notifiedVia)
	var notifyErrors []string
	_ = json.Unmarshal(m.NotifyErrors, &notifyErrors)
	return &domain.HeartbeatTaskResult{
		ID:            m.ID,
		OrgID:         m.OrgID,
		TaskID:        m.TaskID,
		Status:        m.Status,
		Output:        m.Output,
		OutputSummary: m.OutputSummary,
		TokensUsed:    m.TokensUsed,
		CostUSD:       m.CostUSD,
		DurationMS:    m.DurationMS,
		CorrelationID: m.CorrelationID,
		NotifiedVia:   notifiedVia,
		NotifyErrors:  notifyErrors,
		CreatedAt:     m.CreatedAt,
	}
}
