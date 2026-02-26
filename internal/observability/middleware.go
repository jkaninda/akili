package observability

import (
	"net/http"
	"time"

	"github.com/jkaninda/okapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func MetricsMiddleware(metrics *MetricsCollector, tracer trace.Tracer) okapi.Middleware {
	return func(next okapi.HandlerFunc) okapi.HandlerFunc {
		return func(c *okapi.Context) error {
			r := c.Request()

			if tracer != nil {
				_, span := tracer.Start(r.Context(), "http.request",
					trace.WithAttributes(
						attribute.String("http.method", r.Method),
						attribute.String("http.path", r.URL.Path),
					))
				defer span.End()
			}

			if metrics != nil {
				metrics.ActiveRequests.Inc()
				defer metrics.ActiveRequests.Dec()
			}

			start := time.Now()

			err := next(c)

			duration := time.Since(start).Seconds()

			if metrics != nil {
				code := c.Response().StatusCode()
				if code == 0 {
					code = http.StatusOK
				}
				metrics.HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusCode(code)).Inc()
				metrics.HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
			}

			return err
		}
	}
}
