---
name: Certificate Expiry Check
agent_role: executor
category: security
tools_required:
  - shell_exec
  - web_fetch
risk_level: medium
default_budget: 0.30
---

# Certificate Expiry Check

Verify TLS/SSL certificate validity and expiration dates across all public and internal endpoints to prevent outages caused by expired certificates.

## When to Use

- Weekly automated certificate audit
- When certificate expiry alerts fire
- Before and after certificate rotation
- During incident triage when TLS errors are reported

## Procedure

1. **Enumerate endpoints** — List all HTTPS endpoints (public-facing, internal services, APIs) that require valid TLS certificates.
2. **Check certificate details** — For each endpoint, retrieve the certificate chain and extract: issuer, subject, SANs, expiry date, and key algorithm.
3. **Validate chain** — Verify the full certificate chain is valid and trusted. Check for intermediate certificate issues.
4. **Calculate expiry windows** — Flag certificates expiring within 30 days (warning), 7 days (critical), or already expired (outage risk).
5. **Check automation** — Verify that auto-renewal (e.g., Let's Encrypt, cert-manager) is configured and functioning for applicable certificates.
6. **Document findings** — Record all certificates with their expiry dates and renewal method.

## Expected Output

A certificate inventory: endpoint, issuer, expiry date, days remaining, renewal method (manual/auto), and status (ok/warning/critical/expired).

## Escalation Criteria

- Certificate expiring within 7 days without auto-renewal: immediate manual renewal required.
- Expired certificate detected: emergency renewal and deployment needed.
- Certificate chain validation failure: investigate misconfiguration.
