package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

// MetricsCollector holds all Prometheus metrics for Akili.
// Uses a custom registry — no global state.
type MetricsCollector struct {
	Registry *prometheus.Registry

	// LLM metrics.
	LLMRequestsTotal   *prometheus.CounterVec
	LLMRequestDuration *prometheus.HistogramVec
	LLMTokensUsed      *prometheus.CounterVec

	// Tool execution metrics.
	ToolExecutionsTotal   *prometheus.CounterVec
	ToolExecutionDuration *prometheus.HistogramVec

	// Sandbox metrics.
	SandboxExecutionsTotal   *prometheus.CounterVec
	SandboxExecutionDuration *prometheus.HistogramVec

	// Security metrics.
	SecurityChecksTotal *prometheus.CounterVec

	// Budget metrics.
	BudgetSpentTotal *prometheus.CounterVec

	// HTTP gateway metrics.
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	// System metrics.
	ActiveRequests prometheus.Gauge
}

// NewMetricsCollector creates a MetricsCollector with all metrics registered
// on a custom prometheus.Registry.
func NewMetricsCollector() *MetricsCollector {
	reg := prometheus.NewRegistry()

	m := &MetricsCollector{
		Registry: reg,

		LLMRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "llm",
			Name:      "requests_total",
			Help:      "Total LLM API requests.",
		}, []string{"provider", "model", "status"}),

		LLMRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "llm",
			Name:      "request_duration_seconds",
			Help:      "LLM API request duration in seconds.",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		}, []string{"provider", "model"}),

		LLMTokensUsed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "llm",
			Name:      "tokens_used_total",
			Help:      "Total LLM tokens consumed.",
		}, []string{"provider", "model", "direction"}),

		ToolExecutionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "tool",
			Name:      "executions_total",
			Help:      "Total tool executions.",
		}, []string{"tool", "status"}),

		ToolExecutionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "tool",
			Name:      "execution_duration_seconds",
			Help:      "Tool execution duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"tool"}),

		SandboxExecutionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "sandbox",
			Name:      "executions_total",
			Help:      "Total sandbox executions.",
		}, []string{"type", "status"}),

		SandboxExecutionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "sandbox",
			Name:      "execution_duration_seconds",
			Help:      "Sandbox execution duration in seconds.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}, []string{"type"}),

		SecurityChecksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "security",
			Name:      "checks_total",
			Help:      "Total security checks performed.",
		}, []string{"check_type", "result"}),

		BudgetSpentTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "budget",
			Name:      "spent_usd_total",
			Help:      "Total budget spent in USD.",
		}, []string{"user_id"}),

		HTTPRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests.",
		}, []string{"method", "path", "status_code"}),

		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),

		ActiveRequests: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "akili",
			Name:      "active_requests",
			Help:      "Number of currently active requests.",
		}),
	}

	// Register all collectors.
	reg.MustRegister(
		m.LLMRequestsTotal,
		m.LLMRequestDuration,
		m.LLMTokensUsed,
		m.ToolExecutionsTotal,
		m.ToolExecutionDuration,
		m.SandboxExecutionsTotal,
		m.SandboxExecutionDuration,
		m.SecurityChecksTotal,
		m.BudgetSpentTotal,
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.ActiveRequests,
	)

	return m
}
