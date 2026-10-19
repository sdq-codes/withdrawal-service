package routes

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/account/controllers"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/services"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
)

func Register(mux *http.ServeMux, db *sql.DB, tokenTTL time.Duration, requireAuth middleware.Middleware, logger *slog.Logger) {
	svc := services.New(repositories.NewAccountRepository(db), repositories.NewTokenRepository(db), tokenTTL)
	ctrl := controllers.NewAccountController(svc, logger)

	mux.HandleFunc("GET /v1/accounts/{accountID}/reconciliation", requireAuth(ctrl.Reconciliation))
	mux.HandleFunc("POST /v1/accounts/{accountID}/tokens/rotate", requireAuth(ctrl.RotateToken))
	mux.HandleFunc("DELETE /v1/accounts/{accountID}/tokens/current", requireAuth(ctrl.RevokeToken))
}
