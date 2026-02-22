package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jkaninda/akili/internal/protocol"
)

// WSClientConfig configures the agent-side WebSocket client.
type WSClientConfig struct {
	GatewayURL        string
	Token             string
	AgentID           string
	Skills            []string
	Model             string
	MaxParallel       int
	Version           string
	HeartbeatInterval time.Duration
	ReconnectInterval time.Duration
}

// TaskHandler is called when the agent receives a task assignment.
type TaskHandler func(ctx context.Context, assignment protocol.TaskAssignment) (*protocol.TaskResultPayload, error)

// ApprovalHandler is called when the agent receives an approval result.
type ApprovalHandler func(approvalID, decision, approverID string)

// WSClient is the agent-side WebSocket client that connects to the Gateway.
type WSClient struct {
	cfg             WSClientConfig
	logger          *slog.Logger
	taskHandler     TaskHandler
	approvalHandler ApprovalHandler

	conn   *websocket.Conn
	connMu sync.Mutex

	// Active tasks tracking.
	activeMu    sync.Mutex
	activeTasks int
}

// NewWSClient creates a new WebSocket client for agent mode.
func NewWSClient(cfg WSClientConfig, logger *slog.Logger) *WSClient {
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = 30 * time.Second
	}
	if cfg.ReconnectInterval == 0 {
		cfg.ReconnectInterval = 5 * time.Second
	}
	if cfg.MaxParallel == 0 {
		cfg.MaxParallel = 5
	}
	return &WSClient{
		cfg:    cfg,
		logger: logger,
	}
}

// OnTask sets the handler for incoming task assignments.
func (c *WSClient) OnTask(handler TaskHandler) {
	c.taskHandler = handler
}

// OnApproval sets the handler for incoming approval results.
func (c *WSClient) OnApproval(handler ApprovalHandler) {
	c.approvalHandler = handler
}

// Run connects to the gateway and enters the main message loop.
// Reconnects automatically on disconnect with exponential backoff.
// Blocks until the context is cancelled.
func (c *WSClient) Run(ctx context.Context) error {
	attempt := 0
	for {
		err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempt++
		backoff := c.backoff(attempt)
		c.logger.Warn("disconnected from gateway, reconnecting",
			slog.String("error", err.Error()),
			slog.String("backoff", backoff.String()),
			slog.Int("attempt", attempt),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func (c *WSClient) connectAndServe(ctx context.Context) error {
	// Build dial URL with token.
	dialURL := c.cfg.GatewayURL
	if c.cfg.Token != "" {
		sep := "?"
		if len(dialURL) > 0 {
			for _, ch := range dialURL {
				if ch == '?' {
					sep = "&"
					break
				}
			}
		}
		dialURL += sep + "token=" + c.cfg.Token
	}

	conn, _, err := websocket.Dial(ctx, dialURL, &websocket.DialOptions{
		Subprotocols: []string{"akili-agent-v1"},
	})
	if err != nil {
		return fmt.Errorf("dialing gateway: %w", err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	defer func() {
		c.connMu.Lock()
		c.conn = nil
		c.connMu.Unlock()
		conn.Close(websocket.StatusNormalClosure, "agent shutting down")
	}()

	// Send registration.
	if err := c.sendRegistration(ctx, conn); err != nil {
		return fmt.Errorf("registration: %w", err)
	}

	// Wait for confirmation.
	if err := c.waitForRegistered(ctx, conn); err != nil {
		return fmt.Errorf("registration confirmation: %w", err)
	}

	c.logger.Info("connected to gateway",
		slog.String("url", c.cfg.GatewayURL),
		slog.String("agent_id", c.cfg.AgentID),
	)

	// Start heartbeat.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go c.heartbeatLoop(hbCtx, conn)

	// Main message loop.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			c.logger.Warn("invalid message from gateway", slog.String("error", err.Error()))
			continue
		}

		c.handleMessage(ctx, conn, &env)
	}
}

func (c *WSClient) sendRegistration(ctx context.Context, conn *websocket.Conn) error {
	env, err := protocol.NewEnvelope(protocol.MsgAgentRegister, protocol.AgentCapabilities{
		AgentID:     c.cfg.AgentID,
		Skills:      c.cfg.Skills,
		Model:       c.cfg.Model,
		MaxParallel: c.cfg.MaxParallel,
		Version:     c.cfg.Version,
	})
	if err != nil {
		return err
	}
	env.AgentID = c.cfg.AgentID
	return c.writeEnvelope(ctx, conn, env)
}

func (c *WSClient) waitForRegistered(ctx context.Context, conn *websocket.Conn) error {
	regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, data, err := conn.Read(regCtx)
	if err != nil {
		return fmt.Errorf("reading confirmation: %w", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("parsing confirmation: %w", err)
	}

	if env.Type != protocol.MsgRegistered {
		return fmt.Errorf("expected gateway.registered, got %s", env.Type)
	}

	return nil
}

func (c *WSClient) handleMessage(ctx context.Context, conn *websocket.Conn, env *protocol.Envelope) {
	switch env.Type {
	case protocol.MsgTaskAssign:
		var assignment protocol.TaskAssignment
		if err := env.Decode(&assignment); err != nil {
			c.logger.Error("invalid task assignment", slog.String("error", err.Error()))
			return
		}

		// Accept the task.
		c.activeMu.Lock()
		c.activeTasks++
		c.activeMu.Unlock()

		accepted, _ := protocol.NewEnvelope(protocol.MsgTaskAccepted, protocol.TaskAcceptedPayload{
			TaskID: assignment.TaskID,
		})
		accepted.AgentID = c.cfg.AgentID
		accepted.TaskID = assignment.TaskID
		c.writeEnvelope(ctx, conn, accepted)

		// Execute asynchronously.
		go c.executeTask(ctx, conn, assignment)

	case protocol.MsgTaskCancel:
		c.logger.Info("task cancel received", slog.String("task_id", env.TaskID))

	// TODO: cancel in progress task

	case protocol.MsgApprovalResult:
		var result protocol.ApprovalResultPayload
		if err := env.Decode(&result); err == nil && c.approvalHandler != nil {
			c.approvalHandler(result.ApprovalID, result.Decision, result.ApproverID)
		}

	case protocol.MsgPing:
		pong, _ := protocol.NewEnvelope(protocol.MsgPong, nil)
		pong.AgentID = c.cfg.AgentID
		c.writeEnvelope(ctx, conn, pong)

	case protocol.MsgError:
		var errPayload protocol.ErrorPayload
		if err := env.Decode(&errPayload); err == nil {
			c.logger.Error("error from gateway",
				slog.String("code", errPayload.Code),
				slog.String("message", errPayload.Message),
			)
		}

	default:
		c.logger.Debug("unknown message from gateway", slog.String("type", string(env.Type)))
	}
}

func (c *WSClient) executeTask(ctx context.Context, conn *websocket.Conn, assignment protocol.TaskAssignment) {
	defer func() {
		c.activeMu.Lock()
		c.activeTasks--
		c.activeMu.Unlock()
	}()

	if c.taskHandler == nil {
		c.sendTaskFailed(ctx, conn, assignment.TaskID, "no task handler configured")
		return
	}

	// Execute with timeout.
	timeout := time.Duration(assignment.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := c.taskHandler(taskCtx, assignment)
	if err != nil {
		c.sendTaskFailed(ctx, conn, assignment.TaskID, err.Error())
		return
	}

	env, _ := protocol.NewEnvelope(protocol.MsgTaskResult, result)
	env.AgentID = c.cfg.AgentID
	env.TaskID = assignment.TaskID
	c.writeEnvelope(ctx, conn, env)
}

func (c *WSClient) sendTaskFailed(ctx context.Context, conn *websocket.Conn, taskID, errMsg string) {
	env, _ := protocol.NewEnvelope(protocol.MsgTaskFailed, protocol.TaskFailedPayload{
		Error: errMsg,
	})
	env.AgentID = c.cfg.AgentID
	env.TaskID = taskID
	c.writeEnvelope(ctx, conn, env)
}

// SendApprovalRequest sends an approval request to the gateway.
func (c *WSClient) SendApprovalRequest(ctx context.Context, req protocol.ApprovalRequestPayload) error {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected to gateway")
	}

	env, _ := protocol.NewEnvelope(protocol.MsgApprovalRequest, req)
	env.AgentID = c.cfg.AgentID
	return c.writeEnvelope(ctx, conn, env)
}

// SendProgress sends a task progress update to the gateway.
func (c *WSClient) SendProgress(ctx context.Context, taskID string, progress protocol.TaskProgress) error {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected to gateway")
	}

	env, _ := protocol.NewEnvelope(protocol.MsgTaskProgress, progress)
	env.AgentID = c.cfg.AgentID
	env.TaskID = taskID
	return c.writeEnvelope(ctx, conn, env)
}

func (c *WSClient) heartbeatLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.activeMu.Lock()
			active := c.activeTasks
			c.activeMu.Unlock()

			env, _ := protocol.NewEnvelope(protocol.MsgAgentHeartbeat, protocol.HeartbeatPayload{
				ActiveTasks: active,
			})
			env.AgentID = c.cfg.AgentID
			if err := c.writeEnvelope(ctx, conn, env); err != nil {
				return
			}
		}
	}
}

func (c *WSClient) writeEnvelope(ctx context.Context, conn *websocket.Conn, env *protocol.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// backoff returns exponential backoff duration with jitter, capped at 60s.
func (c *WSClient) backoff(attempt int) time.Duration {
	base := c.cfg.ReconnectInterval
	mult := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(base) * mult)
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}
