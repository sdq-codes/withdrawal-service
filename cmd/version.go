package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sdq-codes/withdrawal-service/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the build version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "withdrawal-service %s (commit %s, built %s)\n", version.Version, orUnknown(version.GitCommit), orUnknown(version.BuildTime))
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
