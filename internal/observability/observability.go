// Package observability provides Prometheus metrics, OpenTelemetry tracing,
// health checks, and anomaly detection for Akili.
// All components are optional and nil-safe — when disabled, wrappers
// skip recording with a single nil check per operation.
package observability

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jkaninda/akili/internal/config"
)

// Observability is the top-level facade holding all observability components.
// Any field may be nil when that feature is disabled.
type Observability struct {
	Metrics *MetricsCollector
	Tracer  *TracerSetup
	Anomaly *AnomalyDetector
	Health  *HealthChecker
}

// New creates an Observability instance from config.
// Returns nil when the config is nil (all features disabled).
func New(cfg *config.ObservabilityConfig, logger *slog.Logger) (*Observability, error) {
	if cfg == nil {
		return nil, nil
	}

	obs := &Observability{}

	// Metrics.
	if cfg.Metrics != nil && cfg.Metrics.Enabled {
		obs.Metrics = NewMetricsCollector()
	}

	// Tracing.
	if cfg.Tracing != nil && cfg.Tracing.Enabled {
		ts, err := NewTracerSetup(cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("initializing tracing: %w", err)
		}
		obs.Tracer = ts
	}

	// Anomaly detection.
	if cfg.Anomaly != nil && cfg.Anomaly.Enabled {
		obs.Anomaly = NewAnomalyDetector(cfg.Anomaly, logger)
	}

	// Health checker (always created, checks added later in main.go).
	obs.Health = NewHealthChecker(logger)

	return obs, nil
}

// Shutdown releases observability resources.
func (o *Observability) Shutdown(ctx context.Context) {
	if o == nil {
		return
	}
	if o.Tracer != nil {
		_ = o.Tracer.Shutdown(ctx)
	}
}

// TracerOrNil returns the OTel tracer or nil if tracing is disabled.
func (o *Observability) TracerOrNil() *TracerSetup {
	if o == nil {
		return nil
	}
	return o.Tracer
}
