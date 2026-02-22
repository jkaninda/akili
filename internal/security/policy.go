// Package security policy.go implements capability-based access control.
// PolicyEnforcer is orthogonal to RBAC — RBAC checks user permissions,
// policies restrict what an agent (or role) is allowed to touch.
//
// Deny-first evaluation: DeniedX checked first; if match, deny.
// Then AllowedX checked; if non-empty and no match, deny.
// Empty AllowedX = allow all (backward compatible).
package security

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// SecurityPolicy defines what an agent or role is allowed to do.
type SecurityPolicy struct {
	Name            string   `json:"name"`
	AllowedTools    []string `json:"allowed_tools"`
	DeniedTools     []string `json:"denied_tools"`
	AllowedPaths    []string `json:"allowed_paths"`
	DeniedPaths     []string `json:"denied_paths"`
	AllowedDomains  []string `json:"allowed_domains"`
	DeniedDomains   []string `json:"denied_domains"`
	AllowedCommands []string `json:"allowed_commands"`
	DeniedCommands  []string `json:"denied_commands"`
	MaxRiskLevel    string   `json:"max_risk_level"`
	RequireApproval []string `json:"require_approval"`
	AllowedSkills   []string `json:"allowed_skills"`
	DeniedSkills    []string `json:"denied_skills"`
}

// PolicyEnforcer validates tool execution against capability-based policies.
// Thread-safe for concurrent use.
type PolicyEnforcer struct {
	mu       sync.RWMutex
	policies map[string]*SecurityPolicy // policy name → policy
	bindings map[string]string          // agent ID or role → policy name
	logger   *slog.Logger
}

// NewPolicyEnforcer creates an enforcer from a list of policies and bindings.
func NewPolicyEnforcer(policies []SecurityPolicy, bindings map[string]string, logger *slog.Logger) *PolicyEnforcer {
	pm := make(map[string]*SecurityPolicy, len(policies))
	for i := range policies {
		p := policies[i]
		pm[p.Name] = &p
	}
	return &PolicyEnforcer{
		policies: pm,
		bindings: bindings,
		logger:   logger,
	}
}

// ResolvePolicy returns the policy bound to the given agent ID.
// Returns nil if no policy is bound (= no enforcement).
func (e *PolicyEnforcer) ResolvePolicy(agentID string) *SecurityPolicy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	policyName, ok := e.bindings[agentID]
	if !ok {
		return nil
	}
	return e.policies[policyName]
}

// CheckToolAllowed returns nil if the tool is allowed under the agent's policy.
func (e *PolicyEnforcer) CheckToolAllowed(_ context.Context, agentID string, toolName string) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil // No policy = no restriction.
	}
	return checkAllowDeny(toolName, policy.AllowedTools, policy.DeniedTools, "tool")
}

// CheckPathAllowed returns nil if filesystem access to the path is allowed.
func (e *PolicyEnforcer) CheckPathAllowed(_ context.Context, agentID string, path string) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil
	}
	return checkPrefixAllowDeny(path, policy.AllowedPaths, policy.DeniedPaths, "path")
}

// CheckDomainAllowed returns nil if network access to the domain is allowed.
func (e *PolicyEnforcer) CheckDomainAllowed(_ context.Context, agentID string, domain string) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil
	}
	return checkAllowDeny(strings.ToLower(domain), toLower(policy.AllowedDomains), toLower(policy.DeniedDomains), "domain")
}

// CheckCommandAllowed returns nil if the shell command is allowed.
func (e *PolicyEnforcer) CheckCommandAllowed(_ context.Context, agentID string, command string) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil
	}
	return checkPrefixAllowDeny(command, policy.AllowedCommands, policy.DeniedCommands, "command")
}

// CheckSkillAllowed validates skill execution against the policy.
func (e *PolicyEnforcer) CheckSkillAllowed(_ context.Context, agentID string, skillKey string) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil
	}
	return checkAllowDeny(skillKey, policy.AllowedSkills, policy.DeniedSkills, "skill")
}

// CheckRiskLevel returns nil if the risk level is within the policy's cap.
func (e *PolicyEnforcer) CheckRiskLevel(_ context.Context, agentID string, riskLevel RiskLevel) error {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return nil
	}
	if policy.MaxRiskLevel == "" {
		return nil // No cap.
	}
	maxRisk := ParseRiskLevel(policy.MaxRiskLevel)
	if riskLevel > maxRisk {
		return fmt.Errorf("%w: risk level %s exceeds policy %q cap of %s",
			ErrPermissionDenied, riskLevel, policy.Name, maxRisk)
	}
	return nil
}

// RequiresApproval returns true if the tool is on the policy's require_approval list.
func (e *PolicyEnforcer) RequiresApproval(agentID string, toolName string) bool {
	policy := e.ResolvePolicy(agentID)
	if policy == nil {
		return false
	}
	for _, t := range policy.RequireApproval {
		if t == toolName {
			return true
		}
	}
	return false
}

// checkAllowDeny implements deny-first, then allow-list logic for exact matches.
func checkAllowDeny(value string, allowed, denied []string, label string) error {
	// Deny list checked first.
	for _, d := range denied {
		if d == value {
			return fmt.Errorf("%w: %s %q is explicitly denied by policy", ErrPermissionDenied, label, value)
		}
	}
	// If allow list is non-empty, value must be in it.
	if len(allowed) > 0 {
		for _, a := range allowed {
			if a == value {
				return nil
			}
		}
		return fmt.Errorf("%w: %s %q is not in the policy allow list", ErrPermissionDenied, label, value)
	}
	return nil // Empty allow list = allow all.
}

// checkPrefixAllowDeny implements deny-first logic with prefix matching.
func checkPrefixAllowDeny(value string, allowed, denied []string, label string) error {
	// Deny list checked first (prefix match).
	for _, d := range denied {
		if strings.HasPrefix(value, d) {
			return fmt.Errorf("%w: %s %q matches denied prefix %q", ErrPermissionDenied, label, value, d)
		}
	}
	// If allow list is non-empty, value must match at least one prefix.
	if len(allowed) > 0 {
		for _, a := range allowed {
			if strings.HasPrefix(value, a) {
				return nil
			}
		}
		return fmt.Errorf("%w: %s %q does not match any allowed prefix", ErrPermissionDenied, label, value)
	}
	return nil
}

func toLower(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}
