---
name: Log Analysis
agent_role: researcher
category: incident-response
tools_required:
  - shell_exec
  - file_read
risk_level: low
default_budget: 0.50
---

# Log Analysis

Analyze application and system logs to identify error patterns, performance bottlenecks, and anomalous behavior.

## When to Use

- During incident investigation to identify root cause
- Periodic log review for proactive issue detection
- After deployments to verify expected behavior

## Procedure

1. **Locate log files** — Identify relevant log paths for the target service (application logs, system logs, access logs).
2. **Filter by timeframe** — Narrow logs to the relevant time window around the reported issue.
3. **Search for errors** — Grep for ERROR, FATAL, WARN, panic, and exception patterns. Count occurrences and group by type.
4. **Identify patterns** — Look for repeated error signatures, escalating frequency, or sudden onset correlating with a specific timestamp.
5. **Extract context** — For the top error patterns, extract surrounding log lines to understand the call chain and triggering conditions.
6. **Correlate with metrics** — Cross-reference error timestamps with CPU/memory/latency spikes to confirm causation.

## Expected Output

A report listing: top error patterns with counts, first occurrence timestamp, sample log entries with context, and correlation with system metrics.
