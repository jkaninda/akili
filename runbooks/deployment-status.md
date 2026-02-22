---
name: Deployment Status
agent_role: researcher
category: deployment
tools_required:
  - shell_exec
  - git_read
risk_level: low
default_budget: 0.30
---

# Deployment Status

Check the current deployment state of services by reviewing git history, running containers, and deployment metadata to provide a clear picture of what is deployed where.

## When to Use

- Before deploying a new release to confirm the current state
- During incident triage to determine what version is running
- For audit and compliance reporting

## Procedure

1. **Check running versions** — Query running containers or processes to identify deployed versions, image tags, and build metadata.
2. **Review git history** — Check recent commits, tags, and branches to identify the latest release and any pending changes.
3. **Compare environments** — If multiple environments exist (staging, production), compare deployed versions to identify drift.
4. **Check deployment pipeline** — Review CI/CD pipeline status for any in-progress or failed deployments.
5. **List pending changes** — Identify commits merged to main but not yet deployed to production.

## Expected Output

A deployment status report per service/environment: current version, last deployment timestamp, deployer identity, pending changes count, and pipeline status.
