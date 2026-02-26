package soul

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jkaninda/akili/internal/llm"
)

// reflectionLoop runs the periodic self-reflection cycle.
func (m *Manager) reflectionLoop(ctx context.Context) {
	interval := m.config.reflectionInterval()
	m.logger.Info("soul reflection loop started", slog.Duration("interval", interval))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("soul reflection loop stopped")
			return
		case <-ticker.C:
			if err := m.RunReflection(ctx); err != nil {
				m.logger.Warn("soul reflection failed", slog.String("error", err.Error()))
			}
		}
	}
}

// RunReflection performs a single LLM-based self-reflection cycle.
// It reads the current soul state, asks the LLM for improvements,
// and records any resulting evolution events.
func (m *Manager) RunReflection(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return nil
	}

	m.mu.RLock()
	currentSoul := m.soul
	m.mu.RUnlock()

	if currentSoul == nil {
		return nil
	}

	m.logger.Info("starting soul reflection cycle",
		slog.Int("soul_version", currentSoul.Version),
	)

	// Build reflection prompt with current soul state.
	prompt := BuildReflectionPrompt(currentSoul)

	resp, err := m.provider.SendMessage(ctx, &llm.Request{
		SystemPrompt: reflectionSystemPrompt,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens: 2048,
	})
	if err != nil {
		return fmt.Errorf("LLM reflection: %w", err)
	}

	// Extract text content from response.
	responseText := resp.Content
	if responseText == "" && len(resp.ContentBlocks) > 0 {
		for _, block := range resp.ContentBlocks {
			if block.Type == "text" {
				responseText += block.Text
			}
		}
	}

	// Parse structured JSON response into soul events.
	events, err := parseReflectionOutput(responseText)
	if err != nil {
		m.logger.Warn("failed to parse reflection output",
			slog.String("error", err.Error()),
			slog.String("raw_output", truncate(responseText, 500)),
		)
		return nil // Non-fatal: malformed reflection is discarded.
	}

	if len(events) == 0 {
		m.logger.Info("soul reflection produced no evolution events")
		return nil
	}

	// Apply each event.
	applied := 0
	for i := range events {
		events[i].EventType = EventReflectionCycle
		if err := m.Evolve(ctx, &events[i]); err != nil {
			m.logger.Warn("failed to apply reflection event",
				slog.String("title", events[i].Title),
				slog.String("error", err.Error()),
			)
			continue
		}
		applied++
	}

	m.logger.Info("soul reflection completed",
		slog.Int("events_produced", len(events)),
		slog.Int("events_applied", applied),
	)

	return nil
}

// reflectionEvent is the JSON schema expected from the LLM.
type reflectionEvent struct {
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

// parseReflectionOutput parses the LLM's JSON response into SoulEvents.
func parseReflectionOutput(raw string) ([]SoulEvent, error) {
	// Trim whitespace and try to find JSON array.
	raw = extractJSONArray(raw)
	if raw == "" {
		return nil, fmt.Errorf("no JSON array found in output")
	}

	var items []reflectionEvent
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("unmarshaling reflection output: %w", err)
	}

	// Validate and convert.
	validCategories := map[string]bool{
		"pattern": true, "philosophy": true, "reflection": true,
		"reasoning": true, "guardrail": true,
	}

	var events []SoulEvent
	for _, item := range items {
		if !validCategories[item.Category] {
			continue // Skip invalid categories.
		}
		if item.Title == "" || item.Description == "" {
			continue // Skip incomplete events.
		}
		// Reject attempts to modify principles.
		if item.Category == "principle" {
			continue
		}

		severity := EventSeverity(item.Severity)
		if severity != SeverityMinor && severity != SeverityNormal && severity != SeverityMajor {
			severity = SeverityMinor
		}

		evidence := item.Evidence
		if evidence == "" {
			evidence = "{}"
		}

		events = append(events, SoulEvent{
			Severity:    severity,
			Category:    item.Category,
			Title:       item.Title,
			Description: item.Description,
			Evidence:    evidence,
		})
	}

	return events, nil
}

// extractJSONArray finds the first JSON array in the string.
func extractJSONArray(s string) string {
	start := -1
	depth := 0
	for i, c := range s {
		if c == '[' {
			if start == -1 {
				start = i
			}
			depth++
		} else if c == ']' {
			depth--
			if depth == 0 && start != -1 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// truncate returns the first n characters of s.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
