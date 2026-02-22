package scheduler

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds Prometheus metrics for the cron scheduler.
type Metrics struct {
	JobsFired     prometheus.Counter
	JobsSucceeded prometheus.Counter
	JobsFailed    prometheus.Counter
	JobsMissed    prometheus.Counter
	TickDuration  prometheus.Histogram
}

// NewMetrics creates and registers scheduler metrics.
// Returns nil if reg is nil.
func NewMetrics(reg *prometheus.Registry) *Metrics {
	if reg == nil {
		return nil
	}

	m := &Metrics{
		JobsFired: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "scheduler",
			Name:      "jobs_fired_total",
			Help:      "Total cron jobs fired (workflow submitted).",
		}),
		JobsSucceeded: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "scheduler",
			Name:      "jobs_succeeded_total",
			Help:      "Total cron job workflow submissions that succeeded.",
		}),
		JobsFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "scheduler",
			Name:      "jobs_failed_total",
			Help:      "Total cron job workflow submissions that failed.",
		}),
		JobsMissed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "scheduler",
			Name:      "jobs_missed_total",
			Help:      "Total cron jobs skipped because they were outside the missed job window.",
		}),
		TickDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "scheduler",
			Name:      "tick_duration_seconds",
			Help:      "Duration of each scheduler tick (poll + fire cycle).",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}),
	}

	reg.MustRegister(
		m.JobsFired,
		m.JobsSucceeded,
		m.JobsFailed,
		m.JobsMissed,
		m.TickDuration,
	)

	return m
}
