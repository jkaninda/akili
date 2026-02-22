package observability

import (
	"context"
	"log/slog"
	"time"
)

const healthCheckTimeout = 3 * time.Second

// HealthChecker aggregates health from multiple subsystems.
type HealthChecker struct {
	checks []HealthCheck
	logger *slog.Logger
}

// HealthCheck is a named dependency check.
type HealthCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// HealthStatus is the JSON response for health/readiness endpoints.
type HealthStatus struct {
	Status string                 `json:"status"` // "ok" or "degraded"
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

// CheckResult is the status of a single dependency check.
type CheckResult struct {
	Status  string `json:"status"`            // "ok" or "fail"
	Message string `json:"message,omitempty"` // Error message on failure.
}

// NewHealthChecker creates a HealthChecker with no checks registered.
func NewHealthChecker(logger *slog.Logger) *HealthChecker {
	return &HealthChecker{logger: logger}
}

// AddCheck registers a named health check.
func (h *HealthChecker) AddCheck(name string, check func(ctx context.Context) error) {
	h.checks = append(h.checks, HealthCheck{Name: name, Check: check})
}

// CheckHealth returns liveness status. Always returns "ok" if the process is running.
func (h *HealthChecker) CheckHealth() HealthStatus {
	return HealthStatus{Status: "ok"}
}

// CheckReady runs all registered checks and returns aggregate readiness.
// Returns "ok" only if all checks pass; "degraded" if any fail.
func (h *HealthChecker) CheckReady(ctx context.Context) HealthStatus {
	if len(h.checks) == 0 {
		return HealthStatus{Status: "ok"}
	}

	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	status := HealthStatus{
		Status: "ok",
		Checks: make(map[string]CheckResult, len(h.checks)),
	}

	for _, c := range h.checks {
		if err := c.Check(checkCtx); err != nil {
			status.Status = "degraded"
			status.Checks[c.Name] = CheckResult{
				Status:  "fail",
				Message: err.Error(),
			}
			if h.logger != nil {
				h.logger.Warn("readiness check failed",
					slog.String("check", c.Name),
					slog.String("error", err.Error()),
				)
			}
		} else {
			status.Checks[c.Name] = CheckResult{Status: "ok"}
		}
	}

	return status
}
