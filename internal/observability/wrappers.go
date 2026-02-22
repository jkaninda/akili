package observability

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/security"
)

// --- InstrumentedProvider ---

// InstrumentedProvider wraps an llm.Provider with metrics, tracing, and anomaly detection.
type InstrumentedProvider struct {
	inner   llm.Provider
	metrics *MetricsCollector
	tracer  trace.Tracer
	anomaly *AnomalyDetector
}

// NewInstrumentedProvider wraps an LLM provider with observability.
func NewInstrumentedProvider(inner llm.Provider, metrics *MetricsCollector, ts *TracerSetup, anomaly *AnomalyDetector) *InstrumentedProvider {
	var tracer trace.Tracer
	if ts != nil {
		tracer = ts.Tracer()
	}
	return &InstrumentedProvider{
		inner:   inner,
		metrics: metrics,
		tracer:  tracer,
		anomaly: anomaly,
	}
}

func (p *InstrumentedProvider) Name() string { return p.inner.Name() }

func (p *InstrumentedProvider) SendMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	provider := p.inner.Name()

	if p.tracer != nil {
		var span trace.Span
		ctx, span = p.tracer.Start(ctx, "llm.send_message",
			trace.WithAttributes(
				attribute.String("llm.provider", provider),
			))
		defer span.End()
	}

	start := time.Now()
	resp, err := p.inner.SendMessage(ctx, req)
	duration := time.Since(start).Seconds()

	status := "success"
	if err != nil {
		status = "error"
		if p.tracer != nil {
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}

	if p.metrics != nil {
		p.metrics.LLMRequestsTotal.WithLabelValues(provider, "", status).Inc()
		p.metrics.LLMRequestDuration.WithLabelValues(provider, "").Observe(duration)

		if resp != nil {
			p.metrics.LLMTokensUsed.WithLabelValues(provider, "", "input").Add(float64(resp.Usage.InputTokens))
			p.metrics.LLMTokensUsed.WithLabelValues(provider, "", "output").Add(float64(resp.Usage.OutputTokens))
		}
	}

	if p.anomaly != nil {
		if err != nil {
			p.anomaly.RecordError("llm_request")
		} else {
			p.anomaly.RecordSuccess("llm_request")
		}
	}

	return resp, err
}

// --- InstrumentedSandbox ---

// InstrumentedSandbox wraps a sandbox.Sandbox with metrics, tracing, and anomaly detection.
type InstrumentedSandbox struct {
	inner       sandbox.Sandbox
	sandboxType string // "process" or "docker"
	metrics     *MetricsCollector
	tracer      trace.Tracer
	anomaly     *AnomalyDetector
}

// NewInstrumentedSandbox wraps a sandbox with observability.
func NewInstrumentedSandbox(inner sandbox.Sandbox, sandboxType string, metrics *MetricsCollector, ts *TracerSetup, anomaly *AnomalyDetector) *InstrumentedSandbox {
	var tracer trace.Tracer
	if ts != nil {
		tracer = ts.Tracer()
	}
	return &InstrumentedSandbox{
		inner:       inner,
		sandboxType: sandboxType,
		metrics:     metrics,
		tracer:      tracer,
		anomaly:     anomaly,
	}
}

func (s *InstrumentedSandbox) Execute(ctx context.Context, req sandbox.ExecutionRequest) (*sandbox.ExecutionResult, error) {
	if s.tracer != nil {
		var span trace.Span
		ctx, span = s.tracer.Start(ctx, "sandbox.execute",
			trace.WithAttributes(
				attribute.String("sandbox.type", s.sandboxType),
			))
		defer span.End()
	}

	start := time.Now()
	result, err := s.inner.Execute(ctx, req)
	duration := time.Since(start).Seconds()

	status := "success"
	if err != nil {
		status = "error"
		if s.tracer != nil {
			span := trace.SpanFromContext(ctx)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	} else if result != nil && result.ExitCode != 0 {
		status = "nonzero_exit"
		if s.tracer != nil {
			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attribute.Int("sandbox.exit_code", result.ExitCode))
		}
	}

	if s.metrics != nil {
		s.metrics.SandboxExecutionsTotal.WithLabelValues(s.sandboxType, status).Inc()
		s.metrics.SandboxExecutionDuration.WithLabelValues(s.sandboxType).Observe(duration)
	}

	if s.anomaly != nil {
		if err != nil {
			s.anomaly.RecordError("sandbox_" + s.sandboxType)
		} else {
			s.anomaly.RecordSuccess("sandbox_" + s.sandboxType)
		}
	}

	return result, err
}

// --- InstrumentedSecurityManager ---

// InstrumentedSecurityManager wraps a security.SecurityManager with metrics and tracing.
type InstrumentedSecurityManager struct {
	inner   security.SecurityManager
	metrics *MetricsCollector
	tracer  trace.Tracer
}

// NewInstrumentedSecurityManager wraps a security manager with observability.
func NewInstrumentedSecurityManager(inner security.SecurityManager, metrics *MetricsCollector, ts *TracerSetup) *InstrumentedSecurityManager {
	var tracer trace.Tracer
	if ts != nil {
		tracer = ts.Tracer()
	}
	return &InstrumentedSecurityManager{
		inner:   inner,
		metrics: metrics,
		tracer:  tracer,
	}
}

func (s *InstrumentedSecurityManager) CheckPermission(ctx context.Context, userID string, action security.Action) error {
	if s.tracer != nil {
		var span trace.Span
		ctx, span = s.tracer.Start(ctx, "security.check_permission",
			trace.WithAttributes(
				attribute.String("security.user_id", userID),
				attribute.String("security.action", action.Name),
			))
		defer span.End()
	}

	err := s.inner.CheckPermission(ctx, userID, action)
	s.recordSecurityCheck("permission", err)
	return err
}

func (s *InstrumentedSecurityManager) RequireApproval(ctx context.Context, userID string, action security.Action) error {
	if s.tracer != nil {
		var span trace.Span
		ctx, span = s.tracer.Start(ctx, "security.require_approval",
			trace.WithAttributes(
				attribute.String("security.user_id", userID),
				attribute.String("security.action", action.Name),
			))
		defer span.End()
	}

	err := s.inner.RequireApproval(ctx, userID, action)
	s.recordSecurityCheck("approval", err)
	return err
}

func (s *InstrumentedSecurityManager) CheckBudget(ctx context.Context, userID string, estimatedCost float64) error {
	if s.tracer != nil {
		var span trace.Span
		ctx, span = s.tracer.Start(ctx, "security.check_budget",
			trace.WithAttributes(
				attribute.String("security.user_id", userID),
				attribute.Float64("security.estimated_cost", estimatedCost),
			))
		defer span.End()
	}

	err := s.inner.CheckBudget(ctx, userID, estimatedCost)
	s.recordSecurityCheck("budget", err)
	return err
}

func (s *InstrumentedSecurityManager) ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error) {
	if s.tracer != nil {
		var span trace.Span
		ctx, span = s.tracer.Start(ctx, "security.reserve_budget",
			trace.WithAttributes(
				attribute.String("security.user_id", userID),
				attribute.Float64("security.estimated_cost", estimatedCost),
			))
		defer span.End()
	}

	release, err := s.inner.ReserveBudget(ctx, userID, estimatedCost)
	if err != nil {
		s.recordSecurityCheck("budget_reserve", err)
		return nil, err
	}
	s.recordSecurityCheck("budget_reserve", nil)
	return release, nil
}

func (s *InstrumentedSecurityManager) RecordCost(ctx context.Context, userID string, actualCost float64) {
	s.inner.RecordCost(ctx, userID, actualCost)

	if s.metrics != nil {
		s.metrics.BudgetSpentTotal.WithLabelValues(userID).Add(actualCost)
	}
}

func (s *InstrumentedSecurityManager) LogAction(ctx context.Context, event security.AuditEvent) error {
	return s.inner.LogAction(ctx, event)
}

func (s *InstrumentedSecurityManager) recordSecurityCheck(checkType string, err error) {
	if s.metrics == nil {
		return
	}
	result := "allowed"
	if err != nil {
		result = "denied"
	}
	s.metrics.SecurityChecksTotal.WithLabelValues(checkType, result).Inc()
}

// --- Compile-time interface checks ---

var (
	_ llm.Provider             = (*InstrumentedProvider)(nil)
	_ sandbox.Sandbox          = (*InstrumentedSandbox)(nil)
	_ security.SecurityManager = (*InstrumentedSecurityManager)(nil)
)

// statusCode returns the HTTP status code as a string for metric labels.
func statusCode(code int) string {
	return strconv.Itoa(code)
}
