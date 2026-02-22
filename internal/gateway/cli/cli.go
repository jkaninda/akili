// Package cli implements an interactive CLI gateway for Akili.
package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/agent"
	"github.com/jkaninda/akili/internal/approval"
)

const cliUserID = "cli-user"

// Gateway is the interactive command-line interface.
type Gateway struct {
	agent          agent.Agent
	approvalMgr    approval.ApprovalManager
	logger         *slog.Logger
	done           chan struct{} // closed by Stop to signal shutdown
	conversationID string        // persistent for the entire CLI session
}

// NewGateway creates a CLI gateway backed by the given agent.
func NewGateway(a agent.Agent, am approval.ApprovalManager, logger *slog.Logger) *Gateway {
	return &Gateway{
		agent:          a,
		approvalMgr:    am,
		logger:         logger,
		done:           make(chan struct{}),
		conversationID: uuid.New().String(),
	}
}

// Start runs the interactive REPL. Blocks until ctx is cancelled,
// Stop is called, or the user types "exit".
func (g *Gateway) Start(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("Akili — Security-First AI Agent Built for DevOps, SRE, and Platform Teams")
	fmt.Println("Type your message (or \"exit\" to quit).")
	fmt.Println()

	for {
		fmt.Print("akili> ")

		// Check for context cancellation or Stop signal between prompts.
		select {
		case <-ctx.Done():
			fmt.Println("\nShutting down.")
			return nil
		case <-g.done:
			fmt.Println("\nShutting down.")
			return nil
		default:
		}

		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			fmt.Println("Goodbye.")
			return nil
		}

		correlationID := newCorrelationID()

		input := &agent.Input{
			UserID:         cliUserID,
			Message:        line,
			CorrelationID:  correlationID,
			ConversationID: g.conversationID,
		}

		g.logger.DebugContext(ctx, "cli request",
			slog.String("user_id", cliUserID),
			slog.String("correlation_id", correlationID),
		)

		resp, err := g.agent.Process(ctx, input)
		if err != nil {
			// Check if approval is needed.
			var approvalErr *agent.ErrApprovalPending
			if errors.As(err, &approvalErr) {
				g.handleApproval(ctx, scanner, approvalErr)
				continue
			}

			g.logger.ErrorContext(ctx, "agent processing failed",
				slog.String("correlation_id", correlationID),
				slog.String("error", err.Error()),
			)
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		fmt.Println()
		fmt.Println(resp.Message)

		// Check for pending approval requests surfaced through the response.
		for _, ar := range resp.ApprovalRequests {
			g.handleApprovalRequest(ctx, scanner, &ar)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stdin: %w", err)
	}

	return nil
}

// Stop signals the REPL to shut down.
func (g *Gateway) Stop(_ context.Context) error {
	select {
	case <-g.done:
		// Already closed.
	default:
		close(g.done)
	}
	return nil
}

// handleApproval prompts the CLI user for approval of a pending action.
func (g *Gateway) handleApproval(ctx context.Context, scanner *bufio.Scanner, err *agent.ErrApprovalPending) {
	fmt.Printf("\nApproval required for %s (risk: %s)\n", err.ActionName, err.RiskLevel)
	fmt.Printf("  Tool:       %s\n", err.ToolName)
	fmt.Printf("  Requested:  %s\n", err.UserID)
	fmt.Printf("  Approval ID: %s\n", err.ApprovalID)
	fmt.Print("Approve? [y/N]: ")

	if !scanner.Scan() {
		return
	}

	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "y" || answer == "yes" {
		g.approveAndResume(ctx, err.ApprovalID)
	} else {
		if g.approvalMgr != nil {
			_ = g.approvalMgr.Deny(ctx, err.ApprovalID, cliUserID)
		}
		fmt.Println("Denied.")
	}
}

// handleApprovalRequest handles an approval request surfaced via the agent response.
func (g *Gateway) handleApprovalRequest(ctx context.Context, scanner *bufio.Scanner, ar *agent.ApprovalRequest) {
	fmt.Printf("\nApproval required for %s (risk: %s)\n", ar.ActionName, ar.RiskLevel)
	fmt.Printf("  Tool:       %s\n", ar.ToolName)
	fmt.Printf("  Requested:  %s\n", ar.UserID)
	fmt.Printf("  Approval ID: %s\n", ar.ApprovalID)
	fmt.Print("Approve? [y/N]: ")

	if !scanner.Scan() {
		return
	}

	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "y" || answer == "yes" {
		g.approveAndResume(ctx, ar.ApprovalID)
	} else {
		if g.approvalMgr != nil {
			_ = g.approvalMgr.Deny(ctx, ar.ApprovalID, cliUserID)
		}
		fmt.Println("Denied.")
	}
}

// approveAndResume approves the pending action and resumes execution.
func (g *Gateway) approveAndResume(ctx context.Context, approvalID string) {
	if g.approvalMgr == nil {
		fmt.Println("No approval manager configured.")
		return
	}

	if err := g.approvalMgr.Approve(ctx, approvalID, cliUserID); err != nil {
		fmt.Fprintf(os.Stderr, "Approval failed: %v\n", err)
		return
	}

	result, err := g.agent.ResumeWithApproval(ctx, approvalID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Execution after approval failed: %v\n", err)
		return
	}

	fmt.Printf("\nApproved. Result:\n%s\n", result.Output)
}

// newCorrelationID generates a short random hex ID for request tracing.
func newCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}
