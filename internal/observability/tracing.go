package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/jkaninda/akili/internal/config"
)

// TracerSetup holds the OTel TracerProvider and a named tracer.
// NOT set as global — injected via dependency injection.
type TracerSetup struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
}

// NewTracerSetup creates an OTel TracerProvider with an OTLP exporter.
func NewTracerSetup(cfg *config.TracingConfig) (*TracerSetup, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}

	ctx := context.Background()

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "akili"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	var exporter sdktrace.SpanExporter
	switch cfg.Protocol {
	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exporter, err = otlptracehttp.New(ctx, opts...)
	default: // "grpc" or empty
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	sampleRate := cfg.SampleRate
	if sampleRate <= 0 {
		sampleRate = 1.0
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(sampleRate)),
	)

	return &TracerSetup{
		provider: tp,
		tracer:   tp.Tracer(serviceName),
	}, nil
}

// Tracer returns the named tracer for creating spans.
func (t *TracerSetup) Tracer() trace.Tracer {
	if t == nil {
		return trace.NewNoopTracerProvider().Tracer("")
	}
	return t.tracer
}

// Shutdown flushes any pending spans and shuts down the TracerProvider.
func (t *TracerSetup) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	return t.provider.Shutdown(ctx)
}
