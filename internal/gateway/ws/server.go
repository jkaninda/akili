// Package ws implements the WebSocket server for Gateway ↔ Agent communication.
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

// Server is the WebSocket server that manages agent connections.
type Server struct {
	registry *agent.Registry
	cfg      *config.WebSocketGatewayConfig
	logger   *slog.Logger

	// Pending tasks waiting for an available agent.
	pendingMu sync.Mutex
	pending   []pendingTask

	// Approval routing: approval_id → agent_id.
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
		cfg:       cfg,
		logger:    logger,
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
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"akili-agent-v1"},
	})
	if err != nil {
		s.logger.Error("websocket accept failed", slog.String("error", err.Error()))
		return
	}

	s.handleConnection(r.Context(), conn)
}

func (s *Server) handleConnection(ctx context.Context, conn *websocket.Conn) {
	var agentID string
	defer func() {
		if agentID != "" {
			s.registry.Deregister(agentID)
		}
		conn.Close(websocket.StatusNormalClosure, "connection closed")
	}()

	// Wait for agent.register as the first message.
	agentID, err := s.waitForRegistration(ctx, conn)
	if err != nil {
		s.logger.Error("agent registration failed", slog.String("error", err.Error()))
		return
	}

	// Start heartbeat pinger.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go s.heartbeatLoop(hbCtx, conn, agentID)

	// Try to assign any pending tasks to this newly-registered agent.
	s.drainPendingTasks()

	// Main message loop.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				s.logger.Info("agent disconnected normally", slog.String("agent_id", agentID))
			} else {
				s.logger.Warn("agent connection error",
					slog.String("agent_id", agentID),
					slog.String("error", err.Error()),
				)
			}
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

func (s *Server) waitForRegistration(ctx context.Context, conn *websocket.Conn) (string, error) {
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

	// Register the agent.
	s.registry.Register(caps, conn)

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
		s.logger.Debug("task accepted by agent",
			slog.String("agent_id", agentID),
			slog.String("task_id", env.TaskID),
		)

	case protocol.MsgTaskProgress:
		var progress protocol.TaskProgress
		if err := env.Decode(&progress); err == nil {
			s.logger.Debug("task progress",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("message", progress.Message),
			)
		}

	case protocol.MsgTaskResult:
		s.registry.DecrementTasks(agentID)
		var result protocol.TaskResultPayload
		if err := env.Decode(&result); err == nil {
			s.logger.Info("task completed",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("duration", result.Duration),
				slog.Int("tokens", result.TokensUsed),
			)
		}
		// Try to assign pending tasks now that capacity freed up.
		s.drainPendingTasks()

	case protocol.MsgTaskFailed:
		s.registry.DecrementTasks(agentID)
		var fail protocol.TaskFailedPayload
		if err := env.Decode(&fail); err == nil {
			s.logger.Warn("task failed",
				slog.String("agent_id", agentID),
				slog.String("task_id", env.TaskID),
				slog.String("error", fail.Error),
			)
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

	s.logger.Info("task assigned to agent",
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

func (s *Server) writeEnvelope(ctx context.Context, conn *websocket.Conn, env *protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
