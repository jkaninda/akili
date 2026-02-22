package orchestrator

import "github.com/prometheus/client_golang/prometheus"

// SkillMetrics holds Prometheus metrics for the skill intelligence layer.
// All metrics use the akili_skill_ namespace.
type SkillMetrics struct {
	SkillReliability   *prometheus.GaugeVec
	SkillAvgDuration   *prometheus.GaugeVec
	SkillAvgCost       *prometheus.GaugeVec
	SkillCompletions   *prometheus.CounterVec
	SkillMaturityLevel *prometheus.GaugeVec

	// Loader metrics.
	SkillDefsLoaded *prometheus.CounterVec
	SkillDefsErrors *prometheus.CounterVec
}

// NewSkillMetrics creates and registers skill metrics on the given registry.
// Returns nil if reg is nil.
func NewSkillMetrics(reg *prometheus.Registry) *SkillMetrics {
	if reg == nil {
		return nil
	}

	m := &SkillMetrics{
		SkillReliability: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "reliability",
			Help:      "Current reliability score by agent role and skill.",
		}, []string{"agent_role", "skill_key", "maturity"}),

		SkillAvgDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "avg_duration_ms",
			Help:      "Rolling average task duration in milliseconds.",
		}, []string{"agent_role", "skill_key"}),

		SkillAvgCost: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "avg_cost_usd",
			Help:      "Rolling average task cost in USD.",
		}, []string{"agent_role", "skill_key"}),

		SkillCompletions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "completions_total",
			Help:      "Total skill completions by status.",
		}, []string{"agent_role", "skill_key", "status"}),

		SkillMaturityLevel: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "maturity_level",
			Help:      "Skill maturity level (0=basic, 1=proven, 2=trusted, 3=optimized).",
		}, []string{"agent_role", "skill_key"}),

		SkillDefsLoaded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "definitions_loaded_total",
			Help:      "Total skill definitions loaded from Markdown files.",
		}, []string{"agent_role"}),

		SkillDefsErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "akili",
			Subsystem: "skill",
			Name:      "definitions_errors_total",
			Help:      "Total errors while loading skill definitions.",
		}, []string{"source"}),
	}

	reg.MustRegister(
		m.SkillReliability,
		m.SkillAvgDuration,
		m.SkillAvgCost,
		m.SkillCompletions,
		m.SkillMaturityLevel,
		m.SkillDefsLoaded,
		m.SkillDefsErrors,
	)

	return m
}
