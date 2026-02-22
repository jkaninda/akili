// Package database implements a read-only database query tool for Akili.
//
// Security:
//   - Only read-only SQL statements allowed (SELECT, EXPLAIN, SHOW, DESCRIBE)
//   - All write/DDL statements blocked (INSERT, UPDATE, DELETE, DROP, ALTER, etc.)
//   - Query timeout enforced via context
//   - Row limit enforced to prevent OOM
//   - Connection DSN configurable per-tool (separate from Akili internal DB)
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver.

	"github.com/jkaninda/akili/internal/security"
	"github.com/jkaninda/akili/internal/tools"
)

// Default limits.
const (
	defaultMaxRows       = 1000
	defaultTimeoutSec    = 30
	defaultMaxOutputRows = 500
)

// blockedPrefixes are SQL statement prefixes that indicate write/DDL operations.
var blockedPrefixes = []string{
	"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "CREATE",
	"TRUNCATE", "GRANT", "REVOKE", "COPY", "VACUUM", "REINDEX",
	"COMMENT", "LOCK", "DISCARD", "SET ", "RESET", "BEGIN",
	"COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "PREPARE",
	"EXECUTE", "DEALLOCATE", "LISTEN", "NOTIFY", "UNLISTEN",
	"LOAD", "CLUSTER", "REFRESH", "SECURITY",
}

// allowedPrefixes are the only SQL statement prefixes permitted.
var allowedPrefixes = []string{
	"SELECT", "EXPLAIN", "SHOW", "DESCRIBE", "WITH",
}

// Config holds database tool settings.
type Config struct {
	DSN            string // Connection string (e.g. "postgres://user:pass@host/db?sslmode=disable").
	MaxRows        int    // Maximum rows returned per query. Default: 1000.
	TimeoutSeconds int    // Per-query timeout. Default: 30.
}

// Tool runs read-only SQL queries against a configured database.
type Tool struct {
	config Config
	db     *sql.DB
	logger *slog.Logger
}

// NewTool creates a database read tool. The connection is opened lazily on first Execute.
func NewTool(cfg Config, logger *slog.Logger) *Tool {
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = defaultMaxRows
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = defaultTimeoutSec
	}
	return &Tool{config: cfg, logger: logger}
}

func (t *Tool) Name() string        { return "database_read" }
func (t *Tool) Description() string { return "Run read-only SQL queries (SELECT, EXPLAIN, SHOW)" }
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":    map[string]any{"type": "string", "description": "SQL query to execute (must be read-only: SELECT, EXPLAIN, SHOW, DESCRIBE, WITH)"},
			"max_rows": map[string]any{"type": "number", "description": "Maximum number of rows to return (default: 1000)"},
		},
		"required": []string{"query"},
	}
}
func (t *Tool) RequiredAction() security.Action {
	return security.Action{Name: "database_read", RiskLevel: security.RiskLow}
}
func (t *Tool) EstimateCost(_ map[string]any) float64 { return 0 }

func (t *Tool) Validate(params map[string]any) error {
	query, err := requireString(params, "query")
	if err != nil {
		return err
	}

	if err := validateReadOnly(query); err != nil {
		return err
	}

	return nil
}

// Execute runs a read-only SQL query and returns formatted results.
//
// Required params:
//
//	"query" (string) — SQL query to execute (must be read-only)
//
// Optional params:
//
//	"max_rows" (float64) — override maximum rows returned
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.Result, error) {
	query, _ := requireString(params, "query")

	// Open connection lazily.
	if err := t.ensureConnected(); err != nil {
		return nil, fmt.Errorf("database connection: %w", err)
	}

	maxRows := t.config.MaxRows
	if v, ok := params["max_rows"].(float64); ok && int(v) > 0 {
		maxRows = int(v)
		if maxRows > t.config.MaxRows {
			maxRows = t.config.MaxRows
		}
	}

	timeout := time.Duration(t.config.TimeoutSeconds) * time.Second
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t.logger.InfoContext(ctx, "database_read executing",
		slog.String("query_prefix", truncateQuery(query, 100)),
		slog.Int("max_rows", maxRows),
	)

	rows, err := t.db.QueryContext(queryCtx, query)
	if err != nil {
		return nil, fmt.Errorf("query execution: %w", err)
	}
	defer rows.Close()

	output, rowCount, err := formatRows(rows, maxRows)
	if err != nil {
		return nil, fmt.Errorf("reading results: %w", err)
	}

	return &tools.Result{
		Output:  tools.TruncateOutput(output, tools.MaxOutputBytes),
		Success: true,
		Metadata: map[string]any{
			"rows_returned": rowCount,
			"max_rows":      maxRows,
		},
	}, nil
}

// ensureConnected opens the database connection if not already open.
func (t *Tool) ensureConnected() error {
	if t.db != nil {
		return t.db.Ping()
	}
	if t.config.DSN == "" {
		return fmt.Errorf("database DSN not configured")
	}

	db, err := sql.Open("pgx", t.config.DSN)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	// Conservative connection pool for a tool (not a web server).
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("pinging database: %w", err)
	}

	t.db = db
	return nil
}

// validateReadOnly checks that a SQL statement is safe for read-only execution.
func validateReadOnly(query string) error {
	normalized := strings.TrimSpace(query)
	if normalized == "" {
		return fmt.Errorf("query must not be empty")
	}

	// Strip leading comments (-- or /* */) to find the actual statement.
	normalized = stripLeadingComments(normalized)
	upper := strings.ToUpper(normalized)

	// Check against blocked prefixes first for clear error messages.
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return fmt.Errorf("query blocked: %s statements are not allowed (read-only mode)", prefix)
		}
	}

	// Verify it starts with an allowed prefix.
	allowed := false
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(upper, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("query must start with one of: %s", strings.Join(allowedPrefixes, ", "))
	}

	// Block multiple statements (semicolons not at the end).
	trimmed := strings.TrimRight(normalized, "; \t\n\r")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("multiple statements not allowed; submit one query at a time")
	}

	return nil
}

// stripLeadingComments removes SQL comments from the beginning of a query.
func stripLeadingComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			if idx := strings.Index(s, "\n"); idx >= 0 {
				s = s[idx+1:]
			} else {
				return ""
			}
		} else if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
			} else {
				return ""
			}
		} else {
			return s
		}
	}
}

// formatRows reads SQL rows and formats them as a tab-separated table with headers.
func formatRows(rows *sql.Rows, maxRows int) (string, int, error) {
	cols, err := rows.Columns()
	if err != nil {
		return "", 0, fmt.Errorf("getting columns: %w", err)
	}

	var sb strings.Builder

	// Write header.
	sb.WriteString(strings.Join(cols, "\t"))
	sb.WriteString("\n")

	// Prepare scan targets.
	values := make([]any, len(cols))
	scanArgs := make([]any, len(cols))
	for i := range values {
		scanArgs[i] = &values[i]
	}

	rowCount := 0
	for rows.Next() {
		if rowCount >= maxRows {
			sb.WriteString(fmt.Sprintf("\n... [results truncated at %d rows]", maxRows))
			break
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return "", rowCount, fmt.Errorf("scanning row %d: %w", rowCount, err)
		}

		for i, v := range values {
			if i > 0 {
				sb.WriteString("\t")
			}
			sb.WriteString(formatValue(v))
		}
		sb.WriteString("\n")
		rowCount++
	}

	if err := rows.Err(); err != nil {
		return "", rowCount, fmt.Errorf("iterating rows: %w", err)
	}

	if rowCount == 0 {
		sb.WriteString("(no rows returned)\n")
	}

	return sb.String(), rowCount, nil
}

// formatValue converts a scanned SQL value to a display string.
func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case []byte:
		s := string(val)
		if len(s) > 500 {
			return s[:500] + "..."
		}
		return s
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// truncateQuery returns the first n characters of a query for logging.
func truncateQuery(q string, n int) string {
	q = strings.ReplaceAll(q, "\n", " ")
	if len(q) > n {
		return q[:n] + "..."
	}
	return q
}

func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string, got %T", key, v)
	}
	if s == "" {
		return "", fmt.Errorf("parameter %s must not be empty", key)
	}
	return s, nil
}
