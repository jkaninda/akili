package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RecoveryAction is the action the diagnostician recommends.
type RecoveryAction string

const (
	RecoveryRetry    RecoveryAction = "retry"
	RecoveryEscalate RecoveryAction = "escalate"
	RecoverySkip     RecoveryAction = "skip"
)

// RecoveryDecision is the parsed output from a diagnostician agent.
type RecoveryDecision struct {
	Action     RecoveryAction `json:"action"`
	Reason     string         `json:"reason"`
	Evidence   []string       `json:"evidence"`
	RootCause  string         `json:"root_cause"`
	Confidence float64        `json:"confidence"`
}

// parseRecoveryDecision extracts a RecoveryDecision from agent output.
// If confidence is below the safety floor (0.5) and the action is retry,
// the decision is overridden to escalate.
func parseRecoveryDecision(output *AgentOutput) (*RecoveryDecision, error) {
	if output.Response == "" {
		return nil, fmt.Errorf("diagnostician returned empty response")
	}

	var decision RecoveryDecision
	if err := json.Unmarshal([]byte(output.Response), &decision); err != nil {
		// Try to find JSON object within the response (diagnostician might include surrounding text).
		start := strings.Index(output.Response, "{")
		end := strings.LastIndex(output.Response, "}")
		if start < 0 || end <= start {
			return nil, fmt.Errorf("no JSON object in diagnostician response: %w", err)
		}
		if err := json.Unmarshal([]byte(output.Response[start:end+1]), &decision); err != nil {
			return nil, fmt.Errorf("parsing diagnostician JSON: %w", err)
		}
	}

	// Validate action.
	switch decision.Action {
	case RecoveryRetry, RecoveryEscalate, RecoverySkip:
		// valid
	default:
		return nil, fmt.Errorf("unknown recovery action: %s", decision.Action)
	}

	// Safety: low confidence forces escalation (Default Deny applied to recovery).
	if decision.Confidence < 0.5 && decision.Action == RecoveryRetry {
		decision.Action = RecoveryEscalate
		decision.Reason += " [overridden: confidence too low for automatic retry]"
	}

	return &decision, nil
}

// buildDiagnosticInput constructs the prompt input for a diagnostician task.
func buildDiagnosticInput(failedTask *Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Failed Task Analysis\n\n")
	fmt.Fprintf(&b, "**Task ID**: %s\n", failedTask.ID)
	fmt.Fprintf(&b, "**Role**: %s\n", failedTask.AgentRole)
	fmt.Fprintf(&b, "**Description**: %s\n", failedTask.Description)
	fmt.Fprintf(&b, "**Input**: %s\n\n", failedTask.Input)
	fmt.Fprintf(&b, "**Error**: %s\n\n", failedTask.Error)
	fmt.Fprintf(&b, "**Retry Count**: %d\n", failedTask.RetryCount)

	maxRetries := failedTask.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2 // display the default
	}
	fmt.Fprintf(&b, "**Max Retries**: %d\n\n", maxRetries)

	if failedTask.Output != "" {
		fmt.Fprintf(&b, "**Partial Output**:\n%s\n\n", failedTask.Output)
	}

	b.WriteString("Analyze this failure and recommend a recovery action.\n")
	return b.String()
}
