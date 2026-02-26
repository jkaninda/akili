// Package soul implements Akili's evolving operational identity.
// The soul is an event-sourced, persistent self-improvement layer that refines
// reasoning patterns, learned behaviors, and operational philosophy over time.
//
// Architecture:
//   - Database (soul_events table) is the append-only source of truth
//   - Soul struct is a materialized view derived by replaying events
//   - SOUL.md in the workspace is a human-readable rendered snapshot
//   - Evolution happens through structured SoulEvents, not random mutations
//
// Security constraint: the soul cannot override hard policy constraints.
// Core principles are immutable and seeded on first run.
package soul

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventType classifies what triggered a soul evolution event.
type EventType string

const (
	EventTaskCompletion EventType = "task_completion"  // After a workflow/task finishes.
	EventIncidentReview EventType = "incident_review"  // After incident handling.
	EventReflectionCycle EventType = "reflection_cycle" // Periodic LLM self-reflection.
	EventPatternLearned EventType = "pattern_learned"  // New operational pattern discovered.
	EventGuardrailUpdate EventType = "guardrail_update" // Refinement of self-imposed constraint.
	EventManualUpdate   EventType = "manual_update"    // Operator-initiated soul adjustment.
)

// EventSeverity indicates the importance/weight of a soul event.
type EventSeverity string

const (
	SeverityMinor  EventSeverity = "minor"  // Incremental learning.
	SeverityNormal EventSeverity = "normal" // Standard evolution.
	SeverityMajor  EventSeverity = "major"  // Significant shift in approach.
)

// SoulEvent is an immutable record of a single evolution step.
// The event log is append-only; the current soul state is derived
// by replaying all events in chronological order.
type SoulEvent struct {
	ID          uuid.UUID     // Primary key.
	OrgID       uuid.UUID     // Tenant scope.
	Version     int           // Monotonically increasing version number per org.
	EventType   EventType     // What triggered this event.
	Severity    EventSeverity // Importance level.
	Category    string        // Section: "principle", "pattern", "philosophy", "reflection", "reasoning", "guardrail".
	Title       string        // Human-readable summary (one line).
	Description string        // Detailed explanation of the evolution.
	Evidence    string        // JSON-encoded evidence (task IDs, metrics, etc.).
	PriorState  string        // Snapshot of affected section before change (for auditing).
	CreatedAt   time.Time
}

// Soul is the materialized current state, derived from the event log.
type Soul struct {
	Version             int       // Latest event version.
	LastUpdatedAt       time.Time // When the last event occurred.
	TotalEvents         int       // Total event count.
	CorePrinciples      []Principle
	LearnedPatterns     []Pattern
	Philosophy          []Insight
	Reflections         []Reflection
	ReasoningStrategies []Strategy
	Guardrails          []Guardrail
}

// Principle is an immutable core axiom, seeded on first run.
type Principle struct {
	Name        string
	Description string
}

// Pattern is a learned operational pattern from experience.
type Pattern struct {
	Category    string    // e.g., "incident_handling", "deployment", "diagnostics".
	Description string
	LearnedAt   time.Time
	EventID     uuid.UUID
	Confidence  float64 // 0.0-1.0.
}

// Insight is an evolving operational philosophy statement.
type Insight struct {
	Topic       string
	Description string
	LearnedAt   time.Time
	EventID     uuid.UUID
}

// Reflection is a structured takeaway from a past incident or review.
type Reflection struct {
	Context   string // What happened.
	Lesson    string // What was learned.
	Impact    string // How this changes future behavior.
	LearnedAt time.Time
	EventID   uuid.UUID
}

// Strategy is a reasoning improvement.
type Strategy struct {
	Name        string // e.g., "prefer_rollback_over_fix".
	Description string
	LearnedAt   time.Time
	EventID     uuid.UUID
}

// Guardrail is a self-imposed constraint discovered through experience.
type Guardrail struct {
	Name        string // e.g., "no_parallel_mutations".
	Description string
	LearnedAt   time.Time
	EventID     uuid.UUID
}

// SoulStore persists soul evolution events. Append-only.
type SoulStore interface {
	// AppendEvent persists a new soul event.
	AppendEvent(ctx context.Context, event *SoulEvent) error

	// ListEvents returns all events for an org in chronological order.
	ListEvents(ctx context.Context, orgID uuid.UUID) ([]SoulEvent, error)

	// ListEventsSince returns events after the given version.
	ListEventsSince(ctx context.Context, orgID uuid.UUID, afterVersion int) ([]SoulEvent, error)

	// LatestVersion returns the highest event version for the org.
	// Returns 0 if no events exist.
	LatestVersion(ctx context.Context, orgID uuid.UUID) (int, error)

	// CountEvents returns the total number of events for an org.
	CountEvents(ctx context.Context, orgID uuid.UUID) (int, error)
}

// Materialize builds a Soul from a chronological event log.
func Materialize(events []SoulEvent) *Soul {
	s := &Soul{}
	for i := range events {
		applyEvent(s, &events[i])
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		s.Version = last.Version
		s.LastUpdatedAt = last.CreatedAt
	}
	s.TotalEvents = len(events)
	return s
}

// applyEvent applies a single event to the soul state.
func applyEvent(s *Soul, e *SoulEvent) {
	s.Version = e.Version
	s.LastUpdatedAt = e.CreatedAt
	s.TotalEvents++

	switch e.Category {
	case "principle":
		s.CorePrinciples = append(s.CorePrinciples, Principle{
			Name:        e.Title,
			Description: e.Description,
		})
	case "pattern":
		s.LearnedPatterns = append(s.LearnedPatterns, Pattern{
			Category:    extractSubCategory(e.Evidence),
			Description: e.Description,
			LearnedAt:   e.CreatedAt,
			EventID:     e.ID,
			Confidence:  extractConfidence(e.Evidence),
		})
	case "philosophy":
		s.Philosophy = append(s.Philosophy, Insight{
			Topic:       e.Title,
			Description: e.Description,
			LearnedAt:   e.CreatedAt,
			EventID:     e.ID,
		})
	case "reflection":
		s.Reflections = append(s.Reflections, Reflection{
			Context:   e.Title,
			Lesson:    e.Description,
			Impact:    e.PriorState,
			LearnedAt: e.CreatedAt,
			EventID:   e.ID,
		})
	case "reasoning":
		s.ReasoningStrategies = append(s.ReasoningStrategies, Strategy{
			Name:        e.Title,
			Description: e.Description,
			LearnedAt:   e.CreatedAt,
			EventID:     e.ID,
		})
	case "guardrail":
		s.Guardrails = append(s.Guardrails, Guardrail{
			Name:        e.Title,
			Description: e.Description,
			LearnedAt:   e.CreatedAt,
			EventID:     e.ID,
		})
	}
}

// extractSubCategory reads a "category" field from JSON evidence.
func extractSubCategory(evidence string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(evidence), &data); err != nil {
		return ""
	}
	if cat, ok := data["category"].(string); ok {
		return cat
	}
	return ""
}

// extractConfidence reads a "confidence" field from JSON evidence. Defaults to 0.5.
func extractConfidence(evidence string) float64 {
	var data map[string]any
	if err := json.Unmarshal([]byte(evidence), &data); err != nil {
		return 0.5
	}
	if conf, ok := data["confidence"].(float64); ok {
		return conf
	}
	return 0.5
}
