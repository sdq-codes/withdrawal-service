package controllers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/sdq-codes/withdrawal-service/internal/apps/account/dto"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/services"
	"github.com/sdq-codes/withdrawal-service/internal/interface/response"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
	"github.com/sdq-codes/withdrawal-service/internal/tracing"
)

type AccountController struct {
	svc    *services.AccountService
	logger *slog.Logger
}

func NewAccountController(svc *services.AccountService, logger *slog.Logger) *AccountController {
	return &AccountController{svc: svc, logger: logger}
}

func (c *AccountController) Reconciliation(w http.ResponseWriter, r *http.Request) {
	accountID := middleware.AccountID(r.Context())
	rec, err := c.svc.Reconcile(r.Context(), accountID)
	if errors.Is(err, models.ErrAccountNotFound) {
		response.Error(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "The account does not exist.")
		return
	}
	if err != nil {
		response.InternalError(w, r, c.logger, err)
		return
	}
	if !rec.Consistent {
		c.logger.Error("reconciliation mismatch", "account_id", accountID, "trace_id", tracing.TraceID(r.Context()))
	}
	response.JSON(w, http.StatusOK, dto.NewReconciliationResponse(rec))
}

func (c *AccountController) RotateToken(w http.ResponseWriter, r *http.Request) {
	issued, err := c.svc.RotateToken(r.Context(), middleware.TokenHash(r.Context()))
	if errors.Is(err, models.ErrInvalidToken) {
		// Lost a race with a concurrent rotation or revocation of this token.
		response.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "The supplied token is invalid or revoked.")
		return
	}
	if err != nil {
		response.InternalError(w, r, c.logger, err)
		return
	}
	response.JSON(w, http.StatusCreated, dto.NewTokenResponse(issued))
}

func (c *AccountController) RevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := c.svc.RevokeToken(r.Context(), middleware.TokenHash(r.Context())); err != nil && !errors.Is(err, models.ErrInvalidToken) {
		response.InternalError(w, r, c.logger, err)
		return
	}
	response.NoContent(w)
}
