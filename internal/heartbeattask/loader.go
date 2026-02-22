package heartbeattask

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// HeartbeatTaskFile represents the YAML frontmatter of a heartbeat tasks file.
type HeartbeatTaskFile struct {
	Name                   string   `yaml:"name"`
	Description            string   `yaml:"description"`
	UserID                 string   `yaml:"user_id"`
	DefaultCron            string   `yaml:"default_cron"`
	DefaultBudgetUSD       float64  `yaml:"default_budget_usd"`
	NotificationChannelIDs []string `yaml:"notification_channels"`
	Enabled                bool     `yaml:"enabled"`
}

// ParsedTask represents a single task extracted from the Markdown body.
type ParsedTask struct {
	Name           string
	Mode           string  // "quick" or "long".
	CronExpression string  // Per-task override; empty = use file default.
	BudgetUSD      float64 // Per-task override; 0 = use file default.
	Prompt         string  // The task description/goal text.
}

// Loader parses heartbeat task Markdown files.
type Loader struct {
	logger *slog.Logger
}

// NewLoader creates a new Loader.
func NewLoader(logger *slog.Logger) *Loader {
	return &Loader{logger: logger}
}

// ParseFile reads a Markdown file and returns the frontmatter + extracted tasks.
func (l *Loader) ParseFile(path string) (*HeartbeatTaskFile, []ParsedTask, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Expect first line to be "---".
	if !scanner.Scan() {
		return nil, nil, fmt.Errorf("empty file")
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return nil, nil, fmt.Errorf("missing YAML frontmatter (file must start with ---)")
	}

	// Read until closing "---".
	var frontmatterLines []string
	foundClose := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundClose = true
			break
		}
		frontmatterLines = append(frontmatterLines, line)
	}
	if !foundClose {
		return nil, nil, fmt.Errorf("unclosed YAML frontmatter (missing closing ---)")
	}

	// Parse frontmatter.
	var file HeartbeatTaskFile
	if err := yaml.Unmarshal([]byte(strings.Join(frontmatterLines, "\n")), &file); err != nil {
		return nil, nil, fmt.Errorf("parsing frontmatter: %w", err)
	}

	// Read remaining body.
	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading file: %w", err)
	}

	// Parse tasks from body.
	tasks := parseTasks(bodyLines)

	return &file, tasks, nil
}

// Validate checks that a parsed file has valid fields.
func (l *Loader) Validate(file *HeartbeatTaskFile, tasks []ParsedTask) error {
	if file.Name == "" {
		return fmt.Errorf("frontmatter 'name' is required")
	}
	for i, t := range tasks {
		if t.Name == "" {
			return fmt.Errorf("task %d: name is required", i)
		}
		if t.Mode != "quick" && t.Mode != "long" {
			return fmt.Errorf("task %q: mode must be 'quick' or 'long', got %q", t.Name, t.Mode)
		}
		if t.Prompt == "" {
			return fmt.Errorf("task %q: prompt/description is required", t.Name)
		}
	}
	return nil
}

// parseTasks extracts individual task definitions from the Markdown body.
//
// Parsing rules:
//   - "## Quick Tasks" / "## Long Tasks" headings set the default mode.
//   - "### <Task Name>" starts a new task.
//   - Bullet lines with "- cron:", "- mode:", "- budget_usd:" are metadata.
//   - All other text under the heading becomes the prompt.
func parseTasks(lines []string) []ParsedTask {
	var tasks []ParsedTask
	currentMode := "quick" // default mode
	var current *ParsedTask
	var promptLines []string

	flush := func() {
		if current != nil {
			current.Prompt = strings.TrimSpace(strings.Join(promptLines, "\n"))
			if current.Mode == "" {
				current.Mode = currentMode
			}
			tasks = append(tasks, *current)
			current = nil
			promptLines = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// ## section heading sets default mode.
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading := strings.ToLower(strings.TrimPrefix(trimmed, "## "))
			if strings.Contains(heading, "quick") {
				currentMode = "quick"
			} else if strings.Contains(heading, "long") {
				currentMode = "long"
			}
			continue
		}

		// ### task heading starts a new task.
		if strings.HasPrefix(trimmed, "### ") {
			flush()
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			current = &ParsedTask{Name: name}
			promptLines = nil
			continue
		}

		// Only parse metadata and prompt lines within a task.
		if current == nil {
			continue
		}

		// Metadata bullet lines.
		if strings.HasPrefix(trimmed, "- cron:") {
			current.CronExpression = strings.TrimSpace(strings.TrimPrefix(trimmed, "- cron:"))
			current.CronExpression = strings.Trim(current.CronExpression, "\"'")
			continue
		}
		if strings.HasPrefix(trimmed, "- mode:") {
			current.Mode = strings.TrimSpace(strings.TrimPrefix(trimmed, "- mode:"))
			continue
		}
		if strings.HasPrefix(trimmed, "- budget_usd:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "- budget_usd:"))
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				current.BudgetUSD = f
			}
			continue
		}

		// Everything else is prompt text.
		promptLines = append(promptLines, line)
	}

	flush()
	return tasks
}
