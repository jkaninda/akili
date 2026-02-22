package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/observability"
	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Orchestrator is the default Agent implementation.
// It delegates to an LLM provider and enforces security checks on tool execution.
// Conversation history is managed per-call: loaded from a ConversationStore
// (if configured) or kept ephemeral (empty each call).
// Runbook holds a loaded runbook definition for dynamic injection.
type Runbook struct {
	Name        string
	Category    string
	Description string
}

type Orchestrator struct {
	provider       llm.Provider
	systemPrompt   string
	logger         *slog.Logger
	security       security.SecurityManager     // nil = security disabled
	toolRegistry   *tools.Registry              // nil = no tools available
	approvalMgr    approval.ApprovalManager     // nil = approvals disabled (returns raw ErrApprovalRequired)
	obs            *observability.Observability // nil = observability disabled
	policyEnforcer *security.PolicyEnforcer     // nil = no policy enforcement
	autoApprover   *approval.AutoApprover       // nil = no auto-approval
	maxIterations  int                          // 0 = DefaultMaxIterations
	skillSummary   string                       // pre-rendered skill descriptions for system prompt

	// Dynamic runbook injection.
	runbooks []Runbook // Available runbooks for context-aware injection.

	// Tool result caching.
	toolCache     *ToolCache      // nil = caching disabled.
	toolCacheTTL  time.Duration   // 0 = caching disabled.
	readOnlyTools map[string]bool // Tool names eligible for caching.

	// Conversation summarization.
	summarizeOnTruncate bool

	// ReAct planning.
	planningEnabled bool

	// Conversation memory (nil = ephemeral, no persistence).
	convStore          ConversationStore
	convOrgID          uuid.UUID
	maxHistoryMessages int // 0 = DefaultMaxHistoryMessages
	maxMessageBytes    int // 0 = DefaultMaxMessageBytes
}

// NewOrchestrator creates an agent backed by the given LLM provider.
func NewOrchestrator(provider llm.Provider, systemPrompt string, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{
		provider:     provider,
		systemPrompt: systemPrompt,
		logger:       logger,
	}
}

// WithSecurity attaches a SecurityManager to the orchestrator.
func (o *Orchestrator) WithSecurity(sm security.SecurityManager) *Orchestrator {
	o.security = sm
	return o
}

// WithTools attaches a tool registry to the orchestrator.
func (o *Orchestrator) WithTools(registry *tools.Registry) *Orchestrator {
	o.toolRegistry = registry
	return o
}

// WithApproval attaches an ApprovalManager for pending approval workflows.
func (o *Orchestrator) WithApproval(am approval.ApprovalManager) *Orchestrator {
	o.approvalMgr = am
	return o
}

// WithObservability attaches observability (metrics, tracing, anomaly detection).
func (o *Orchestrator) WithObservability(obs *observability.Observability) *Orchestrator {
	o.obs = obs
	return o
}

// WithMaxIterations sets the maximum number of tool-use loop iterations.
func (o *Orchestrator) WithMaxIterations(n int) *Orchestrator {
	o.maxIterations = n
	return o
}

// WithPolicyEnforcer attaches capability-based policy enforcement.
func (o *Orchestrator) WithPolicyEnforcer(pe *security.PolicyEnforcer) *Orchestrator {
	o.policyEnforcer = pe
	return o
}

// WithSkillSummary injects loaded skill descriptions into the system prompt.
func (o *Orchestrator) WithSkillSummary(summary string) *Orchestrator {
	o.skillSummary = summary
	return o
}

// WithConversationStore attaches persistent conversation memory.
func (o *Orchestrator) WithConversationStore(store ConversationStore, orgID uuid.UUID, maxMessages, maxMsgBytes int) *Orchestrator {
	o.convStore = store
	o.convOrgID = orgID
	o.maxHistoryMessages = maxMessages
	o.maxMessageBytes = maxMsgBytes
	return o
}

// WithAutoApprover enables pattern-based automatic approval.
func (o *Orchestrator) WithAutoApprover(aa *approval.AutoApprover) *Orchestrator {
	o.autoApprover = aa
	return o
}

// WithRunbooks sets the available runbooks for dynamic context injection.
func (o *Orchestrator) WithRunbooks(runbooks []Runbook) *Orchestrator {
	o.runbooks = runbooks
	return o
}

// WithToolCache enables TTL-based caching for read-only tools.
func (o *Orchestrator) WithToolCache(ttl time.Duration, readOnlyTools []string) *Orchestrator {
	o.toolCacheTTL = ttl
	o.toolCache = NewToolCache(ttl)
	o.readOnlyTools = make(map[string]bool, len(readOnlyTools))
	for _, name := range readOnlyTools {
		o.readOnlyTools[name] = true
	}
	return o
}

// WithSummarization enables conversation summarization before truncation.
func (o *Orchestrator) WithSummarization(enabled bool) *Orchestrator {
	o.summarizeOnTruncate = enabled
	return o
}

// WithPlanning enables ReAct planning for complex tasks.
func (o *Orchestrator) WithPlanning(enabled bool) *Orchestrator {
	o.planningEnabled = enabled
	return o
}

// buildSystemPrompt returns the system prompt enriched with skill information,
func (o *Orchestrator) buildSystemPrompt(userMessage string) string {
	prompt := o.systemPrompt
	if o.skillSummary != "" {
		prompt += "\n\n## Available Skills\n" + o.skillSummary
	}

	// Dynamic runbook injection: match user message against runbooks.
	if len(o.runbooks) > 0 && userMessage != "" {
		matched := matchRunbooks(userMessage, o.runbooks, 2)
		for _, rb := range matched {
			prompt += fmt.Sprintf("\n\n## Active Runbook: %s\n%s", rb.Name, rb.Description)
		}
	}

	// Incident report template injection.
	if detectIncidentContext(userMessage) {
		prompt += incidentReportPrompt
	}

	// Planning instruction injection.
	if o.planningEnabled && shouldPlan(userMessage) {
		prompt += planningInstruction
	}

	return prompt
}

const incidentReportPrompt = `

## Incident Report Format
When investigating an incident, conclude with a structured report:
**Severity**: P1/P2/P3/P4
**Status**: Investigating/Identified/Monitoring/Resolved
**Timeline**: Key events with timestamps
**Impact**: What's affected and scope
**Root Cause**: Identified or suspected cause
**Remediation**: Actions taken or recommended
**Action Items**: Follow-up tasks`

// detectIncidentContext checks if the user message suggests an incident scenario.
func detectIncidentContext(message string) bool {
	if message == "" {
		return false
	}
	lower := strings.ToLower(message)
	keywords := []string{
		"incident", "outage", "down", "p1", "p2", "on-call",
		"pagerduty", "opsgenie", "sev1", "sev2", "production issue",
		"service degraded", "alert firing",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// Process sends the user's message to the LLM and runs an agentic loop:
// when the LLM requests tool use, the tools are executed and results fed back
// until the LLM produces a final text response.
func (o *Orchestrator) Process(ctx context.Context, input *Input) (*Response, error) {
	if o.obs != nil && o.obs.Tracer != nil {
		var span trace.Span
		ctx, span = o.obs.Tracer.Tracer().Start(ctx, "agent.process",
			trace.WithAttributes(
				attribute.String("user_id", input.UserID),
				attribute.String("correlation_id", input.CorrelationID),
			))
		defer span.End()
	}

	o.logger.DebugContext(ctx, "processing input",
		slog.String("user_id", input.UserID),
		slog.String("correlation_id", input.CorrelationID),
		slog.String("conversation_id", input.ConversationID),
	)

	// Load or create conversation-scoped history.
	var history []llm.Message
	var convID uuid.UUID
	persistent := input.ConversationID != "" && o.convStore != nil

	if persistent {
		var err error
		convID, err = uuid.Parse(input.ConversationID)
		if err != nil {
			convID = uuid.New()
		}
		convID, err = o.convStore.GetOrCreateConversation(ctx, o.convOrgID, input.UserID, convID)
		if err != nil {
			o.logger.ErrorContext(ctx, "failed to load conversation, falling back to ephemeral",
				slog.String("error", err.Error()),
			)
			persistent = false
		} else {
			maxHist := o.maxHistoryMessages
			if maxHist <= 0 {
				maxHist = DefaultMaxHistoryMessages
			}
			history, err = o.convStore.LoadHistory(ctx, convID, maxHist)
			if err != nil {
				o.logger.ErrorContext(ctx, "failed to load history, falling back to ephemeral",
					slog.String("error", err.Error()),
				)
				persistent = false
				history = nil
			}
		}
	}

	// Track new messages added during this call for batch persistence.
	historyStart := len(history)

	// Append user message.
	userMsg := llm.Message{
		Role:    llm.RoleUser,
		Content: o.truncateContent(input.Message),
	}
	history = append(history, userMsg)

	// Context window management: summarize then truncate from oldest if over limit.
	if o.summarizeOnTruncate {
		maxHist := o.maxHistoryMessages
		if maxHist <= 0 {
			maxHist = DefaultMaxHistoryMessages
		}
		history = summarizeHistory(ctx, o.provider, history, maxHist, o.logger)
	}
	history = o.truncateHistory(history)

	// Build tool definitions from registry
	var toolDefs []llm.ToolDefinition
	if o.toolRegistry != nil {
		toolDefs = tools.ToLLMDefinitions(o.toolRegistry)
	}

	systemPrompt := o.buildSystemPrompt(input.Message)

	maxIter := o.maxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}

	totalTokens := 0
	var allToolResults []ToolCallResult
	var pendingApprovals []ApprovalRequest

	for iter := 0; iter < maxIter; iter++ {
		llmResp, err := o.provider.SendMessage(ctx, &llm.Request{
			SystemPrompt: systemPrompt,
			Messages:     history,
			Tools:        toolDefs,
		})
		if err != nil {
			if span := trace.SpanFromContext(ctx); span.IsRecording() {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			o.persistNewMessages(ctx, persistent, convID, history[historyStart:])
			return nil, fmt.Errorf("llm request failed: %w", err)
		}

		totalTokens += llmResp.Usage.InputTokens + llmResp.Usage.OutputTokens

		// Append the full assistant response (with tool_use blocks) to history.
		history = append(history, llm.Message{
			Role:          llm.RoleAssistant,
			ContentBlocks: llmResp.ContentBlocks,
		})

		// If no tool use requested, we are done.
		if !llmResp.HasToolUse() {
			o.persistNewMessages(ctx, persistent, convID, history[historyStart:])
			return &Response{
				Message:          llmResp.Content,
				TokensUsed:       totalTokens,
				ToolResults:      allToolResults,
				ApprovalRequests: pendingApprovals,
			}, nil
		}

		// Execute each tool_use block and collect results.
		o.logger.InfoContext(ctx, "executing tool calls",
			slog.Int("iteration", iter+1),
			slog.Int("tool_calls", len(llmResp.ToolUseBlocks())),
			slog.String("correlation_id", input.CorrelationID),
		)

		resultBlocks, results, approvals := o.executeToolCalls(ctx, input, llmResp.ToolUseBlocks())
		allToolResults = append(allToolResults, results...)
		pendingApprovals = append(pendingApprovals, approvals...)

		if len(approvals) > 0 {
			history = append(history, llm.Message{
				Role:          llm.RoleUser,
				ContentBlocks: resultBlocks,
			})
			o.persistNewMessages(ctx, persistent, convID, history[historyStart:])
			return &Response{
				Message:          llmResp.Content,
				TokensUsed:       totalTokens,
				ToolResults:      allToolResults,
				ApprovalRequests: pendingApprovals,
			}, nil
		}

		// Append tool results as a user message with tool_result blocks.
		history = append(history, llm.Message{
			Role:          llm.RoleUser,
			ContentBlocks: resultBlocks,
		})
	}

	// Max iterations reached.
	o.logger.WarnContext(ctx, "max tool-use iterations reached",
		slog.Int("max_iterations", maxIter),
		slog.String("correlation_id", input.CorrelationID),
	)

	o.persistNewMessages(ctx, persistent, convID, history[historyStart:])

	return &Response{
		Message:          "Maximum tool use iterations reached. Please refine your request.",
		TokensUsed:       totalTokens,
		ToolResults:      allToolResults,
		ApprovalRequests: pendingApprovals,
	}, nil
}

// persistNewMessages saves new messages to the conversation store (non-fatal on error).
func (o *Orchestrator) persistNewMessages(ctx context.Context, persistent bool, convID uuid.UUID, msgs []llm.Message) {
	if !persistent || len(msgs) == 0 {
		return
	}
	if err := o.convStore.AppendMessages(ctx, convID, o.convOrgID, msgs); err != nil {
		o.logger.ErrorContext(ctx, "failed to persist conversation messages",
			slog.String("conversation_id", convID.String()),
			slog.String("error", err.Error()),
		)
	}
}

// truncateHistory keeps the last maxHistoryMessages messages.
// Ensures the first message has role "user" to avoid LLM protocol violations.
func (o *Orchestrator) truncateHistory(history []llm.Message) []llm.Message {
	max := o.maxHistoryMessages
	if max <= 0 {
		max = DefaultMaxHistoryMessages
	}
	if len(history) <= max {
		return history
	}
	truncated := history[len(history)-max:]
	if len(truncated) > 0 && truncated[0].Role == llm.RoleAssistant {
		truncated = truncated[1:]
	}
	return truncated
}

// truncateContent enforces the per-message size limit.
func (o *Orchestrator) truncateContent(s string) string {
	max := o.maxMessageBytes
	if max <= 0 {
		max = DefaultMaxMessageBytes
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[message truncated]"
}

// executeToolCalls iterates over tool_use blocks, calls ExecuteTool for each,
// and builds tool_result content blocks.
func (o *Orchestrator) executeToolCalls(
	ctx context.Context,
	input *Input,
	toolUseBlocks []llm.ContentBlock,
) ([]llm.ContentBlock, []ToolCallResult, []ApprovalRequest) {
	var resultBlocks []llm.ContentBlock
	var results []ToolCallResult
	var approvals []ApprovalRequest

	for _, block := range toolUseBlocks {
		toolResp, err := o.ExecuteTool(ctx, &ToolRequest{
			UserID:         input.UserID,
			ToolName:       block.Name,
			Parameters:     block.Input,
			CorrelationID:  input.CorrelationID,
			ConversationID: input.ConversationID,
			ToolUseID:      block.ID,
		})

		if err != nil {
			// Check for approval pending.
			var approvalErr *ErrApprovalPending
			if errors.As(err, &approvalErr) {
				approvals = append(approvals, ApprovalRequest{
					ApprovalID: approvalErr.ApprovalID,
					ToolName:   approvalErr.ToolName,
					ActionName: approvalErr.ActionName,
					RiskLevel:  approvalErr.RiskLevel,
					UserID:     approvalErr.UserID,
				})
				resultBlocks = append(resultBlocks, llm.ToolResultBlock(
					block.ID,
					fmt.Sprintf("Tool execution requires approval (id: %s). Waiting for human approval.", approvalErr.ApprovalID),
					true,
				))
				results = append(results, ToolCallResult{ToolName: block.Name, Success: false})
				continue
			}

			// Other errors are reported back to the LLM as error results.
			resultBlocks = append(resultBlocks, llm.ToolResultBlock(
				block.ID,
				fmt.Sprintf("Error: %s", err.Error()),
				true,
			))
			results = append(results, ToolCallResult{ToolName: block.Name, Success: false})
			continue
		}

		output := tools.TruncateOutput(toolResp.Output, tools.MaxOutputBytes)
		resultBlocks = append(resultBlocks, llm.ToolResultBlock(
			block.ID,
			output,
			false,
		))
		results = append(results, ToolCallResult{
			ToolName: block.Name,
			Success:  toolResp.Success,
		})
	}

	return resultBlocks, results, approvals
}

// ExecuteTool runs the full security pipeline and then executes the tool.
//
// Flow: validate → cache check → permission → approval → budget reserve → log intent →
// execute → cache store → log result → record cost.
func (o *Orchestrator) ExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	if o.obs != nil && o.obs.Tracer != nil {
		var span trace.Span
		ctx, span = o.obs.Tracer.Tracer().Start(ctx, "agent.execute_tool",
			trace.WithAttributes(
				attribute.String("tool", req.ToolName),
				attribute.String("user_id", req.UserID),
				attribute.String("correlation_id", req.CorrelationID),
			))
		defer span.End()
	}

	// Look up the tool.
	if o.toolRegistry == nil {
		return nil, fmt.Errorf("no tool registry configured")
	}
	tool := o.toolRegistry.Get(req.ToolName)
	if tool == nil {
		return nil, fmt.Errorf("unknown tool: %s", req.ToolName)
	}

	// Validate parameters (fail fast before spending resources on security checks).
	if err := tool.Validate(req.Parameters); err != nil {
		return nil, fmt.Errorf("tool %s validation: %w", req.ToolName, err)
	}

	// Tool result cache check (read-only tools only).
	if o.toolCache != nil && o.readOnlyTools[req.ToolName] {
		if cached, ok := o.toolCache.Get(req.ToolName, req.Parameters); ok {
			o.logger.DebugContext(ctx, "tool cache hit",
				slog.String("tool", req.ToolName),
				slog.String("correlation_id", req.CorrelationID),
			)
			return cached, nil
		}
	}

	// Policy enforcement (RBAC)
	if o.policyEnforcer != nil {
		if err := o.policyEnforcer.CheckToolAllowed(ctx, req.UserID, req.ToolName); err != nil {
			if o.security != nil {
				_ = o.security.LogAction(ctx, security.AuditEvent{
					Timestamp:     time.Now().UTC(),
					CorrelationID: req.CorrelationID,
					UserID:        req.UserID,
					Action:        tool.RequiredAction().Name,
					Tool:          req.ToolName,
					Result:        "policy_denied",
					Error:         err.Error(),
				})
			}
			return nil, fmt.Errorf("policy denied tool %s: %w", req.ToolName, err)
		}
	}

	// Get the tool's required security action.
	secAction := tool.RequiredAction()
	estimatedCost := tool.EstimateCost(req.Parameters)

	o.logger.InfoContext(ctx, "executing tool",
		slog.String("tool", req.ToolName),
		slog.String("user_id", req.UserID),
		slog.String("correlation_id", req.CorrelationID),
		slog.String("action", secAction.Name),
		slog.String("risk", secAction.RiskLevel.String()),
		slog.Float64("estimated_cost", estimatedCost),
	)

	// Security checks
	if o.security != nil {
		if err := o.runSecurityChecks(ctx, req, secAction, estimatedCost); err != nil {
			return nil, err
		}
		resp, err := o.executeWithAudit(ctx, req, tool, secAction, estimatedCost, "")
		if err == nil {
			o.cacheToolResult(req.ToolName, req.Parameters, resp)
		}
		return resp, err
	}

	// No security
	resp, err := o.executeTool(tools.ContextWithUserID(ctx, req.UserID), tool, req.Parameters)
	if err == nil {
		o.cacheToolResult(req.ToolName, req.Parameters, resp)
	}
	return resp, err
}

// ResumeWithApproval resumes a tool execution that was blocked on approval.
func (o *Orchestrator) ResumeWithApproval(ctx context.Context, approvalID string) (*ToolResponse, error) {
	if o.approvalMgr == nil {
		return nil, fmt.Errorf("no approval manager configured")
	}

	// Get and validate the pending approval.
	pa, err := o.approvalMgr.Get(ctx, approvalID)
	if err != nil {
		return nil, fmt.Errorf("approval lookup: %w", err)
	}
	if pa.Status != approval.StatusApproved {
		return nil, fmt.Errorf("approval %s is not approved (status: %s)", approvalID, pa.Status)
	}

	o.logger.InfoContext(ctx, "resuming with approval",
		slog.String("approval_id", approvalID),
		slog.String("approved_by", pa.ApprovedBy),
		slog.String("tool", pa.ToolName),
		slog.String("user_id", pa.UserID),
	)

	// Record the manual approval for auto-approval learning.
	if o.autoApprover != nil {
		o.autoApprover.RecordManualApproval(pa.UserID, pa.ToolName, pa.Parameters)
	}

	// Look up the tool
	if o.toolRegistry == nil {
		return nil, fmt.Errorf("no tool registry configured")
	}
	tool := o.toolRegistry.Get(pa.ToolName)
	if tool == nil {
		return nil, fmt.Errorf("tool %s no longer available", pa.ToolName)
	}

	// Re-validate parameters
	if err := tool.Validate(pa.Parameters); err != nil {
		return nil, fmt.Errorf("tool %s re-validation: %w", pa.ToolName, err)
	}

	// Reconstruct the request from the pending approval.
	req := &ToolRequest{
		UserID:        pa.UserID,
		ToolName:      pa.ToolName,
		Parameters:    pa.Parameters,
		CorrelationID: pa.CorrelationID,
	}

	secAction := tool.RequiredAction()

	var resp *ToolResponse
	var execErr error
	if o.security != nil {
		resp, execErr = o.executeWithAudit(ctx, req, tool, secAction, pa.EstimatedCost, pa.ApprovedBy)
	} else {
		resp, execErr = o.executeTool(ctx, tool, pa.Parameters)
	}

	if execErr != nil {
		o.updateApprovalToolResult(ctx, pa, fmt.Sprintf("Error: %s", execErr.Error()), true)
		return nil, execErr
	}
	o.updateApprovalToolResult(ctx, pa, resp.Output, false)
	return resp, nil
}

// updateApprovalToolResult replaces the "approval pending" tool_result placeholder
// in conversation history with the actual execution result. Non-fatal on error.
func (o *Orchestrator) updateApprovalToolResult(ctx context.Context, pa *approval.PendingApproval, content string, isError bool) {
	if o.convStore == nil || pa.ConversationID == "" || pa.ToolUseID == "" {
		return
	}
	convID, err := uuid.Parse(pa.ConversationID)
	if err != nil {
		return
	}
	if err := o.convStore.ReplaceToolResult(ctx, convID, pa.ToolUseID, content, isError); err != nil {
		o.logger.ErrorContext(ctx, "failed to update approval result in conversation",
			slog.String("conversation_id", pa.ConversationID),
			slog.String("tool_use_id", pa.ToolUseID),
			slog.String("error", err.Error()),
		)
	}
}

// runSecurityChecks performs permission and approval checks.
func (o *Orchestrator) runSecurityChecks(ctx context.Context, req *ToolRequest, action security.Action, estimatedCost float64) error {
	// Permission check (RBAC).
	if err := o.security.CheckPermission(ctx, req.UserID, action); err != nil {
		_ = o.logAuditEvent(ctx, req, action.Name, "denied", estimatedCost, "", err)
		return err
	}

	// Approval check.
	if err := o.security.RequireApproval(ctx, req.UserID, action); err != nil {
		if errors.Is(err, security.ErrApprovalRequired) {
			// Check auto-approval before creating a pending approval.
			if o.autoApprover != nil {
				if ok, reason := o.autoApprover.ShouldAutoApprove(req.UserID, req.ToolName, req.Parameters); ok {
					_ = o.logAuditEvent(ctx, req, action.Name, "auto_approved", estimatedCost, "", nil)
					o.logger.InfoContext(ctx, "tool auto-approved",
						slog.String("tool", req.ToolName),
						slog.String("user_id", req.UserID),
						slog.String("reason", reason),
					)
					return nil
				}
			}
		}
		if errors.Is(err, security.ErrApprovalRequired) && o.approvalMgr != nil {
			// Create a pending approval
			approvalID, createErr := o.approvalMgr.Create(ctx, &approval.CreateRequest{
				UserID:         req.UserID,
				ToolName:       req.ToolName,
				Parameters:     req.Parameters,
				ActionName:     action.Name,
				RiskLevel:      action.RiskLevel.String(),
				EstimatedCost:  estimatedCost,
				CorrelationID:  req.CorrelationID,
				ConversationID: req.ConversationID,
				ToolUseID:      req.ToolUseID,
			})
			if createErr != nil {
				return fmt.Errorf("creating approval request: %w", createErr)
			}

			_ = o.logAuditEvent(ctx, req, action.Name, "approval_pending", estimatedCost, "", nil)

			return &ErrApprovalPending{
				ApprovalID: approvalID,
				ToolName:   req.ToolName,
				ActionName: action.Name,
				RiskLevel:  action.RiskLevel.String(),
				UserID:     req.UserID,
			}
		}
		_ = o.logAuditEvent(ctx, req, action.Name, "denied", estimatedCost, "", err)
		return err
	}

	return nil
}

// executeWithAudit handles budget reservation, audit logging, tool execution, and cost recording.
func (o *Orchestrator) executeWithAudit(ctx context.Context, req *ToolRequest, tool tools.Tool, action security.Action, estimatedCost float64, approvedBy string) (*ToolResponse, error) {
	// 4c. Budget reservation (atomic — prevents TOCTOU).
	release, err := o.security.ReserveBudget(ctx, req.UserID, estimatedCost)
	if err != nil {
		_ = o.logAuditEvent(ctx, req, action.Name, "denied", estimatedCost, approvedBy, err)
		return nil, err
	}
	defer release()
	// Log intent.
	_ = o.logAuditEvent(ctx, req, action.Name, "intent", estimatedCost, approvedBy, nil)

	// Execute the tool
	toolCtx := tools.ContextWithUserID(ctx, req.UserID)
	result, execErr := tool.Execute(toolCtx, req.Parameters)

	// Log result and record cost.
	if execErr != nil {
		_ = o.logAuditEvent(ctx, req, action.Name, "failure", estimatedCost, approvedBy, execErr)
		o.security.RecordCost(ctx, req.UserID, estimatedCost)
		return nil, fmt.Errorf("tool %s execution failed: %w", req.ToolName, execErr)
	}

	_ = o.logAuditEvent(ctx, req, action.Name, "success", estimatedCost, approvedBy, nil)
	o.security.RecordCost(ctx, req.UserID, estimatedCost)

	return &ToolResponse{
		Output:   result.Output,
		Success:  result.Success,
		CostUSD:  estimatedCost,
		Metadata: result.Metadata,
	}, nil
}

// executeTool runs the tool without any security checks (fallback when security is disabled).
func (o *Orchestrator) executeTool(ctx context.Context, tool tools.Tool, params map[string]any) (*ToolResponse, error) {
	result, err := tool.Execute(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("tool %s execution failed: %w", tool.Name(), err)
	}
	return &ToolResponse{
		Output:   result.Output,
		Success:  result.Success,
		Metadata: result.Metadata,
	}, nil
}

// cacheToolResult stores a tool result in the cache if caching is enabled for this tool.
func (o *Orchestrator) cacheToolResult(toolName string, params map[string]any, resp *ToolResponse) {
	if o.toolCache != nil && o.readOnlyTools[toolName] {
		o.toolCache.Set(toolName, params, resp)
	}
}

// logAuditEvent is a helper that builds and logs an AuditEvent.
func (o *Orchestrator) logAuditEvent(ctx context.Context, req *ToolRequest, actionName, result string, costUSD float64, approvedBy string, err error) error {
	event := security.AuditEvent{
		Timestamp:     time.Now().UTC(),
		CorrelationID: req.CorrelationID,
		UserID:        req.UserID,
		Action:        actionName,
		Tool:          req.ToolName,
		Parameters:    req.Parameters,
		Result:        result,
		CostUSD:       costUSD,
		ApprovedBy:    approvedBy,
	}
	if err != nil {
		event.Error = err.Error()
	}
	return o.security.LogAction(ctx, event)
}
