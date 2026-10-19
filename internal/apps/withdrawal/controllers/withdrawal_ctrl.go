package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/dto"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/services"
	"github.com/sdq-codes/withdrawal-service/internal/interface/response"
	"github.com/sdq-codes/withdrawal-service/internal/metrics"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
	"github.com/sdq-codes/withdrawal-service/internal/tracing"
)

type WithdrawalController struct {
	svc    *services.Service
	logger *slog.Logger
}

func NewWithdrawalController(svc *services.Service, logger *slog.Logger) *WithdrawalController {
	return &WithdrawalController{svc: svc, logger: logger}
}

func (c *WithdrawalController) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req dto.CreateWithdrawalRequest
	if err := decoder.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "Request body must be valid JSON with amount and target_currency fields.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", "Request body must contain exactly one JSON object.")
		return
	}
	wd, replayed, err := c.svc.Create(r.Context(), middleware.AccountID(r.Context()), r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		c.handleServiceError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		metrics.WithdrawalOutcomes.WithLabelValues("replayed").Inc()
	} else {
		metrics.WithdrawalOutcomes.WithLabelValues("authorised").Inc()
	}
	response.JSON(w, http.StatusCreated, wd)
}

func (c *WithdrawalController) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, models.ErrInvalidAmount), errors.Is(err, models.ErrInvalidCurrency), errors.Is(err, models.ErrInvalidIdempotencyKey):
		fail(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
	case errors.Is(err, models.ErrAccountNotFound):
		fail(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "The account does not exist.")
	case errors.Is(err, models.ErrInsufficientFunds):
		fail(w, http.StatusConflict, "INSUFFICIENT_FUNDS", "The account has insufficient funds.")
	case errors.Is(err, models.ErrIdempotencyKeyReused):
		fail(w, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED", "The Idempotency-Key was already used with a different request.")
	case errors.Is(err, models.ErrCurrencyNotSupported):
		c.logFXFailure(r, err)
		fail(w, http.StatusBadRequest, "UNSUPPORTED_CURRENCY", "The target currency is not supported by the exchange-rate provider.")
	case errors.Is(err, models.ErrExchangeRateStale):
		c.logFXFailure(r, err)
		fail(w, http.StatusBadGateway, "EXCHANGE_RATE_STALE", "The latest available exchange rate is too old to use. No funds were debited.")
	case errors.Is(err, models.ErrExchangeRateUnavailable):
		c.logFXFailure(r, err)
		fail(w, http.StatusBadGateway, "EXCHANGE_RATE_UNAVAILABLE", "The exchange-rate provider is unavailable. No funds were debited.")
	default:
		metrics.WithdrawalOutcomes.WithLabelValues("INTERNAL_ERROR").Inc()
		response.InternalError(w, r, c.logger, err)
	}
}

// The client gets a generic message; the provider failure cause is only logged.
func (c *WithdrawalController) logFXFailure(r *http.Request, err error) {
	c.logger.WarnContext(r.Context(), "exchange-rate lookup failed", "error", err.Error(), "trace_id", tracing.TraceID(r.Context()))
}

func fail(w http.ResponseWriter, status int, code, message string) {
	metrics.WithdrawalOutcomes.WithLabelValues(code).Inc()
	response.Error(w, status, code, message)
}
