---
name: Deployment Rollback
agent_role: executor
category: deployment
tools_required:
  - shell_exec
  - git_read
  - git_write
risk_level: high
default_budget: 1.00
---

# Deployment Rollback

Safely revert a deployment to the previous known-good version when the current release causes service degradation, errors, or outages.

## When to Use

- Post-deployment monitoring detects elevated error rates or latency
- Users report regressions introduced by the latest release
- Canary deployment fails health checks
- SEV1/SEV2 incident traced to a recent deployment

## Procedure

1. **Confirm rollback decision** — Verify that the issue is deployment-related (not infrastructure or upstream dependency). Check git log for recent commits and deployment timestamps.
2. **Identify target version** — Determine the last known-good commit or tag to roll back to using `git log` and deployment history.
3. **Notify stakeholders** — Alert the team that a rollback is in progress. Document the reason and target version.
4. **Execute rollback** — Revert to the target version using `git revert` for the problematic commits. Avoid force-pushing; create a new forward commit.
5. **Validate rollback** — Run health checks against the rolled-back version. Verify error rates return to baseline.
6. **Post-rollback verification** — Monitor for 15 minutes to confirm stability. Check that no data migration conflicts exist.

## Expected Output

A rollback summary containing: the reverted commit(s), target version, health check results post-rollback, and confirmation that error rates returned to baseline.

## Escalation Criteria

- Rollback fails or introduces new errors: escalate to senior engineer immediately.
- Database migrations were applied that cannot be reversed: escalate to database team.
- Rollback does not resolve the issue: the problem may not be deployment-related; trigger incident triage.
