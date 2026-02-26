package orchestrator

import "github.com/prometheus/client_golang/prometheus"

// WorkflowMetrics holds Prometheus metrics for the multi-agent orchestrator.
// All metrics use the akili_workflow_ namespace.
type WorkflowMetrics struct {
	WorkflowsTotal        *prometheus.CounterVec
	WorkflowDuration      *prometheus.HistogramVec
	TasksTotal            *prometheus.CounterVec
	TaskDuration          *prometheus.HistogramVec
	AgentInvocationsTotal *prometheus.CounterVec
	ActiveWorkflows       prometheus.Gauge
	ActiveTasks           prometheus.Gauge
	RecoveryAttemptsTotal *prometheus.CounterVec
}

// NewWorkflowMetrics creates and registers workflow metrics on the given registry.
// Returns nil if reg is nil.
func NewWorkflowMetrics(reg *prometheus.Registry) *WorkflowMetrics {
	if reg == nil {
		return nil
	}

	m := &WorkflowMetrics{
		WorkflowsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "total",
			Help:      "Total workflows by final status.",
		}, []string{"status"}),

		WorkflowDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "duration_seconds",
			Help:      "Workflow total duration in seconds.",
			Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"status"}),

		TasksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "tasks_total",
			Help:      "Total tasks by agent role and final status.",
		}, []string{"agent_role", "status"}),

		TaskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "task_duration_seconds",
			Help:      "Task duration in seconds by agent role.",
			Buckets:   []float64{0.5, 1, 2, 5, 10, 30, 60, 120},
		}, []string{"agent_role"}),

		AgentInvocationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "agent_invocations_total",
			Help:      "Total agent invocations by role and status.",
		}, []string{"agent_role", "status"}),

		ActiveWorkflows: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "active_workflows",
			Help:      "Number of currently running workflows.",
		}),

		ActiveTasks: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "active_tasks",
			Help:      "Number of currently running tasks.",
		}),

		RecoveryAttemptsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "workflow",
			Name:      "recovery_attempts_total",
			Help:      "Total recovery attempts by action (initiated, retry, escalate, skip).",
		}, []string{"action"}),
	}

	reg.MustRegister(
		m.WorkflowsTotal,
		m.WorkflowDuration,
		m.TasksTotal,
		m.TaskDuration,
		m.AgentInvocationsTotal,
		m.ActiveWorkflows,
		m.ActiveTasks,
		m.RecoveryAttemptsTotal,
	)

	return m
}
