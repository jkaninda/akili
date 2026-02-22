---
name: Network Diagnostics
agent_role: executor
category: networking
tools_required:
  - shell_exec
  - web_fetch
risk_level: medium
default_budget: 0.50
---

# Network Diagnostics

Diagnose network connectivity issues between services by testing DNS resolution, TCP connectivity, latency, packet loss, and routing paths.

## When to Use

- Services report connection timeouts to dependencies
- Elevated latency between specific service pairs
- DNS resolution failures
- After network infrastructure changes (firewall rules, load balancer config, VPC peering)

## Procedure

1. **DNS resolution** — Verify that all service hostnames resolve correctly. Check for stale DNS cache entries and compare against expected IPs.
2. **TCP connectivity** — Test TCP connectivity to dependent services on their expected ports. Identify connection refused vs timeout (firewall vs service down).
3. **Latency measurement** — Measure round-trip latency between the affected services. Compare against baseline. Check for asymmetric latency.
4. **Packet loss detection** — Run sustained connectivity tests to detect intermittent packet loss that causes retransmissions and elevated latency.
5. **Route tracing** — Trace the network path between affected services to identify where latency is introduced or packets are dropped.
6. **Firewall and security groups** — Verify that firewall rules, security groups, and network ACLs permit the required traffic. Check for recent rule changes.

## Expected Output

A network diagnostics report: DNS resolution status per hostname, TCP connectivity results per service:port, latency measurements with baseline comparison, packet loss percentage, route trace output, and any firewall rule mismatches.

## Escalation Criteria

- Complete connectivity loss between services: escalate to network/infrastructure team.
- Packet loss above 1%: investigate network hardware or provider issues.
- DNS resolution failure: check DNS infrastructure and zone configuration.
