package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/sdq-codes/withdrawal-service/config"
	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/internal/logger"
)

var rootCmd = &cobra.Command{
	Use:           "withdrawal-service",
	Short:         "Withdrawal authorisation service",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func setup(ctx context.Context) (*config.Config, *slog.Logger, *sql.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	log := logger.New(cfg.Log.Level)
	db, err := database.Open(cfg.Database.Path)
	if err != nil {
		return nil, nil, nil, err
	}
	applied, err := database.Migrate(ctx, db)
	if err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	for _, m := range applied {
		log.Info("applied migration", "version", m.Version, "name", m.Name)
	}
	return cfg, log, db, nil
}
