package httpapi

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	accountRoutes "github.com/sdq-codes/withdrawal-service/internal/apps/account/routes"
	withdrawalRoutes "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/routes"
	withdrawalServices "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/services"
	"github.com/sdq-codes/withdrawal-service/internal/interface/response"
	"github.com/sdq-codes/withdrawal-service/internal/router/middleware"
)

type Dependencies struct {
	DB         *sql.DB
	Rates      withdrawalServices.RateProvider
	Logger     *slog.Logger
	TokenTTL   time.Duration
	MaxRateAge time.Duration
}

func RegisterRoutes(mux *http.ServeMux, deps Dependencies) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	requireAuth := middleware.RequireAccountToken(accountRepositories.NewTokenRepository(deps.DB), deps.Logger)

	withdrawalRoutes.Register(mux, deps.DB, deps.Rates, deps.MaxRateAge, requireAuth, deps.Logger)
	accountRoutes.Register(mux, deps.DB, deps.TokenTTL, requireAuth, deps.Logger)
}
