package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	accountDTO "github.com/sdq-codes/withdrawal-service/internal/apps/account/dto"
	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	accountServices "github.com/sdq-codes/withdrawal-service/internal/apps/account/services"
)

// Exits non-zero on any mismatch so a scheduler can alert on it.
var reconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile every account balance against its ledger and exit",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cfg, logger, db, err := setup(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		svc := accountServices.New(accountRepositories.NewAccountRepository(db), accountRepositories.NewTokenRepository(db), cfg.Token.TTL)
		results, err := svc.ReconcileAll(ctx)
		if err != nil {
			return err
		}
		mismatches := 0
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, rec := range results {
			if !rec.Consistent {
				mismatches++
				logger.Error("reconciliation mismatch", "account_id", rec.AccountID)
			}
			if err := enc.Encode(accountDTO.NewReconciliationResponse(rec)); err != nil {
				return err
			}
		}
		if mismatches > 0 {
			return fmt.Errorf("reconciliation failed: %d of %d accounts inconsistent", mismatches, len(results))
		}
		logger.Info("reconciliation passed", "accounts", len(results))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(reconcileCmd)
}
