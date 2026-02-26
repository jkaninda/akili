package postgres

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/orchestrator"
)

// --- Workflow ---

func toWorkflowModel(wf *orchestrator.Workflow) WorkflowModel {
	metadata, _ := json.Marshal(wf.Metadata)
	if metadata == nil {
		metadata = []byte("{}")
	}
	return WorkflowModel{
		ID:             wf.ID,
		OrgID:          wf.OrgID,
		UserID:         wf.UserID,
		CorrelationID:  wf.CorrelationID,
		Goal:           wf.Goal,
		Status:         string(wf.Status),
		RootTaskID:     wf.RootTaskID,
		BudgetLimitUSD: wf.BudgetLimitUSD,
		BudgetSpentUSD: wf.BudgetSpentUSD,
		MaxDepth:       wf.MaxDepth,
		MaxTasks:       wf.MaxTasks,
		TaskCount:      wf.TaskCount,
		Error:          wf.Error,
		Metadata:       JSONB(metadata),
		CreatedAt:      wf.CreatedAt,
		UpdatedAt:      wf.UpdatedAt,
		CompletedAt:    wf.CompletedAt,
	}
}

func toWorkflowDomain(m *WorkflowModel) *orchestrator.Workflow {
	var metadata map[string]any
	if len(m.Metadata) > 0 {
		_ = json.Unmarshal(m.Metadata, &metadata)
	}
	return &orchestrator.Workflow{
		ID:             m.ID,
		OrgID:          m.OrgID,
		UserID:         m.UserID,
		CorrelationID:  m.CorrelationID,
		Goal:           m.Goal,
		Status:         orchestrator.WorkflowStatus(m.Status),
		RootTaskID:     m.RootTaskID,
		BudgetLimitUSD: m.BudgetLimitUSD,
		BudgetSpentUSD: m.BudgetSpentUSD,
		MaxDepth:       m.MaxDepth,
		MaxTasks:       m.MaxTasks,
		TaskCount:      m.TaskCount,
		Error:          m.Error,
		Metadata:       metadata,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		CompletedAt:    m.CompletedAt,
	}
}

// --- Task ---

func toTaskModel(t *orchestrator.Task) TaskModel {
	depsJSON, _ := json.Marshal(t.DependsOn)
	if depsJSON == nil {
		depsJSON = []byte("[]")
	}
	metadata, _ := json.Marshal(t.Metadata)
	if metadata == nil {
		metadata = []byte("{}")
	}
	return TaskModel{
		ID:           t.ID,
		WorkflowID:   t.WorkflowID,
		OrgID:        t.OrgID,
		ParentTaskID: t.ParentTaskID,
		AgentRole:    string(t.AgentRole),
		Description:  t.Description,
		Input:        t.Input,
		Output:       t.Output,
		Status:       string(t.Status),
		Mode:         string(t.Mode),
		Depth:        t.Depth,
		Priority:     t.Priority,
		DependsOn:    JSONB(depsJSON),
		Error:        t.Error,
		CostUSD:      t.CostUSD,
		TokensUsed:   t.TokensUsed,
		ClaimedBy:      t.ClaimedBy,
		ClaimedAt:      t.ClaimedAt,
		RetryCount:     t.RetryCount,
		MaxRetries:     t.MaxRetries,
		OriginalTaskID: t.OriginalTaskID,
		Metadata:       JSONB(metadata),
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
		StartedAt:      t.StartedAt,
		CompletedAt:    t.CompletedAt,
	}
}

func toTaskDomain(m *TaskModel) *orchestrator.Task {
	var deps []uuid.UUID
	if len(m.DependsOn) > 0 {
		_ = json.Unmarshal(m.DependsOn, &deps)
	}
	var metadata map[string]any
	if len(m.Metadata) > 0 {
		_ = json.Unmarshal(m.Metadata, &metadata)
	}
	return &orchestrator.Task{
		ID:           m.ID,
		WorkflowID:   m.WorkflowID,
		OrgID:        m.OrgID,
		ParentTaskID: m.ParentTaskID,
		AgentRole:    orchestrator.AgentRole(m.AgentRole),
		Description:  m.Description,
		Input:        m.Input,
		Output:       m.Output,
		Status:       orchestrator.TaskStatus(m.Status),
		Mode:         orchestrator.TaskMode(m.Mode),
		Depth:        m.Depth,
		Priority:     m.Priority,
		DependsOn:    deps,
		Error:        m.Error,
		CostUSD:      m.CostUSD,
		TokensUsed:   m.TokensUsed,
		ClaimedBy:      m.ClaimedBy,
		ClaimedAt:      m.ClaimedAt,
		RetryCount:     m.RetryCount,
		MaxRetries:     m.MaxRetries,
		OriginalTaskID: m.OriginalTaskID,
		Metadata:       metadata,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		StartedAt:      m.StartedAt,
		CompletedAt:    m.CompletedAt,
	}
}

// --- AgentMessage ---

func toAgentMessageModel(msg *orchestrator.AgentMessage) AgentMessageModel {
	artifacts, _ := json.Marshal(msg.Artifacts)
	if artifacts == nil {
		artifacts = []byte("[]")
	}
	return AgentMessageModel{
		ID:            msg.ID,
		WorkflowID:    msg.WorkflowID,
		OrgID:         msg.WorkflowID, // OrgID resolved from workflow context.
		FromTaskID:    msg.FromTaskID,
		ToTaskID:      msg.ToTaskID,
		FromRole:      string(msg.FromRole),
		ToRole:        string(msg.ToRole),
		MessageType:   string(msg.MessageType),
		Content:       msg.Content,
		Artifacts:     JSONB(artifacts),
		CorrelationID: msg.CorrelationID,
		CreatedAt:     msg.CreatedAt,
	}
}

func toAgentMessageDomain(m *AgentMessageModel) *orchestrator.AgentMessage {
	var artifacts []orchestrator.Artifact
	if len(m.Artifacts) > 0 {
		_ = json.Unmarshal(m.Artifacts, &artifacts)
	}
	return &orchestrator.AgentMessage{
		ID:            m.ID,
		WorkflowID:    m.WorkflowID,
		FromTaskID:    m.FromTaskID,
		ToTaskID:      m.ToTaskID,
		FromRole:      orchestrator.AgentRole(m.FromRole),
		ToRole:        orchestrator.AgentRole(m.ToRole),
		MessageType:   orchestrator.MessageType(m.MessageType),
		Content:       m.Content,
		Artifacts:     artifacts,
		CorrelationID: m.CorrelationID,
		CreatedAt:     m.CreatedAt,
	}
}
