---
name: Cost Optimization
agent_role: researcher
category: cost
tools_required:
  - shell_exec
  - web_fetch
  - file_read
risk_level: low
default_budget: 0.75
---

# Cost Optimization

Analyze infrastructure costs to identify waste, right-sizing opportunities, and optimization strategies that reduce spend without impacting reliability or performance.

## When to Use

- Monthly cost reviews
- When cloud spend exceeds budget thresholds
- After scaling events to verify cost efficiency
- Before budget planning cycles

## Procedure

1. **Inventory resources** — List all running instances, containers, storage volumes, and managed services with their specifications and costs.
2. **Identify idle resources** — Find instances with sustained CPU < 5%, unused storage volumes, unattached IPs, and orphaned snapshots.
3. **Right-sizing analysis** — Compare actual resource utilization against provisioned capacity. Identify over-provisioned instances that can be downsized.
4. **Reserved capacity opportunities** — For stable, long-running workloads, calculate savings from reserved instances or committed use discounts.
5. **Storage optimization** — Identify cold data that can be moved to cheaper storage tiers. Check for duplicate or unnecessary backups.
6. **Network cost analysis** — Review data transfer costs, identify cross-region traffic that could be avoided, and check CDN utilization.

## Expected Output

A cost optimization report containing: current monthly spend breakdown by service, identified waste with estimated savings, right-sizing recommendations, reserved capacity opportunities, and a prioritized action plan ranked by savings potential.
