package heartbeattask

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus metrics for the heartbeat task runner.
type Metrics struct {
	TasksExecuted  prometheus.Counter
	TasksSucceeded prometheus.Counter
	TasksFailed    prometheus.Counter
	TickDuration   prometheus.Histogram
	TaskDuration   prometheus.Histogram
}

// NewMetrics registers and returns heartbeat task metrics.
func NewMetrics(reg *prometheus.Registry) *Metrics {
	m := &Metrics{
		TasksExecuted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "akili_heartbeat_tasks_executed_total",
			Help: "Total heartbeat tasks executed.",
		}),
		TasksSucceeded: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "akili_heartbeat_tasks_succeeded_total",
			Help: "Total heartbeat tasks that succeeded.",
		}),
		TasksFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "akili_heartbeat_tasks_failed_total",
			Help: "Total heartbeat tasks that failed.",
		}),
		TickDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "akili_heartbeat_tasks_tick_duration_seconds",
			Help:    "Seconds per heartbeat task poll cycle.",
			Buckets: prometheus.DefBuckets,
		}),
		TaskDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "akili_heartbeat_task_duration_seconds",
			Help:    "Seconds per individual heartbeat task execution.",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
		}),
	}
	reg.MustRegister(m.TasksExecuted, m.TasksSucceeded, m.TasksFailed, m.TickDuration, m.TaskDuration)
	return m
}
