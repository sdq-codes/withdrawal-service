package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sdq-codes/withdrawal-service/internal/tracing"
)

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const TimeoutBody = `{"error":{"code":"REQUEST_TIMEOUT","message":"Request timed out."}}`

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorEnvelope{Error: APIError{Code: code, Message: message}})
}

func NoContent(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func InternalError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err, "trace_id", tracing.TraceID(r.Context()))
	Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
}
