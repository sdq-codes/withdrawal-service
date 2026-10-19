package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/metrics"
	"github.com/sdq-codes/withdrawal-service/internal/tracing"
)

func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID := tracing.TraceID(r.Context())
		if traceID != "" {
			w.Header().Set("X-Trace-Id", traceID)
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start)
		metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(elapsed.Seconds())
		if route != "GET /healthz" {
			logger.Info("request completed", "method", r.Method, "route", route, "status", rec.status,
				"duration_ms", elapsed.Milliseconds(), "trace_id", traceID)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
