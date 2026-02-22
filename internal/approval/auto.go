package approval

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// AutoApprover tracks approval patterns and auto-approves repeated safe operations.
// When the same user+tool+params combination has been manually approved N times
// within a lookback window, subsequent identical requests are auto-approved.
type AutoApprover struct {
	mu       sync.RWMutex
	history  map[string][]time.Time // key → timestamps of manual approvals
	counters map[string]int         // userID → auto-approval count this hour
	hourSlot int64                  // current hour slot for counter reset
	config   AutoApprovalConfig
	logger   *slog.Logger
}

// AutoApprovalConfig controls auto-approval behavior.
type AutoApprovalConfig struct {
	Enabled           bool
	MaxAutoApprovals  int      // Per user per hour. Default: 10.
	AllowedTools      []string // Tools eligible for auto-approval.
	RequiredApprovals int      // Manual approvals needed before auto. Default: 3.
	WindowHours       int      // Lookback window in hours. Default: 24.
}

// NewAutoApprover creates an AutoApprover with the given config.
func NewAutoApprover(cfg AutoApprovalConfig, logger *slog.Logger) *AutoApprover {
	if cfg.MaxAutoApprovals <= 0 {
		cfg.MaxAutoApprovals = 10
	}
	if cfg.RequiredApprovals <= 0 {
		cfg.RequiredApprovals = 3
	}
	if cfg.WindowHours <= 0 {
		cfg.WindowHours = 24
	}
	return &AutoApprover{
		history:  make(map[string][]time.Time),
		counters: make(map[string]int),
		config:   cfg,
		logger:   logger,
	}
}

// ShouldAutoApprove checks if an identical operation was manually approved
// enough times to warrant automatic approval.
func (a *AutoApprover) ShouldAutoApprove(userID, toolName string, params map[string]any) (bool, string) {
	if !a.config.Enabled {
		return false, ""
	}

	if !a.isToolAllowed(toolName) {
		return false, ""
	}

	// Check hourly auto-approval cap.
	a.mu.Lock()
	currentHour := time.Now().Unix() / 3600
	if currentHour != a.hourSlot {
		a.counters = make(map[string]int)
		a.hourSlot = currentHour
	}
	if a.counters[userID] >= a.config.MaxAutoApprovals {
		a.mu.Unlock()
		return false, ""
	}
	a.mu.Unlock()

	key := approvalKey(userID, toolName, params)
	cutoff := time.Now().Add(-time.Duration(a.config.WindowHours) * time.Hour)

	a.mu.RLock()
	timestamps := a.history[key]
	a.mu.RUnlock()

	// Count recent manual approvals.
	recent := 0
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			recent++
		}
	}

	if recent >= a.config.RequiredApprovals {
		a.mu.Lock()
		a.counters[userID]++
		a.mu.Unlock()

		reason := fmt.Sprintf("%d prior manual approvals in %dh window", recent, a.config.WindowHours)
		a.logger.Info("auto-approving tool execution",
			slog.String("user_id", userID),
			slog.String("tool", toolName),
			slog.String("reason", reason),
		)
		return true, reason
	}

	return false, ""
}

// RecordManualApproval records that a user manually approved a specific operation.
func (a *AutoApprover) RecordManualApproval(userID, toolName string, params map[string]any) {
	key := approvalKey(userID, toolName, params)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history[key] = append(a.history[key], time.Now())

	// Prune old entries beyond the window.
	cutoff := time.Now().Add(-time.Duration(a.config.WindowHours) * time.Hour)
	entries := a.history[key]
	pruned := make([]time.Time, 0, len(entries))
	for _, ts := range entries {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	a.history[key] = pruned
}

func (a *AutoApprover) isToolAllowed(toolName string) bool {
	if len(a.config.AllowedTools) == 0 {
		return false // Explicit allowlist required.
	}
	for _, t := range a.config.AllowedTools {
		if t == toolName {
			return true
		}
	}
	return false
}

func approvalKey(userID, toolName string, params map[string]any) string {
	data, _ := json.Marshal(params)
	h := sha256.Sum256(append([]byte(userID+"|"+toolName+"|"), data...))
	return fmt.Sprintf("%x", h[:16])
}
