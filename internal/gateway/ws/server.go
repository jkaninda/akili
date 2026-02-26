// Package ws implements the WebSocket server for Gateway <-> Agent communication.
// Agents connect via WebSocket, register their capabilities, and receive task
// assignments in real-time instead of polling the database.
package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/protocol"
)

// disconnectReason categorizes why an agent disconnected.
type disconnectReason string

const (
	reasonNormal  disconnectReason = "normal"
	reasonError   disconnectReason = "error"
	reasonTimeout disconnectReason = "timeout"
)

// Server is the WebSocket server that manages agent connections.
type Server struct {
	registry *agent.Registry
	tracker  *TaskTracker
	cfg      *config.WebSocketGatewayConfig
	logger   *slog.Logger

	// Pending tasks waiting for an available agent.
	pendingMu sync.Mutex
	pending   []pendingTask

	// Synchronous task result routing for ExecuteSync.
	sync *syncWaiter

	// Approval routing: approval_id -> agent_id.
	approvalMu sync.RWMutex
	approvals  map[string]string
}

// pendingTask is a task waiting for an available agent.
type pendingTask struct {
	assignment protocol.TaskAssignment
	result     chan error
}

// NewServer creates a WebSocket server with the given agent registry.
func NewServer(registry *agent.Registry, cfg *config.WebSocketGatewayConfig, logger *slog.Logger) *Server {
	return &Server{
		registry:  registry,
		tracker:   NewTaskTracker(cfg.WSAckTimeout(), logger),
		cfg:       cfg,
		logger:    logger,
		sync:      newSyncWaiter(),
		approvals: make(map[string]string),
	}
}

// Registry returns the agent registry managed by this server.
func (s *Server) Registry() *agent.Registry {
	return s.registry
}

// Handler returns an http.Handler that upgrades connections to WebSocket.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handleUpgrade)
}

func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	remoteAddr := r.RemoteAddr
	s.logger.Info("agent connection attempt",
		slog.String("remote_addr", remoteAddr),
		slog.String("path", r.URL.Path),
	)

	// Authenticate agent via token.
	if s.cfg != nil && s.cfg.AgentToken != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.Header.Get("Authorization")
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}
		if token != s.cfg.AgentToken {
			s.logger.Warn("agent connection rejected: invalid token",
				slog.String("remote_addr", remoteAddr),
			)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"akili-agent-v1"},
	})
	if err != nil {
		s.logger.Error("websocket accept failed",
			slog.String("remote_addr", remoteAddr),
			slog.String("error", err.Error()),
		)
		return
	}

	s.handleConnection(r.Context(), conn, remoteAddr)
}

func (s *Server) handleConnection(ctx context.Context, conn *websocket.Conn, remoteAddr string) {
	var agentID string
	connectedAt := time.Now()

	defer func() {
		if agentID != "" {
			// Re-queue all in-progress tasks for the disconnected agent.
			orphaned := s.tracker.TasksForAgent(agentID)
			if len(orphaned) > 0 {
				s.logger.Warn("re-queuing tasks from disconnected agent",
					slog.String("agent_id", agentID),
					slog.Int("task_count", len(orphaned)),
				)
				for _, task := range orphaned {
					s.tracker.MarkFailed(task.TaskID, "agent disconnected")
					// Notify any synchronous waiter that the task failed.
					s.sync.resolve(task.TaskID, &SyncTaskResult{
						Success: false,
						Error:   "agent disconnected",
					})
					s.requeueTask(task.Assignment)
				}
			}
			s.registry.Deregister(agentID, conn)
		}
		conn.Close(websocket.StatusNormalClosure, "connection closed")
	}()

	// Wait for agent.register as the first message.
	agentID, err := s.waitForRegistration(ctx, conn, remoteAddr)
	if err != nil {
		s.logger.Error("agent registration failed",
			slog.String("remote_addr", remoteAddr),
			slog.String("error", err.Error()),
		)
		return
	}

	// Start heartbeat pinger.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go s.heartbeatLoop(hbCtx, conn, agentID)

	// Try to assign any pending tasks to this newly-registered agent.
	s.drainPendingTasks()

	// Main message loop with read deadline.
	readTimeout := 3 * s.cfg.WSHeartbeatInterval()

	for {
		readCtx, readCancel := context.WithTimeout(ctx, readTimeout)
		_, data, err := conn.Read(readCtx)
		readCancel()

		if err != nil {
			reason := reasonError
			logLevel := slog.LevelWarn

			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				reason = reasonNormal
				logLevel = slog.LevelInfo
			} else if readCtx.Err() != nil && ctx.Err() == nil {
				reason = reasonTimeout
			}

			sessionDuration := time.Since(connectedAt)
			// Count remaining agents (after deregister in defer).
			remainingAgents := s.registry.Count()
			s.logger.Log(ctx, logLevel, "agent disconnected",
				slog.String("agent_id", agentID),
				slog.String("remote_addr", remoteAddr),
				slog.String("reason", string(reason)),
				slog.String("session_duration", sessionDuration.String()),
				slog.String("error", err.Error()),
				slog.Int("remaining_agents", remainingAgents),
			)
			return
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.logger.Warn("invalid message from agent",
				slog.String("agent_id", agentID),
				slog.String("error", err.Error()),
			)
			continue
		}
		env.AgentID = agentID

		s.handleMessage(ctx, conn, agentID, &env)
	}
}

func (s *Server) waitForRegistration(ctx context.Context, conn *websocket.Conn, remoteAddr string) (string, error) {
	// Set a deadline for registration.
	regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, data, err := conn.Read(regCtx)
	if err != nil {
		return "", fmt.Errorf("reading registration: %w", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", fmt.Errorf("parsing registration: %w", err)
	}

	if env.Type != protocol.MsgAgentRegister {
		return "", fmt.Errorf("expected agent.register, got %s", env.Type)
	}

	var caps protocol.AgentCapabilities
	if err := env.Decode(&caps); err != nil {
		return "", fmt.Errorf("parsing capabilities: %w", err)
	}

	if caps.AgentID == "" {
		return "", fmt.Errorf("agent_id is required")
	}

	// Register the agent, handling duplicate connections.
	oldConn := s.registry.RegisterOrReplace(caps, conn)
	if oldConn != nil {
		oldConn.Close(websocket.StatusPolicyViolation, "duplicate connection")
		s.logger.Warn("closed duplicate agent connection",
			slog.String("agent_id", caps.AgentID),
		)
	}

	// Log rich connection event with total count for visibility.
	totalAgents := s.registry.Count()
	s.logger.Info("agent connected",
		slog.String("agent_id", caps.AgentID),
		slog.String("remote_addr", remoteAddr),
		slog.Any("skills", caps.Skills),
		slog.String("model", caps.Model),
		slog.String("version", caps.Version),
		slog.Int("max_parallel", caps.MaxParallel),
		slog.Int("total_connected_agents", totalAgents),
	)

	// Send confirmation.
	resp, _ := protocol.NewEnvelope(protocol.MsgRegistered, protocol.RegisteredPayload{
		Message: fmt.Sprintf("registered as %s", caps.AgentID),
	})
	resp.AgentID = caps.AgentID
	s.writeEnvelope(ctx, conn, resp)

	return caps.AgentID, nil
}

func (s *Server) handleMessage(_ context.Context, conn *websocket.Conn, agentID string, env *protocol.Envelope) {
	switch env.Type {
	case protocol.MsgAgentHeartbeat:
		var hb protocol.HeartbeatPayload
		if err := env.Decode(&hb); err == nil {
			s.registry.UpdateHeartbeat(agentID, hb.ActiveTasks)
		}

	case protocol.MsgTaskAccepted:
		if !s.tracker.MarkAccepted(env.TaskID) {
			s.logger.Warn("task acceptance for unknown/unexpected task",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
			)
		}

	case protocol.MsgTaskProgress:
		var progress protocol.TaskProgress
		if err := env.Decode(&progress); err == nil {
			s.tracker.MarkProgress(env.TaskID)
			s.logger.Debug("task progress",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("message", progress.Message),
			)
		}

	case protocol.MsgTaskResult:
		s.registry.DecrementTasks(agentID)
		s.tracker.MarkCompleted(env.TaskID)
		var result protocol.TaskResultPayload
		if err := env.Decode(&result); err == nil {
			s.logger.Info("task completed",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("duration", result.Duration),
				slog.Int("tokens", result.TokensUsed),
			)
			// Resolve any synchronous waiter for this task.
			s.sync.resolve(env.TaskID, &SyncTaskResult{
				Success:    true,
				Output:     result.Output,
				TokensUsed: result.TokensUsed,
			})
		}
		// Try to assign pending tasks now that capacity freed up.
		s.drainPendingTasks()

	case protocol.MsgTaskFailed:
		s.registry.DecrementTasks(agentID)
		var fail protocol.TaskFailedPayload
		if err := env.Decode(&fail); err == nil {
			s.tracker.MarkFailed(env.TaskID, fail.Error)
			s.logger.Warn("task failed",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("error", fail.Error),
			)
			// Resolve any synchronous waiter for this task.
			s.sync.resolve(env.TaskID, &SyncTaskResult{
				Success:    false,
				Error:      fail.Error,
				TokensUsed: fail.TokensUsed,
			})
		}
		s.drainPendingTasks()

	case protocol.MsgApprovalRequest:
		var req protocol.ApprovalRequestPayload
		if err := env.Decode(&req); err == nil {
			s.approvalMu.Lock()
			s.approvals[req.ApprovalID] = agentID
			s.approvalMu.Unlock()
			s.logger.Info("approval request from agent",
				slog.String("agent_id", agentID),
				slog.String("approval_id", req.ApprovalID),
				slog.String("tool", req.ToolName),
			)
		}

	case protocol.MsgPong:
		// Agent responded to our ping — refresh last-seen time.
		s.registry.UpdateHeartbeat(agentID, -1) // -1 = don't change active task count.

	default:
		s.logger.Warn("unknown message type from agent",
			slog.String("agent_id", agentID),
			slog.String("type", string(env.Type)),
		)
	}
}

// AssignTask routes a task to an available agent. If no agent is available,
// the task is queued and will be assigned when an agent becomes available.
func (s *Server) AssignTask(ctx context.Context, assignment protocol.TaskAssignment) error {
	// Try to find an agent immediately.
	a, err := s.registry.Route(nil) // TODO: extract skills from task
	if err == nil {
		return s.sendTaskToAgent(ctx, a, assignment)
	}

	// Queue the task.
	s.pendingMu.Lock()
	result := make(chan error, 1)
	s.pending = append(s.pending, pendingTask{
		assignment: assignment,
		result:     result,
	})
	s.pendingMu.Unlock()

	s.logger.Debug("task queued (no available agents)",
		slog.String("task_id", assignment.TaskID),
	)

	// Wait for assignment or context cancellation.
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RouteApprovalResult sends an approval decision to the agent that requested it.
func (s *Server) RouteApprovalResult(ctx context.Context, approvalID, decision, approverID string) error {
	s.approvalMu.RLock()
	agentID, ok := s.approvals[approvalID]
	s.approvalMu.RUnlock()

	if !ok {
		return fmt.Errorf("no agent found for approval %s", approvalID)
	}

	a, ok := s.registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %s not connected", agentID)
	}

	env, _ := protocol.NewEnvelope(protocol.MsgApprovalResult, protocol.ApprovalResultPayload{
		ApprovalID: approvalID,
		Decision:   decision,
		ApproverID: approverID,
	})
	env.AgentID = agentID

	return s.writeEnvelope(ctx, a.Conn, env)
}

func (s *Server) sendTaskToAgent(ctx context.Context, a *agent.ConnectedAgent, assignment protocol.TaskAssignment) error {
	env, _ := protocol.NewEnvelope(protocol.MsgTaskAssign, assignment)
	env.AgentID = a.Info.AgentID
	env.TaskID = assignment.TaskID

	if err := s.writeEnvelope(ctx, a.Conn, env); err != nil {
		return fmt.Errorf("sending task to agent %s: %w", a.Info.AgentID, err)
	}

	s.registry.IncrementTasks(a.Info.AgentID)
	s.tracker.Track(assignment, a.Info.AgentID)

	s.logger.Info("task dispatched to agent",
		slog.String("agent_id", a.Info.AgentID),
		slog.String("task_id", assignment.TaskID),
	)
	return nil
}

// drainPendingTasks tries to assign queued tasks to available agents.
func (s *Server) drainPendingTasks() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	remaining := s.pending[:0]
	for _, pt := range s.pending {
		a, err := s.registry.Route(nil)
		if err != nil {
			remaining = append(remaining, pt)
			continue
		}
		sendErr := s.sendTaskToAgent(context.Background(), a, pt.assignment)
		pt.result <- sendErr
	}
	s.pending = remaining
}

func (s *Server) heartbeatLoop(ctx context.Context, conn *websocket.Conn, agentID string) {
	interval := s.cfg.WSHeartbeatInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			env, _ := protocol.NewEnvelope(protocol.MsgPing, nil)
			if err := s.writeEnvelope(ctx, conn, env); err != nil {
				s.logger.Debug("heartbeat ping failed",
					slog.String("agent_id", agentID),
					slog.String("error", err.Error()),
				)
				return
			}
		}
	}
}

// RunStaleChecker periodically removes agents that haven't sent a heartbeat
// within the stale timeout. Call this as a goroutine from the gateway startup.
func (s *Server) RunStaleChecker(ctx context.Context) {
	staleTimeout := s.cfg.WSStaleTimeout()
	ticker := time.NewTicker(staleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stale := s.registry.RemoveStale(staleTimeout)
			for _, sa := range stale {
				sa.Conn.Close(websocket.StatusPolicyViolation, "heartbeat timeout")
				s.logger.Warn("agent evicted (stale)",
					slog.String("agent_id", sa.AgentID),
					slog.Time("last_seen", sa.LastSeen),
					slog.String("session_duration", time.Since(sa.ConnectedAt).String()),
				)
			}
		}
	}
}

// Shutdown gracefully closes all agent connections.
func (s *Server) Shutdown(_ context.Context) {
	agents := s.registry.List()
	for _, a := range agents {
		a.Conn.Close(websocket.StatusGoingAway, "server shutting down")
	}
	s.logger.Info("websocket server shutdown, closed agent connections",
		slog.Int("count", len(agents)),
	)
}

// Tracker returns the task tracker for external inspection.
func (s *Server) Tracker() *TaskTracker {
	return s.tracker
}

// ConnectedAgentCount returns the number of currently connected remote agents.
func (s *Server) ConnectedAgentCount() int {
	return s.registry.Count()
}

// requeueTask adds a task assignment back to the pending queue for reassignment.
func (s *Server) requeueTask(assignment protocol.TaskAssignment) {
	s.pendingMu.Lock()
	result := make(chan error, 1)
	s.pending = append(s.pending, pendingTask{
		assignment: assignment,
		result:     result,
	})
	s.pendingMu.Unlock()

	// Try to drain immediately (non-blocking — result is buffered).
	s.drainPendingTasks()

	// Don't block on the result channel; the re-queued task will be
	// picked up by drainPendingTasks when an agent becomes available.
	go func() { <-result }()
}

// RunAckChecker periodically checks for tasks that were dispatched but never
// acknowledged by an agent. Unacknowledged tasks are re-queued for reassignment.
// Call this as a goroutine from the gateway startup.
func (s *Server) RunAckChecker(ctx context.Context) {
	// Check every 10 seconds.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Also periodically clean up completed tasks older than 5 minutes.
	cleanTicker := time.NewTicker(5 * time.Minute)
	defer cleanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			stale := s.tracker.UnacknowledgedTasks()
			for _, task := range stale {
				s.tracker.MarkTimedOut(task.TaskID)
				s.registry.DecrementTasks(task.AgentID)
				s.requeueTask(task.Assignment)

				s.logger.Warn("task re-queued (ack timeout)",
					slog.String("task_id", task.TaskID),
					slog.String("agent_id", task.AgentID),
					slog.String("waited", time.Since(task.DispatchedAt).String()),
				)
			}

		case <-cleanTicker.C:
			cleaned := s.tracker.CleanCompleted(5 * time.Minute)
			if cleaned > 0 {
				s.logger.Debug("cleaned completed tasks from tracker",
					slog.Int("count", cleaned),
				)
			}
		}
	}
}

func (s *Server) writeEnvelope(ctx context.Context, conn *websocket.Conn, env *protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
