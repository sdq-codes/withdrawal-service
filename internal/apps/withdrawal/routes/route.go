package routes

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/controllers"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/services"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
)

func Register(mux *http.ServeMux, db *sql.DB, rates services.RateProvider, maxRateAge time.Duration, requireAuth middleware.Middleware, logger *slog.Logger) {
	svc := services.NewService(repositories.New(db), rates, services.WithMaxRateAge(maxRateAge))
	ctrl := controllers.NewWithdrawalController(svc, logger)

	mux.HandleFunc("POST /v1/accounts/{accountID}/withdrawals", requireAuth(ctrl.Create))
}
