package alerting

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds Prometheus metrics for the alert checker.
type Metrics struct {
	ChecksRun       prometheus.Counter
	ChecksSucceeded prometheus.Counter
	ChecksFailed    prometheus.Counter
	AlertsFired     prometheus.Counter
	CheckDuration   prometheus.Histogram
}

// NewMetrics creates and registers alert checker metrics.
// Returns nil if reg is nil.
func NewMetrics(reg *prometheus.Registry) *Metrics {
	if reg == nil {
		return nil
	}

	m := &Metrics{
		ChecksRun: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "alerting",
			Name:      "checks_run_total",
			Help:      "Total alert rule checks executed.",
		}),
		ChecksSucceeded: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "alerting",
			Name:      "checks_succeeded_total",
			Help:      "Total alert rule checks that completed without error.",
		}),
		ChecksFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "alerting",
			Name:      "checks_failed_total",
			Help:      "Total alert rule checks that failed with an error.",
		}),
		AlertsFired: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "alerting",
			Name:      "alerts_fired_total",
			Help:      "Total alert notifications dispatched.",
		}),
		CheckDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "akili",
			Subsystem: "alerting",
			Name:      "tick_duration_seconds",
			Help:      "Duration of each alert checker tick (poll + evaluate cycle).",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		}),
	}

	reg.MustRegister(
		m.ChecksRun,
		m.ChecksSucceeded,
		m.ChecksFailed,
		m.AlertsFired,
		m.CheckDuration,
	)

	return m
}
