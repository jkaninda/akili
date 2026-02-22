package observability

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// HTTPMetricsMiddleware wraps an http.Handler with request metrics and tracing.
func HTTPMetricsMiddleware(metrics *MetricsCollector, tracer trace.Tracer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tracer != nil {
			var span trace.Span
			r = r.WithContext(r.Context())
			ctx, span := tracer.Start(r.Context(), "http.request",
				trace.WithAttributes(
					attribute.String("http.method", r.Method),
					attribute.String("http.path", r.URL.Path),
				))
			defer span.End()
			r = r.WithContext(ctx)
		}

		if metrics != nil {
			metrics.ActiveRequests.Inc()
			defer metrics.ActiveRequests.Dec()
		}

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(rw, r)

		duration := time.Since(start).Seconds()

		if metrics != nil {
			metrics.HTTPRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusCode(rw.statusCode)).Inc()
			metrics.HTTPRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}
