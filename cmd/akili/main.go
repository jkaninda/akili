// Akili — Autonomous AI Operator System for DevOps & SRE.
package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "akili",
	Short: "Akili — Autonomous AI Operator System for SRE, DevOps, and Platform teams.",
	Long: `Akili is a security-first autonomous AI operator system for DevOps and SRE teams.
It coordinates multiple specialized roles through a DAG-based task scheduler
to decompose and execute complex operational goals autonomously within policy boundaries.`,
	RunE:          runGateway, // Default to gateway mode.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(gatewayCmd, agentCmd, queryCmd, onboardingCmd, versionCmd)
	_ = godotenv.Load()

}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
