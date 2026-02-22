package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/jkaninda/akili/internal/config"
	"github.com/jkaninda/akili/internal/llm"
	"github.com/jkaninda/akili/internal/sandbox"
	"github.com/jkaninda/akili/internal/security"
)

// --- No-op Path ---

func TestNew_NilConfig(t *testing.T) {
	obs, err := New(nil, nil)
	if err != nil {
		t.Fatalf("New(nil) error: %v", err)
	}
	if obs != nil {
		t.Fatal("expected nil Observability for nil config")
	}
}

func TestNew_AllDisabled(t *testing.T) {
	obs, err := New(&config.ObservabilityConfig{}, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if obs == nil {
		t.Fatal("expected non-nil Observability")
	}
	if obs.Metrics != nil {
		t.Error("metrics should be nil when not enabled")
	}
	if obs.Tracer != nil {
		t.Error("tracer should be nil when not enabled")
	}
	if obs.Anomaly != nil {
		t.Error("anomaly should be nil when not enabled")
	}
	if obs.Health == nil {
		t.Error("health checker should always be created")
	}
}

func TestObservability_ShutdownNil(t *testing.T) {
	// Should not panic.
	var obs *Observability
	obs.Shutdown(context.Background())
}

func TestTracerOrNil_Nil(t *testing.T) {
	var obs *Observability
	if obs.TracerOrNil() != nil {
		t.Error("expected nil tracer from nil Observability")
	}
}

// --- MetricsCollector ---

func TestMetricsCollector_Created(t *testing.T) {
	m := NewMetricsCollector()
	if m == nil {
		t.Fatal("expected non-nil MetricsCollector")
	}
	if m.Registry == nil {
		t.Fatal("expected non-nil Registry")
	}

	// Verify some metrics are registered by gathering.
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}
	// Initialize some metrics so they appear in Gather (CounterVec only appears after first use).
	m.LLMRequestsTotal.WithLabelValues("test", "", "success").Inc()
	m.SandboxExecutionsTotal.WithLabelValues("test", "success").Inc()
	m.SecurityChecksTotal.WithLabelValues("test", "allowed").Inc()
	m.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200").Inc()

	families, err = m.Registry.Gather()
	if err != nil {
		t.Fatalf("gather error after increment: %v", err)
	}

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, expected := range []string{
		"akili_llm_requests_total",
		"akili_sandbox_executions_total",
		"akili_security_checks_total",
		"akili_http_requests_total",
	} {
		if !names[expected] {
			t.Errorf("metric %q not found in registry", expected)
		}
	}
}

func TestMetricsCollector_RecordAndGather(t *testing.T) {
	m := NewMetricsCollector()

	// Increment a counter.
	m.LLMRequestsTotal.WithLabelValues("anthropic", "claude", "success").Inc()
	m.LLMRequestsTotal.WithLabelValues("anthropic", "claude", "success").Inc()
	m.LLMRequestsTotal.WithLabelValues("anthropic", "claude", "error").Inc()

	// Gather and verify.
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	var found bool
	for _, f := range families {
		if f.GetName() == "akili_llm_requests_total" {
			found = true
			for _, metric := range f.GetMetric() {
				labels := labelMap(metric.GetLabel())
				if labels["status"] == "success" {
					if got := metric.GetCounter().GetValue(); got != 2 {
						t.Errorf("success count = %v, want 2", got)
					}
				}
				if labels["status"] == "error" {
					if got := metric.GetCounter().GetValue(); got != 1 {
						t.Errorf("error count = %v, want 1", got)
					}
				}
			}
		}
	}
	if !found {
		t.Error("akili_llm_requests_total not found")
	}
}

func labelMap(pairs []*dto.LabelPair) map[string]string {
	m := make(map[string]string)
	for _, p := range pairs {
		m[p.GetName()] = p.GetValue()
	}
	return m
}

// --- HealthChecker ---

func TestHealthChecker_NoChecks(t *testing.T) {
	h := NewHealthChecker(nil)
	status := h.CheckReady(context.Background())
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
}

func TestHealthChecker_AllPass(t *testing.T) {
	h := NewHealthChecker(nil)
	h.AddCheck("db", func(ctx context.Context) error { return nil })
	h.AddCheck("sandbox", func(ctx context.Context) error { return nil })

	status := h.CheckReady(context.Background())
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
	if status.Checks["db"].Status != "ok" {
		t.Errorf("db check = %q, want ok", status.Checks["db"].Status)
	}
}

func TestHealthChecker_OneFails(t *testing.T) {
	h := NewHealthChecker(nil)
	h.AddCheck("db", func(ctx context.Context) error { return errors.New("connection refused") })
	h.AddCheck("sandbox", func(ctx context.Context) error { return nil })

	status := h.CheckReady(context.Background())
	if status.Status != "degraded" {
		t.Errorf("status = %q, want degraded", status.Status)
	}
	if status.Checks["db"].Status != "fail" {
		t.Errorf("db check = %q, want fail", status.Checks["db"].Status)
	}
	if status.Checks["sandbox"].Status != "ok" {
		t.Errorf("sandbox check = %q, want ok", status.Checks["sandbox"].Status)
	}
}

func TestHealthChecker_Liveness(t *testing.T) {
	h := NewHealthChecker(nil)
	status := h.CheckHealth()
	if status.Status != "ok" {
		t.Errorf("liveness status = %q, want ok", status.Status)
	}
}

// --- AnomalyDetector ---

func TestAnomalyDetector_NilSafe(t *testing.T) {
	// All methods should be no-ops on nil receiver.
	var a *AnomalyDetector
	a.RecordError("test")
	a.RecordSuccess("test")
	a.RecordBudgetSpend("user", 10.0)
}

func TestAnomalyDetector_ErrorRateThreshold(t *testing.T) {
	a := NewAnomalyDetector(&config.AnomalyConfig{
		Enabled:            true,
		ErrorRateThreshold: 0.5,
		WindowSeconds:      60,
	}, nil)

	// Record enough data to trigger: 6 errors, 4 successes = 60% error rate > 50%
	for i := 0; i < 4; i++ {
		a.RecordSuccess("test_op")
	}
	for i := 0; i < 6; i++ {
		a.RecordError("test_op")
	}

	// Verify internal counts (not threshold alert, which just logs).
	a.mu.Lock()
	errors := a.errorCounts["test_op"].sum()
	successes := a.successCounts["test_op"].sum()
	a.mu.Unlock()

	if errors != 6 {
		t.Errorf("errors = %v, want 6", errors)
	}
	if successes != 4 {
		t.Errorf("successes = %v, want 4", successes)
	}
}

// --- InstrumentedProvider (wrapper) ---

type mockProvider struct {
	name    string
	resp    *llm.Response
	err     error
	called  int
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) SendMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.called++
	return m.resp, m.err
}

func TestInstrumentedProvider_Success(t *testing.T) {
	metrics := NewMetricsCollector()
	inner := &mockProvider{
		name: "test",
		resp: &llm.Response{
			Content: "hello",
			Usage:   llm.Usage{InputTokens: 10, OutputTokens: 20},
		},
	}

	p := NewInstrumentedProvider(inner, metrics, nil, nil)
	resp, err := p.SendMessage(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q, want hello", resp.Content)
	}
	if inner.called != 1 {
		t.Errorf("inner called %d times, want 1", inner.called)
	}

	// Verify metrics recorded.
	val := counterValue(t, metrics.Registry, "akili_llm_requests_total", prometheus.Labels{"provider": "test", "model": "", "status": "success"})
	if val != 1 {
		t.Errorf("requests_total = %v, want 1", val)
	}
}

func TestInstrumentedProvider_Error(t *testing.T) {
	metrics := NewMetricsCollector()
	inner := &mockProvider{
		name: "test",
		err:  errors.New("api error"),
	}

	p := NewInstrumentedProvider(inner, metrics, nil, nil)
	_, err := p.SendMessage(context.Background(), &llm.Request{})
	if err == nil {
		t.Fatal("expected error")
	}

	val := counterValue(t, metrics.Registry, "akili_llm_requests_total", prometheus.Labels{"provider": "test", "model": "", "status": "error"})
	if val != 1 {
		t.Errorf("error requests_total = %v, want 1", val)
	}
}

func TestInstrumentedProvider_NilMetrics(t *testing.T) {
	inner := &mockProvider{
		name: "test",
		resp: &llm.Response{Content: "ok"},
	}

	// nil metrics — should not panic.
	p := NewInstrumentedProvider(inner, nil, nil, nil)
	resp, err := p.SendMessage(context.Background(), &llm.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q, want ok", resp.Content)
	}
}

// --- InstrumentedSandbox (wrapper) ---

type mockSandbox struct {
	result *sandbox.ExecutionResult
	err    error
}

func (m *mockSandbox) Execute(ctx context.Context, req sandbox.ExecutionRequest) (*sandbox.ExecutionResult, error) {
	return m.result, m.err
}

func TestInstrumentedSandbox_Success(t *testing.T) {
	metrics := NewMetricsCollector()
	inner := &mockSandbox{
		result: &sandbox.ExecutionResult{ExitCode: 0, Duration: 100 * time.Millisecond},
	}

	s := NewInstrumentedSandbox(inner, "process", metrics, nil, nil)
	result, err := s.Execute(context.Background(), sandbox.ExecutionRequest{Command: []string{"echo"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

	val := counterValue(t, metrics.Registry, "akili_sandbox_executions_total", prometheus.Labels{"type": "process", "status": "success"})
	if val != 1 {
		t.Errorf("sandbox executions = %v, want 1", val)
	}
}

// --- InstrumentedSecurityManager (wrapper) ---

type mockSecurityManager struct {
	permErr    error
	approvalErr error
	budgetErr  error
	reserveErr error
}

func (m *mockSecurityManager) CheckPermission(ctx context.Context, userID string, action security.Action) error {
	return m.permErr
}
func (m *mockSecurityManager) RequireApproval(ctx context.Context, userID string, action security.Action) error {
	return m.approvalErr
}
func (m *mockSecurityManager) CheckBudget(ctx context.Context, userID string, estimatedCost float64) error {
	return m.budgetErr
}
func (m *mockSecurityManager) ReserveBudget(ctx context.Context, userID string, estimatedCost float64) (func(), error) {
	if m.reserveErr != nil {
		return nil, m.reserveErr
	}
	return func() {}, nil
}
func (m *mockSecurityManager) RecordCost(ctx context.Context, userID string, actualCost float64) {}
func (m *mockSecurityManager) LogAction(ctx context.Context, event security.AuditEvent) error {
	return nil
}

func TestInstrumentedSecurityManager_CheckPermission(t *testing.T) {
	metrics := NewMetricsCollector()
	inner := &mockSecurityManager{}

	s := NewInstrumentedSecurityManager(inner, metrics, nil)
	err := s.CheckPermission(context.Background(), "alice", security.Action{Name: "read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val := counterValue(t, metrics.Registry, "akili_security_checks_total", prometheus.Labels{"check_type": "permission", "result": "allowed"})
	if val != 1 {
		t.Errorf("security checks = %v, want 1", val)
	}
}

func TestInstrumentedSecurityManager_RecordCost(t *testing.T) {
	metrics := NewMetricsCollector()
	inner := &mockSecurityManager{}

	s := NewInstrumentedSecurityManager(inner, metrics, nil)
	s.RecordCost(context.Background(), "alice", 1.50)

	val := counterValue(t, metrics.Registry, "akili_budget_spent_usd_total", prometheus.Labels{"user_id": "alice"})
	if val != 1.50 {
		t.Errorf("budget spent = %v, want 1.50", val)
	}
}

// --- HTTP Middleware ---

func TestHTTPMetricsMiddleware(t *testing.T) {
	metrics := NewMetricsCollector()

	handler := HTTPMetricsMiddleware(metrics, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	val := counterValue(t, metrics.Registry, "akili_http_requests_total", prometheus.Labels{"method": "GET", "path": "/test", "status_code": "200"})
	if val != 1 {
		t.Errorf("http requests = %v, want 1", val)
	}
}

func TestHTTPMetricsMiddleware_NilMetrics(t *testing.T) {
	// Should not panic with nil metrics.
	handler := HTTPMetricsMiddleware(nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Helpers ---

func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels prometheus.Labels) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			lm := labelMap(metric.GetLabel())
			match := true
			for k, v := range labels {
				if lm[k] != v {
					match = false
					break
				}
			}
			if match {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}
