package cmd

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/frankfurter"
	httpapi "github.com/sdq-codes/withdrawal-service/internal/interface/http"
	"github.com/sdq-codes/withdrawal-service/internal/router"
	"github.com/sdq-codes/withdrawal-service/internal/tracing"
	"github.com/sdq-codes/withdrawal-service/version"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP API and the metrics listener",
	RunE: func(cmd *cobra.Command, args []string) error {
		return serve()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func serve() error {
	cfg, logger, db, err := setup(context.Background())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := cfg.ValidateForServe(); err != nil {
		return err
	}

	shutdownTracing, err := tracing.Setup(context.Background())
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(ctx)
	}()

	if err := accountRepositories.NewAccountRepository(db).SeedDemo(context.Background(), cfg.Demo.AccountID, cfg.Demo.BearerToken, cfg.Demo.BalanceCents); err != nil {
		return err
	}

	rates := frankfurter.NewClient(cfg.Frankfurter.BaseURL, cfg.Frankfurter.Timeout)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rates.Warm(ctx); err != nil {
			logger.Warn("exchange-rate provider warm-up failed; connecting on first request", "error", err.Error())
			return
		}
		logger.Info("exchange-rate provider connection ready")
	}()

	handler := router.New(httpapi.Dependencies{
		DB:         db,
		Rates:      rates,
		Logger:     logger,
		TokenTTL:   cfg.Token.TTL,
		MaxRateAge: cfg.Frankfurter.MaxRateAge,
	})
	server := &http.Server{
		Addr:              ":" + cfg.HTTPServer.Port,
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      7 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Separate port so metrics aren't exposed with the public API.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:              ":" + cfg.HTTPServer.MetricsPort,
		Handler:           metricsMux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		_ = metricsServer.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server stopped", "error", err)
		}
	}()
	logger.Info("withdrawal service listening", "version", version.Version, "address", server.Addr, "metrics_address", metricsServer.Addr,
		"database", cfg.Database.Path, "demo_account_id", cfg.Demo.AccountID, "demo_token", "configured (do not use in production)",
		"token_ttl", cfg.Token.TTL.String(), "fx_max_rate_age", cfg.Frankfurter.MaxRateAge.String())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
