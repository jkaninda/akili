---
name: Capacity Planning
agent_role: researcher
category: capacity
tools_required:
  - shell_exec
  - file_read
  - database_read
risk_level: low
default_budget: 0.75
---

# Capacity Planning

Analyze current resource utilization and growth trends to forecast capacity needs and recommend scaling actions before resources are exhausted.

## When to Use

- Monthly or quarterly capacity reviews
- When resource utilization exceeds 70% sustained
- Before anticipated traffic increases (product launches, seasonal peaks)
- After incidents caused by resource exhaustion

## Procedure

1. **Collect current utilization** — Gather CPU, memory, disk, and network metrics across all hosts and services. Include database connection pool usage and queue depths.
2. **Analyze trends** — Review utilization trends over the past 30/60/90 days. Identify linear or exponential growth patterns.
3. **Identify hotspots** — Find services or hosts approaching capacity limits (>70% sustained utilization).
4. **Forecast growth** — Project when current resources will be exhausted based on observed growth rates.
5. **Cost analysis** — Estimate the cost of scaling horizontally (add nodes) vs vertically (upgrade specs) for each hotspot.
6. **Recommend actions** — Prioritize scaling recommendations by urgency (time to exhaustion) and cost efficiency.

## Expected Output

A capacity report containing: current utilization summary per service/host, 30-day trend analysis, projected exhaustion dates, scaling recommendations with cost estimates, and priority ranking.
