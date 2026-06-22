// Package cmd implements the postura CLI.
package cmd

import (
	"fmt"
	"os"

	"github.com/jackchuka/postura/internal/version"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "postura",
	Short:         "Audit a GitHub org/enterprise against a configurable security baseline",
	Version:       version.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.SetVersionTemplate(
		"postura {{.Version}} (commit: " + version.Commit + ", built: " + version.BuildDate + ")\n",
	)
}

// Execute runs the postura CLI.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}
