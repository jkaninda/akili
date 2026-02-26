package soul

import (
	"fmt"
	"strconv"
	"strings"
)

// reflectionSystemPrompt instructs the LLM to perform self-assessment
// and produce structured evolution events.
const reflectionSystemPrompt = `You are the self-reflection module of Akili, a security-first autonomous AI operator.
Your role is to analyze Akili's current operational state and produce evolution events that improve
reasoning quality, operational efficiency, and security awareness.

You MUST NOT:
- Weaken any security guardrails
- Override immutable core principles
- Suggest actions that bypass policy constraints
- Produce speculative changes without evidence

Output ONLY a JSON array of evolution events:
[
  {
    "severity": "minor|normal|major",
    "category": "pattern|philosophy|reflection|reasoning|guardrail",
    "title": "Short title (one line)",
    "description": "Detailed description of the learning or improvement",
    "evidence": "{\"reason\": \"supporting data or observation\"}"
  }
]

Category definitions:
- pattern: A new operational pattern learned from repeated experience
- philosophy: An evolving approach to operations or decision-making
- reflection: A lesson learned from a past incident or failure
- reasoning: An improvement to the decision-making process
- guardrail: A new self-imposed behavioral constraint discovered through experience

If no meaningful evolution is warranted, return an empty array: []
Do not force evolution — only produce events when genuine learning has occurred.`

// RenderPromptContext produces a compact system prompt section from the soul.
// Only high-confidence patterns, active strategies, and recent reflections
// are included to bound token usage.
func RenderPromptContext(s *Soul) string {
	if s == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n## Soul (v")
	b.WriteString(strconv.Itoa(s.Version))
	b.WriteString(")\n")

	// Core principles are already in the base prompt; skip to avoid duplication.

	if len(s.LearnedPatterns) > 0 {
		var highConf []Pattern
		for _, p := range s.LearnedPatterns {
			if p.Confidence >= 0.7 {
				highConf = append(highConf, p)
			}
		}
		if len(highConf) > 0 {
			b.WriteString("\n### Learned Patterns\n")
			for _, p := range highConf {
				b.WriteString("- ")
				b.WriteString(p.Description)
				b.WriteString("\n")
			}
		}
	}

	if len(s.ReasoningStrategies) > 0 {
		b.WriteString("\n### Reasoning Strategies\n")
		for _, st := range s.ReasoningStrategies {
			b.WriteString("- **")
			b.WriteString(st.Name)
			b.WriteString("**: ")
			b.WriteString(st.Description)
			b.WriteString("\n")
		}
	}

	if len(s.Guardrails) > 0 {
		b.WriteString("\n### Self-Imposed Guardrails\n")
		for _, g := range s.Guardrails {
			b.WriteString("- ")
			b.WriteString(g.Name)
			b.WriteString(": ")
			b.WriteString(g.Description)
			b.WriteString("\n")
		}
	}

	if len(s.Philosophy) > 0 {
		b.WriteString("\n### Operational Philosophy\n")
		for _, i := range s.Philosophy {
			b.WriteString("- **")
			b.WriteString(i.Topic)
			b.WriteString("**: ")
			b.WriteString(i.Description)
			b.WriteString("\n")
		}
	}

	// Include only last 3 reflections for recency.
	if len(s.Reflections) > 0 {
		b.WriteString("\n### Recent Reflections\n")
		start := len(s.Reflections) - 3
		if start < 0 {
			start = 0
		}
		for _, r := range s.Reflections[start:] {
			b.WriteString("- ")
			b.WriteString(r.Lesson)
			b.WriteString("\n")
		}
	}

	return b.String()
}

// BuildReflectionPrompt builds the user-message input for the LLM reflection cycle.
func BuildReflectionPrompt(s *Soul) string {
	var b strings.Builder

	b.WriteString("Perform a self-reflection on Akili's current operational state.\n\n")
	b.WriteString(fmt.Sprintf("Current soul version: %d\n", s.Version))
	b.WriteString(fmt.Sprintf("Total events: %d\n", s.TotalEvents))
	b.WriteString(fmt.Sprintf("Patterns: %d | Reflections: %d | Strategies: %d | Guardrails: %d\n\n",
		len(s.LearnedPatterns), len(s.Reflections), len(s.ReasoningStrategies), len(s.Guardrails)))

	if len(s.CorePrinciples) > 0 {
		b.WriteString("## Core Principles (immutable, cannot be changed)\n")
		for _, p := range s.CorePrinciples {
			b.WriteString(fmt.Sprintf("- %s: %s\n", p.Name, p.Description))
		}
		b.WriteString("\n")
	}

	if len(s.LearnedPatterns) > 0 {
		b.WriteString("## Current Learned Patterns\n")
		for _, p := range s.LearnedPatterns {
			b.WriteString(fmt.Sprintf("- [%s] %s (confidence: %.0f%%)\n", p.Category, p.Description, p.Confidence*100))
		}
		b.WriteString("\n")
	}

	if len(s.ReasoningStrategies) > 0 {
		b.WriteString("## Current Reasoning Strategies\n")
		for _, st := range s.ReasoningStrategies {
			b.WriteString(fmt.Sprintf("- %s: %s\n", st.Name, st.Description))
		}
		b.WriteString("\n")
	}

	if len(s.Guardrails) > 0 {
		b.WriteString("## Current Self-Imposed Guardrails\n")
		for _, g := range s.Guardrails {
			b.WriteString(fmt.Sprintf("- %s: %s\n", g.Name, g.Description))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Task\n")
	b.WriteString("Review the current soul state. Identify:\n")
	b.WriteString("1. New patterns that should be codified from operational experience\n")
	b.WriteString("2. Reasoning strategies that could be refined or added\n")
	b.WriteString("3. New guardrails needed based on observed risks\n")
	b.WriteString("4. Philosophy updates reflecting operational maturity\n")
	b.WriteString("5. Reflections from recent operational patterns\n\n")
	b.WriteString("Only produce events when genuine learning has occurred. Do not force evolution.\n")

	return b.String()
}
