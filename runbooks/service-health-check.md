---
name: Service Health Check
agent_role: executor
category: monitoring
tools_required:
  - shell_exec
  - web_fetch
risk_level: medium
default_budget: 0.50
---

# Service Health Check

Verify the health and availability of services by checking HTTP endpoints, process status, resource utilization, and dependency connectivity.

## When to Use

- Routine health verification after maintenance windows
- Pre/post deployment validation
- When alerts indicate potential service degradation

## Procedure

1. **Check HTTP health endpoints** — Send requests to `/health` and `/ready` endpoints. Verify 200 status and expected response body.
2. **Verify process status** — Confirm the service process is running, check uptime, and review restart counts.
3. **Check resource utilization** — Verify CPU, memory, and disk usage are within acceptable thresholds on the service host.
4. **Test dependencies** — Validate connectivity to databases, caches, message queues, and upstream services.
5. **Verify TLS certificates** — Check certificate validity and expiry for HTTPS endpoints.
6. **Review recent logs** — Check for error spikes or warning patterns in the last 15 minutes.

## Expected Output

A health report per service: endpoint status, response time, process uptime, resource usage percentages, dependency status, and any warnings or anomalies detected.

## Escalation Criteria

- Any health endpoint returning non-200: investigate immediately.
- Resource utilization above 85%: trigger capacity planning runbook.
- Certificate expiring within 7 days: trigger certificate check runbook.
