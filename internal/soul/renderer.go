package soul

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RenderMarkdown produces a human-readable Markdown representation of the soul.
// This is a derived output for human review, not the source of truth.
func RenderMarkdown(s *Soul) string {
	var b strings.Builder

	b.WriteString("# Akili Soul\n\n")
	b.WriteString(fmt.Sprintf("> Version %d | Last updated: %s | Total events: %d\n\n",
		s.Version, s.LastUpdatedAt.Format(time.RFC3339), s.TotalEvents))

	b.WriteString("---\n\n")

	// Core principles.
	b.WriteString("## Core Principles (Immutable)\n\n")
	if len(s.CorePrinciples) > 0 {
		for _, p := range s.CorePrinciples {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", p.Name, p.Description))
		}
	} else {
		b.WriteString("_No principles seeded yet._\n")
	}

	// Learned patterns.
	if len(s.LearnedPatterns) > 0 {
		b.WriteString("\n---\n\n## Learned Patterns\n\n")
		// Group by category.
		grouped := make(map[string][]Pattern)
		for _, p := range s.LearnedPatterns {
			cat := p.Category
			if cat == "" {
				cat = "general"
			}
			grouped[cat] = append(grouped[cat], p)
		}
		for cat, patterns := range grouped {
			b.WriteString(fmt.Sprintf("### %s\n\n", cat))
			for _, p := range patterns {
				b.WriteString(fmt.Sprintf("- %s (confidence: %.0f%%, learned: %s)\n",
					p.Description, p.Confidence*100, p.LearnedAt.Format("2006-01-02")))
			}
			b.WriteString("\n")
		}
	}

	// Operational philosophy.
	if len(s.Philosophy) > 0 {
		b.WriteString("---\n\n## Operational Philosophy\n\n")
		for _, i := range s.Philosophy {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", i.Topic, i.Description))
		}
		b.WriteString("\n")
	}

	// Reflections.
	if len(s.Reflections) > 0 {
		b.WriteString("---\n\n## Reflections\n\n")
		for _, r := range s.Reflections {
			b.WriteString(fmt.Sprintf("### %s\n\n", r.Context))
			b.WriteString(fmt.Sprintf("- **Lesson**: %s\n", r.Lesson))
			if r.Impact != "" {
				b.WriteString(fmt.Sprintf("- **Impact**: %s\n", r.Impact))
			}
			b.WriteString(fmt.Sprintf("- _Learned: %s_\n\n", r.LearnedAt.Format("2006-01-02")))
		}
	}

	// Reasoning strategies.
	if len(s.ReasoningStrategies) > 0 {
		b.WriteString("---\n\n## Reasoning Strategies\n\n")
		for _, st := range s.ReasoningStrategies {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", st.Name, st.Description))
		}
		b.WriteString("\n")
	}

	// Self-imposed guardrails.
	if len(s.Guardrails) > 0 {
		b.WriteString("---\n\n## Self-Imposed Guardrails\n\n")
		for _, g := range s.Guardrails {
			b.WriteString(fmt.Sprintf("- **%s**: %s\n", g.Name, g.Description))
		}
		b.WriteString("\n")
	}

	// Footer.
	b.WriteString("---\n\n")
	b.WriteString("_This file is auto-generated from Akili's soul event log. Do not edit manually._\n")

	return b.String()
}

// WriteSoulFile writes the rendered SOUL.md to the given directory.
func WriteSoulFile(dir string, s *Soul) error {
	if s == nil {
		return nil
	}
	content := RenderMarkdown(s)
	path := filepath.Join(dir, "SOUL.md")
	return os.WriteFile(path, []byte(content), 0640)
}
