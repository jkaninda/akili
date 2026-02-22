---
name: Incident Triage
agent_role: researcher
category: incident-response
tools_required:
  - shell_exec
  - file_read
  - web_fetch
risk_level: medium
default_budget: 1.00
---

# Incident Triage

Perform initial incident triage by gathering system metrics, checking service health endpoints, reviewing recent logs, and correlating alerts to determine scope and severity.

## When to Use

- An alert fires indicating service degradation or outage
- Users report errors or elevated latency
- Monitoring dashboards show anomalous patterns

## Procedure

1. **Identify affected services** — Check health endpoints and recent deployment history to determine which services are impacted.
2. **Gather metrics** — Pull CPU, memory, disk, and network metrics from affected hosts. Check for resource exhaustion.
3. **Review logs** — Search application and system logs for error patterns, stack traces, and correlation IDs from the alert.
4. **Check recent changes** — Review recent deployments, config changes, and infrastructure modifications in the last 24 hours.
5. **Assess blast radius** — Determine if the issue is isolated to a single node/service or affects multiple components.
6. **Classify severity** — Based on user impact and scope, classify as SEV1 (critical), SEV2 (major), SEV3 (minor), or SEV4 (cosmetic).

## Expected Output

A structured incident summary containing: affected services, timeline of events, probable root cause hypothesis, severity classification, and recommended next steps.

## Escalation Criteria

- SEV1/SEV2: Immediately escalate to on-call engineer and notify incident channel.
- Data loss or security implications: Escalate to security team regardless of severity.
- Infrastructure-wide impact: Trigger the capacity planning runbook in parallel.
