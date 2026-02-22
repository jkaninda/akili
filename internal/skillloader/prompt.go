package skillloader

import (
	"fmt"
	"strings"
)

// RenderSkillSummary creates a text description of skills for the system prompt.
// Returns an empty string if no definitions are provided.
func RenderSkillSummary(defs []SkillDefinition) string {
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("You have the following specialized skills available:\n\n")
	for _, def := range defs {
		sb.WriteString(fmt.Sprintf("- **%s** (%s, risk: %s): %s\n",
			def.Name, def.Category, def.RiskLevel,
			firstSentence(def.Description)))
		sb.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(def.ToolsRequired, ", ")))
	}
	return sb.String()
}

// SkillPerformance holds runtime metrics for a skill, used to enrich the prompt.
type SkillPerformance struct {
	SkillKey         string
	ReliabilityScore float64 // 0.0–1.0
	AvgDurationMS    float64
	MaturityLevel    string // "novice", "learning", "competent", "proficient", "expert"
	TotalExecutions  int
}

// RenderSkillSummaryWithPerformance creates a text description enriched with performance data.
// Falls back to the basic summary for skills without performance data.
func RenderSkillSummaryWithPerformance(defs []SkillDefinition, perf map[string]SkillPerformance) string {
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("You have the following specialized skills available:\n\n")
	for _, def := range defs {
		sb.WriteString(fmt.Sprintf("- **%s** (%s, risk: %s): %s\n",
			def.Name, def.Category, def.RiskLevel,
			firstSentence(def.Description)))
		sb.WriteString(fmt.Sprintf("  Tools: %s\n", strings.Join(def.ToolsRequired, ", ")))

		if p, ok := perf[def.SkillKey]; ok && p.TotalExecutions >= 10 {
			sb.WriteString(fmt.Sprintf("  Performance: %.0f%% reliability, avg %.0fms, maturity: %s\n",
				p.ReliabilityScore*100, p.AvgDurationMS, p.MaturityLevel))
		}
	}
	return sb.String()
}

// firstSentence returns the first sentence from a string (up to the first period + space or newline).
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, ". "); idx >= 0 {
		return s[:idx+1]
	}
	if idx := strings.Index(s, ".\n"); idx >= 0 {
		return s[:idx+1]
	}
	// If no sentence boundary, take up to 200 chars.
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
