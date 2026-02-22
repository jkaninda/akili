package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jkaninda/akili/internal/config"
)

var (
	onboardGatewayOutput string
	onboardAgentOutput   string
)

var onboardingCmd = &cobra.Command{
	Use:   "onboarding",
	Short: "Interactive setup wizard",
	Long:  "Generate a configuration file through an interactive wizard.",
}

var onboardGatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Generate gateway configuration",
	RunE:  runOnboardGateway,
}

var onboardAgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Generate agent configuration",
	RunE:  runOnboardAgent,
}

func init() {
	onboardGatewayCmd.Flags().StringVar(&onboardGatewayOutput, "output", config.DefaultConfigPath(), "output config file path")
	onboardAgentCmd.Flags().StringVar(&onboardAgentOutput, "output", config.DefaultConfigPath(), "output config file path")
	onboardingCmd.AddCommand(onboardGatewayCmd, onboardAgentCmd)
}

func runOnboardGateway(_ *cobra.Command, _ []string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Akili Gateway Configuration Wizard")
	fmt.Println("======================================")
	fmt.Println()

	// LLM Provider.
	providerDefault := prompt(scanner, "LLM provider (anthropic/openai/gemini/ollama)", "anthropic")
	model := prompt(scanner, "Model name", "claude-sonnet-4-5-20250929")

	// Security.
	defaultRole := prompt(scanner, "Default user role", "operator")
	budgetStr := prompt(scanner, "Default budget limit (USD)", "10.0")
	budgetUSD, _ := strconv.ParseFloat(budgetStr, 64)
	if budgetUSD <= 0 {
		budgetUSD = 10.0
	}

	// Sandbox.
	sandboxType := prompt(scanner, "Sandbox type (process/docker)", "process")

	// Gateways.
	enableHTTP := promptYesNo(scanner, "Enable HTTP API gateway?", true)
	httpAddr := ":8080"
	if enableHTTP {
		httpAddr = prompt(scanner, "HTTP listen address", ":8080")
	}
	enableCLI := promptYesNo(scanner, "Enable CLI gateway?", true)

	// WebSocket agent endpoint.
	enableWS := false
	if enableHTTP {
		enableWS = promptYesNo(scanner, "Enable WebSocket endpoint for remote agents?", false)
	}

	// Storage.
	fmt.Println()
	fmt.Println("Storage:")
	fmt.Println("  SQLite (default) — zero-config, stores data in ~/.akili/data/akili.db")
	fmt.Println("  PostgreSQL       — production-grade, required for multi-agent DB-polling mode")
	usePostgres := promptYesNo(scanner, "Use PostgreSQL instead of SQLite?", false)
	dsn := ""
	if usePostgres {
		dsn = prompt(scanner, "PostgreSQL DSN", "postgres://akili:secret@localhost:5432/akili?sslmode=disable")
	}

	// Orchestrator.
	enableOrch := promptYesNo(scanner, "Enable multi-agent orchestrator?", false)

	// Build config.
	cfg := &config.Config{
		Security: config.SecurityConfig{
			DefaultRole: defaultRole,
			Roles: map[string]config.RoleConfig{
				"viewer": {
					Permissions:  []string{"read"},
					MaxRiskLevel: "low",
				},
				"operator": {
					Permissions:     []string{"read", "write", "execute"},
					MaxRiskLevel:    "medium",
					RequireApproval: []string{"high", "critical"},
				},
				"admin": {
					Permissions:  []string{"read", "write", "execute", "admin"},
					MaxRiskLevel: "critical",
				},
			},
			UserRoles: map[string]string{
				"cli-user": defaultRole,
			},
		},
		Budget: config.BudgetConfig{
			DefaultLimitUSD: budgetUSD,
		},
		Sandbox: config.SandboxConfig{
			Type:                sandboxType,
			MaxCPUCores:         2,
			MaxMemoryMB:         512,
			MaxExecutionSeconds: 30,
		},
		Providers: config.ProvidersConfig{
			Default: providerDefault,
			Anthropic: config.AnthropicConfig{
				Model: model,
			},
		},
		Approval: config.ApprovalConfig{
			TTLSeconds: 300,
		},
		Tools: config.ToolsConfig{
			File: config.FileToolConfig{
				AllowedPaths:     []string{"/tmp", "."},
				MaxFileSizeBytes: 1048576,
			},
			Web: config.WebToolConfig{
				MaxResponseBytes: 1048576,
				TimeoutSeconds:   30,
			},
			Code: config.CodeToolConfig{
				AllowedLanguages: []string{"python", "bash", "javascript"},
			},
		},
	}

	if enableCLI {
		cfg.Gateways.CLI = &config.CLIGatewayConfig{Enabled: true}
	}
	if enableHTTP {
		cfg.Gateways.HTTP = &config.HTTPGatewayConfig{
			Enabled:    true,
			EnableDocs: false,
			ListenAddr: httpAddr,
			RateLimit:  config.RateLimitConfig{RequestsPerMinute: 60, BurstSize: 10},
		}
	}
	if enableWS {
		agentToken := prompt(scanner, "Agent authentication token (empty = no auth)", "")
		cfg.Gateways.WebSocket = &config.WebSocketGatewayConfig{
			Enabled:    true,
			AgentToken: agentToken,
		}
	}
	if usePostgres {
		cfg.Storage = &config.StorageConfig{
			Driver: "postgres",
			Postgres: &config.PostgresStorageConfig{
				DSN: dsn,
			},
		}
	}
	// SQLite is the default

	if enableOrch {
		skillsDir := prompt(scanner, "Skills directory (empty = skip)", "")
		cfg.Orchestrator = &config.OrchestratorConfig{
			Enabled:   true,
			SkillsDir: skillsDir,
		}
	}

	return writeConfig(scanner, cfg, onboardGatewayOutput)
}

func runOnboardAgent(_ *cobra.Command, _ []string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Akili Agent Configuration Wizard")
	fmt.Println("====================================")
	fmt.Println()

	// Agent connection mode.
	fmt.Println("Agent connection mode:")
	fmt.Println("  WebSocket (recommended) — connects to gateway, no database required on agent")
	fmt.Println("  DB polling (legacy)     — polls PostgreSQL for tasks, requires database")
	useWS := promptYesNo(scanner, "Use WebSocket mode? (recommended)", true)

	// LLM Provider.
	model := prompt(scanner, "Anthropic model", "claude-sonnet-4-5-20250929")

	agentIDStr := prompt(scanner, "Agent ID (empty = auto-generate from hostname)", "")
	concStr := prompt(scanner, "Max concurrent tasks", "5")
	concVal, _ := strconv.Atoi(concStr)
	if concVal <= 0 {
		concVal = 5
	}

	budgetStr := prompt(scanner, "Default budget limit (USD)", "10.0")
	budgetUSD, _ := strconv.ParseFloat(budgetStr, 64)
	if budgetUSD <= 0 {
		budgetUSD = 10.0
	}

	cfg := &config.Config{
		Security: config.SecurityConfig{
			DefaultRole: "operator",
			Roles: map[string]config.RoleConfig{
				"operator": {
					Permissions:  []string{"read", "write", "execute"},
					MaxRiskLevel: "medium",
				},
			},
			UserRoles: map[string]string{},
		},
		Budget: config.BudgetConfig{
			DefaultLimitUSD: budgetUSD,
		},
		Sandbox: config.SandboxConfig{
			Type:                "process",
			MaxCPUCores:         2,
			MaxMemoryMB:         512,
			MaxExecutionSeconds: 30,
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.AnthropicConfig{
				Model: model,
			},
		},
		Approval: config.ApprovalConfig{
			TTLSeconds: 300,
		},
		Tools: config.ToolsConfig{
			File: config.FileToolConfig{
				AllowedPaths:     []string{"/tmp", "."},
				MaxFileSizeBytes: 1048576,
			},
			Web: config.WebToolConfig{
				MaxResponseBytes: 1048576,
				TimeoutSeconds:   30,
			},
			Code: config.CodeToolConfig{
				AllowedLanguages: []string{"python", "bash", "javascript"},
			},
		},
		Runtime: &config.RuntimeConfig{
			Mode:  "agent",
			Agent: &config.AgentConfig{},
		},
	}

	if useWS {
		// WebSocket mode.
		gatewayURL := prompt(scanner, "Gateway WebSocket URL", "ws://localhost:8080/ws/agents")
		token := prompt(scanner, "Agent authentication token (empty = no auth)", "")

		cfg.Runtime.Agent = &config.AgentConfig{
			AgentID:            agentIDStr,
			GatewayURL:         gatewayURL,
			Token:              token,
			MaxConcurrentTasks: concVal,
		}
		// No database needed for WebSocket agents.
	} else {
		// Legacy DB-polling mode.
		dsn := prompt(scanner, "PostgreSQL DSN", "postgres://akili:secret@localhost:5432/akili?sslmode=disable")
		pollStr := prompt(scanner, "Poll interval (seconds)", "2")
		rolesStr := prompt(scanner, "Agent roles (comma-separated, empty = all)", "")

		pollSec, _ := strconv.Atoi(pollStr)
		if pollSec <= 0 {
			pollSec = 2
		}

		var roles []string
		if rolesStr != "" {
			for _, r := range strings.Split(rolesStr, ",") {
				if trimmed := strings.TrimSpace(r); trimmed != "" {
					roles = append(roles, trimmed)
				}
			}
		}

		cfg.Storage = &config.StorageConfig{
			Driver: "postgres",
			Postgres: &config.PostgresStorageConfig{
				DSN: dsn,
			},
		}
		cfg.Orchestrator = &config.OrchestratorConfig{
			Enabled: true,
		}
		cfg.Runtime.Agent = &config.AgentConfig{
			AgentID:             agentIDStr,
			Roles:               roles,
			PollIntervalSeconds: pollSec,
			MaxConcurrentTasks:  concVal,
		}
	}

	skillsDir := prompt(scanner, "Skills directory (empty = skip)", "")
	if skillsDir != "" {
		if cfg.Orchestrator == nil {
			cfg.Orchestrator = &config.OrchestratorConfig{Enabled: true}
		}
		cfg.Orchestrator.SkillsDir = skillsDir
	}

	if err := writeConfig(scanner, cfg, onboardAgentOutput); err != nil {
		return err
	}

	fmt.Println("\nRemember to set the ANTHROPIC_API_KEY environment variable!")
	if useWS {
		fmt.Printf("Start the agent with: akili agent --config %s\n", onboardAgentOutput)
	} else {
		fmt.Printf("Start the agent with: akili agent --config %s\n", onboardAgentOutput)
	}
	return nil
}

// writeConfig marshals and optionally writes a config to a file.
func writeConfig(scanner *bufio.Scanner, cfg *config.Config, outputPath string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	fmt.Printf("\nGenerated config:\n%s\n\n", data)
	if promptYesNo(scanner, fmt.Sprintf("Write to %s?", outputPath), true) {
		dir := filepath.Dir(outputPath)
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("creating directory %s: %w", dir, err)
		}
		if err := os.WriteFile(outputPath, append(data, '\n'), 0644); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
		fmt.Printf("Config written to %s\n", outputPath)
	}

	return nil
}

// prompt asks the user for input with a default value.
func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	if !scanner.Scan() {
		return defaultVal
	}
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

// promptYesNo asks a yes/no question.
func promptYesNo(scanner *bufio.Scanner, question string, defaultYes bool) bool {
	suffix := "[Y/n]"
	if !defaultYes {
		suffix = "[y/N]"
	}
	fmt.Printf("%s %s: ", question, suffix)
	if !scanner.Scan() {
		return defaultYes
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer == "" {
		return defaultYes
	}
	return answer == "y" || answer == "yes"
}
