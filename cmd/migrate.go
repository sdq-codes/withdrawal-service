package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sdq-codes/withdrawal-service/database"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply pending database migrations and exit",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		_, _, db, err := setup(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		current, err := database.Version(ctx, db)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "database schema at version %d\n", current)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
