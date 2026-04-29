package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "pico",
	Short: "pico - Pilab Bot CLI",
	Long:  `pico is a CLI tool for Pilab operations`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

// Execute runs the CLI.
func Execute() error {
	rootCmd.AddCommand(gitsummaryCmd)
	rootCmd.AddCommand(wafreportCmd)
	rootCmd.AddCommand(testLLMCmd)
	return rootCmd.Execute()
}
