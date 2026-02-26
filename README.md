# Akili

**Security-First Autonomous AI Operator System for DevOps, SRE, and Platform Engineering**

Akili is a production-grade autonomous AI operator system that automates incident triage, runbook execution, infrastructure operations, and cost optimization with strict security boundaries enforced at every step.

Akili is moving beyond the “AI assistant” category; it’s shaping up to be a policy-enforced AI runtime built specifically for DevOps, SRE, and platform teams.

Akili is built on one principle:

AI should not just be powerful, it should be controlled, auditable, and production-safe.

> We’re moving from AI as tools to AI as trusted operators, and trust starts with security.

<p align="center">
  <img src="https://raw.githubusercontent.com/jkaninda/akili/main/logo.png" width="300" alt="Akili logo">
</p>

**Core principle: Default Deny. Explicit Allow. Always Auditable.**

[![Test](https://github.com/jkaninda/akili/actions/workflows/test.yml/badge.svg)](https://github.com/jkaninda/akili/actions/workflows/test.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/jkaninda/akili)](https://goreportcard.com/report/github.com/jkaninda/akili)
[![Go](https://img.shields.io/github/go-mod/go-version/jkaninda/akili)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/jkaninda/akili.svg)](https://pkg.go.dev/github.com/jkaninda/akili)
[![GitHub Release](https://img.shields.io/github/v/release/jkaninda/akili)](https://github.com/jkaninda/akili/releases)
![Docker Image Size (latest by date)](https://img.shields.io/docker/image-size/jkaninda/akili?style=flat-square)
![Docker Pulls](https://img.shields.io/docker/pulls/jkaninda/akili?style=flat-square)

## Architecture

<img src="https://raw.githubusercontent.com/jkaninda/akili/main/architecture.png" width="912" alt="Akili architecture">

## Features

- **RBAC with Default-Deny** — Actions require explicit permission grants. Four risk levels (Low, Medium, High, Critical) control approval requirements.
- **Sandboxed Execution** — Every external command runs in an isolated sandbox with CPU, memory, and timeout limits. Supports process-based and Docker-based isolation.
- **Approval Workflows** — High-risk actions pause and require human approval before execution. Approval results are persisted back into conversation history to prevent re-request loops. Integrates with CLI, Slack, Telegram, and Signal.
- **100% Audit Trail** — Every action (permitted, denied, or pending) is logged to an append-only audit log. No exceptions.
- **Budget Enforcement** — Per-user cost limits with atomic reservation. Execution is blocked when budget is exhausted.
- **Multi-Gateway** — Interact via CLI, HTTP API, Slack, Telegram, or Signal. Each gateway enforces identity, rate limiting, and input validation.
- **Infrastructure Memory** — Built-in infrastructure node registry with credential isolation. The agent discovers nodes by name/alias and executes commands remotely — raw credentials never reach the LLM.
- **Secret Provider Chain** — Pluggable credential resolution (environment variables, with extensible support for HashiCorp Vault, AWS Secrets Manager). Secrets are resolved at the tool execution layer, never persisted in conversation history.
- **Rate Limiting** — Per-user token bucket rate limiter with configurable requests-per-minute and burst size. One user cannot exhaust another's quota.
- **Multi-LLM Provider Support** — Switch between Anthropic Claude, OpenAI GPT, Google Gemini, and Ollama (local models) with a single config change. Each provider supports model selection and custom base URLs.
- **LLM Provider Fallback Chain** — Configure multiple providers in priority order. When the primary fails, subsequent providers are tried automatically. All fallback attempts are logged for observability.
- **Persistent Conversation Memory** — Conversation history is persisted per user session (SQLite or PostgreSQL). Context is resumed across messages. Role sanitization prevents prompt injection persistence.
- **Conversation Summarization** — When conversation history approaches the configured maximum, older messages are automatically summarized by the LLM to preserve key context while reducing token usage. The most recent 40% of messages are kept intact.
- **Tool Result Caching** — TTL-based cache (60s default) for read-only tool results prevents redundant executions in agentic loops. Cache keys are deterministic SHA-256 hashes of tool name and parameters.
- **Smart Auto-Approval** — Learns from manual approval patterns. After an identical operation is manually approved N times within a configurable window, future identical requests are auto-approved. Per-user hourly caps, explicit tool allowlists, and full audit logging ensure safety.
- **ReAct Planning** — Complex tasks automatically trigger a plan-then-execute workflow. The agent creates structured execution plans (3-7 steps) before taking action, improving reliability on multi-step investigations and operations.
- **Structured Incident Reports** — When incident-related context is detected (keywords like "outage", "P1", "down"), the agent automatically produces structured reports with severity, status, timeline, impact, root cause, remediation, and action items.
- **Dynamic Runbook Injection** — Runbook descriptions are matched against the current conversation context using keyword scoring. Relevant runbooks are injected into the agent's system prompt, guiding the LLM toward established operational procedures.
- **Multi-Agent Orchestration** — DAG-based workflow engine decomposes complex goals into parallel and sequential tasks executed by specialized agent roles.
- **Skill Intelligence** — Tracks agent performance per skill, learns reliability metrics, and uses weighted scoring for task scheduling. Skills are defined as Markdown files with YAML frontmatter.
- **Heartbeat Tasks** — Periodic agent-executed tasks defined in Markdown files with YAML frontmatter. Two execution modes: quick (60s, direct response) and long (5m, async with tool use). File-based definition with SHA-256 change detection, per-task cron expressions, budget limits, and automatic notification dispatch. Results stored in DB with append-only history.
- **Cron Scheduler** — Distributed-safe recurring job execution with `SELECT FOR UPDATE SKIP LOCKED`. Missed job recovery on startup. Jobs can be created conversationally through any gateway. Optional failure notifications through the notification dispatcher.
- **Alerting & Monitoring** — Create alert rules that monitor HTTP endpoints on a cron schedule. When conditions change (status ok → alert), notifications are dispatched through configured channels with cooldown suppression. Alert rules, history, and channels are managed via REST API or conversationally through any gateway.
- **Multi-Channel Notifications** — Send alerts and ad-hoc messages through Telegram, Slack, WhatsApp, Signal, Email (SMTP with TLS), and HTTP webhooks. Each channel has independent configuration with credential isolation via `CredentialRef`. SSRF protection on webhook URLs. Every notification attempt is audit-logged.
- **MCP Integration** — Connect external MCP servers to extend agent capabilities without writing Go code. Tools are auto-discovered and flow through the same security pipeline.
- **Observability** — Prometheus metrics, OpenTelemetry tracing, health checks, and anomaly detection built in.
- **Zero-Config Storage** — SQLite by default with automatic workspace directory setup (`~/.akili/`). No external database required for single-node deployments. PostgreSQL available for production multi-tenant setups.
- **Dual Runtime Mode** — Run as a gateway (HTTP/CLI/Slack/Telegram/Signal) or as a remote agent. Agents connect via WebSocket (recommended) for real-time task dispatch, or poll the database (legacy) for pending tasks.
- **WebSocket Agent Protocol** — Gateway ↔ Agent communication via WebSocket with JSON envelope messaging, heartbeats, progress updates, and automatic reconnection. Agents register skills, receive task assignments, and report results in real-time.
- **Capability-Based Policies** — Fine-grained access control beyond RBAC. Define allowlists/denylists for tools, filesystem paths, network domains, shell commands, and skills. Policies are bound to specific agents or roles.
- **Soul Evolution** — Optional event-sourced self-improvement layer. The agent learns operational patterns, refines reasoning strategies, and conducts periodic reflections — all constrained by immutable security principles.
- **Token Optimization** — Lazy tool loading, runbook match scoring, and configurable skill summary modes reduce token usage per LLM call without sacrificing capability.
- **Query Command** — One-shot CLI for sending messages to the gateway with SSE streaming, autonomous mode, multi-turn conversation support, and structured exit codes.

## Tool Execution Flow

Every tool call — native or MCP — passes through the full security pipeline before execution:

```mermaid
sequenceDiagram
    participant U as User
    participant G as Gateway
    participant A as Agent/LLM
    participant S as Security
    participant T as Tool
    participant SB as Sandbox

    U->>G: Send message
    G->>A: Process(userID, message)
    A->>A: LLM decides tool call
    A->>S: CheckPermission(userID, action)
    alt Permission Denied
        S-->>A: ErrPermissionDenied
        A-->>G: Denied
    end
    A->>S: RequireApproval(userID, action)
    alt Approval Required
        S-->>A: ErrApprovalRequired
        A-->>G: Pending approval
        U->>G: Approve
        G->>A: ResumeWithApproval
        A->>A: Update conversation history
    end
    A->>S: ReserveBudget(userID, cost)
    A->>S: LogAction(intent)
    A->>T: Execute(params)
    T->>SB: Run in sandbox
    SB-->>T: Result
    T-->>A: Output
    A->>S: LogAction(success)
    A->>S: RecordCost(userID, cost)
    A-->>G: Response
    G-->>U: Result
```

## Built-in Tools

| Tool | Name | Risk Level | Description |
|------|------|------------|-------------|
| Shell | `shell_exec` | High | Execute commands in a sandboxed environment |
| File Read | `file_read` | Low | Read files within allowed paths (symlink-safe) |
| File Write | `file_write` | Medium | Write files within allowed paths |
| Web | `web_fetch` | Low | HTTP requests with SSRF protection and domain allowlist |
| Git Read | `git_read` | Low | Read-only git operations (log, diff, status, show, branch) |
| Git Write | `git_write` | High | Local git write operations (add, commit, checkout, branch, tag, merge, rebase) |
| Code | `code_exec` | High | Execute Python or shell scripts in sandbox |
| Database | `database_read` | Low | Read-only SQL queries against PostgreSQL (SELECT, EXPLAIN, SHOW) |
| Cron Job | `create_cronjob` | Medium | Create scheduled jobs conversationally (requires scheduler) |
| Create Alert | `create_alert` | High | Create monitoring alert rules conversationally (requires alerting) |
| Send Notification | `send_notification` | Medium | Send one-off notifications to configured channels |
| Infra Lookup | `infra_lookup` | Low | Resolve infrastructure nodes by name, alias, or ID |
| Infra Exec | `infra_exec` | Critical | Execute commands on remote infrastructure nodes via MCP |
| Infra Manage | `infra_manage` | High | Add, update, or remove infrastructure nodes from the registry |
| Browser | `browser` | Medium | Web navigation with content extraction (text, links, CSS selectors) and SSRF protection |
| MCP | `mcp__<server>__<tool>` | Configurable | Tools from external MCP servers |

Every tool declares its required security action and risk level. The orchestrator enforces RBAC, approval, and budget checks before any tool executes.

## Links

- **Source Code**: [akili](https://github.com/jkaninda/akili)
- **Docker Image**: [jkaninda/akili](https://hub.docker.com/r/jkaninda/akili)
- **Akili Runtime**: [jkaninda/akili-runtime](https://hub.docker.com/r/jkaninda/akili-runtime)
- **Examples**: [examples/](examples/) — Docker Compose deployment examples (Ollama, monitoring, etc.)

## Quick Start

### Prerequisites

- Go 1.26+ (for building from source)
- Docker and Docker Compose (for containerized deployment)
- An LLM API key (Anthropic, OpenAI, or Google Gemini — or a local Ollama instance)

### Build from Source

```bash
go build -o akili ./cmd/akili
```

Or using Make:

```bash
make build
```

### Version

```bash
./akili version
# akili v1.0.0 (commit: abc1234, built: 2026-02-17)
```

Build with version info via ldflags:

```bash
go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%d)" -o akili ./cmd/akili
```

### Build with Docker

Build the application Docker image:

```bash
make docker-build
```

Build the sandbox runtime image (used for Docker-based sandboxed execution):

```bash
docker build -t akili-runtime:latest -f docker/Dockerfile.runtime .
```

Or using Make:

```bash
make docker-runtime
```

### Docker Compose

The project includes a `docker-compose.yml` that starts Akili with a PostgreSQL database.

**1. Create a `.env` file** with your LLM API key:

```bash
ANTHROPIC_API_KEY=sk-ant-...
```

**2. Start the stack:**

```bash
docker compose up -d
```

Or using Make:

```bash
make up
```

**3. Verify it's running:**

```bash
curl http://localhost:8080/healthz
```

**Available Make targets for Docker Compose:**

| Target | Description |
|--------|-------------|
| `make up` | Start the Docker Compose stack |
| `make down` | Stop the Docker Compose stack |
| `make restart` | Restart the Akili container |
| `make logs` | Tail Docker Compose logs |
| `make ps` | Show running containers |
| `make dev` | Build image and start the full stack |
| `make dev-down` | Stop stack and remove volumes |
| `make dev-logs` | Tail Akili container logs |
| `make dev-restart` | Rebuild image and restart Akili container |

### Generate Configuration

Run the interactive setup wizard:

```bash
# For gateway mode (default):
./akili onboarding gateway

# For agent mode:
./akili onboarding agent

# Specify output path:
./akili onboarding gateway --output /etc/akili/config.json
```

The onboarding wizard prompts for LLM provider selection, security settings, sandbox type, gateway configuration, storage backend (SQLite default or PostgreSQL), and agent connectivity (WebSocket or DB polling).

### Set API Key

```bash
# Anthropic (default)
export ANTHROPIC_API_KEY=sk-ant-...

# Or OpenAI
export OPENAI_API_KEY=sk-...

# Or Google Gemini
export GEMINI_API_KEY=...

# Or Ollama (no API key needed, just a running instance)
```

### Run

```bash
# Gateway mode (default — starts CLI, HTTP, Slack, Telegram as configured):
# Uses SQLite storage by default — no database setup required.
./akili --config configs/akili.json

# Or explicitly:
./akili gateway --config configs/akili.json

# Override HTTP listen port:
./akili gateway --config configs/akili.json --port :9090

# Agent mode (WebSocket — recommended, no database required on agent):
./akili agent --config configs/akili.json --gateway-url ws://gateway-host:8080/ws/agents

# Agent mode (legacy DB polling, requires PostgreSQL):
./akili agent --config configs/akili.json --agent-id worker-1 --concurrency 5 --poll-interval 3
```

### Query Command

Send one-shot messages to the Akili gateway from the command line:

```bash
# Basic query
./akili query -m "check if nginx is running" --api-key my-key

# Stream response in real-time (SSE)
./akili query -m "analyze the latest logs" --stream

# Autonomous mode (bypass approval workflows; RBAC, budget, and audit still enforced)
./akili query -m "restart the failing pods" --autonomous

# Multi-turn conversation
./akili query -m "what happened yesterday?" --conversation-id <uuid-from-previous-response>

# Custom timeout
./akili query -m "run a full cost audit" --timeout 600
```

| Flag | Default | Description |
|------|---------|-------------|
| `-m` / `--message` | — | Message to send (required) |
| `--api-key` | `AKILI_API_KEY` env | API key for authentication |
| `--gateway-url` | `AKILI_GATEWAY_URL` env | Gateway HTTP URL (default `http://localhost:8080`) |
| `--stream` | `false` | Enable SSE streaming response |
| `--autonomous` | `false` | Bypass approval workflows |
| `--conversation-id` | — | Resume a previous conversation |
| `--timeout` | `300` | Request timeout in seconds |

**Exit codes:** `0` success, `1` execution failure, `2` policy denied / approval required, `3` gateway unavailable.

## Persistent Conversation Memory

Akili persists conversation history per user session so the agent retains context across messages:

```mermaid
graph LR
    subgraph Memory
        LOAD[Load History] --> PROCESS[Process Message]
        PROCESS --> PERSIST[Persist New Messages]
    end

    LOAD --> SQLITE2[(SQLite)]
    LOAD --> PG2[(PostgreSQL)]
    PERSIST --> SQLITE2
    PERSIST --> PG2
```

- **Per-user isolation** — Each conversation is tied to a user ID. Cross-user access is blocked.
- **Gateway integration** — CLI uses a persistent session ID. HTTP supports client-provided conversation IDs for session continuity. Slack and Telegram derive deterministic IDs from user/channel pairs.
- **Context window management** — History is truncated to the configured maximum to stay within LLM context limits.
- **Role sanitization** — Only `user` and `assistant` roles are persisted. System messages and unknown roles are normalized to prevent prompt injection persistence.
- **Approval result persistence** — When a tool is approved and executed, the pending placeholder in history is replaced with the actual result, preventing the LLM from re-requesting completed actions.

### Configuration

```json
{
  "memory": {
    "enabled": true,
    "max_history_messages": 100,
    "max_message_bytes": 32768
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable persistent conversation memory |
| `max_history_messages` | `100` | Maximum messages loaded per conversation |
| `max_message_bytes` | `32768` | Maximum size of a single message (32 KB) |

## Conversation Summarization

When conversation history grows long, Akili automatically summarizes older messages to preserve context while staying within LLM token limits.

- **Automatic trigger** — Summarization activates when history reaches 80% of `max_history_messages`
- **Context preservation** — The most recent 40% of messages are kept intact; older messages are compressed into a summary
- **LLM-powered** — The summary is generated by the configured LLM provider, preserving key facts, decisions, tool outputs, and errors
- **Graceful fallback** — If summarization fails, standard truncation is applied

### Configuration

```json
{
  "memory": {
    "enabled": true,
    "max_history_messages": 100,
    "max_message_bytes": 32768
  }
}
```

Summarization is enabled automatically when `memory.enabled` is `true`. The summarization threshold is `0.8 * max_history_messages`.

## Tool Result Caching

Read-only tool results are cached with a configurable TTL to prevent redundant executions in agentic loops where the LLM repeatedly calls the same tool with identical parameters.

- **Deterministic keys** — Cache keys are SHA-256 hashes of tool name + serialized parameters
- **TTL-based expiry** — Cached entries automatically expire (default 60s)
- **Read-only tools only** — Only tools explicitly marked as cacheable are cached (e.g., `file_read`, `web_fetch`, `git_read`, `database_read`, `infra_lookup`)
- **Thread-safe** — Concurrent access is safe across goroutines

The cache is built into the agent orchestrator and requires no additional configuration. Cache checks occur after parameter validation but before the security pipeline, minimizing latency for repeated queries.

## Multi-Agent Orchestration

When the orchestrator is enabled, Akili supports multi-agent workflows:

```mermaid
graph TD
    subgraph Workflow
        GOAL[Goal: Investigate latency spike]
        PLAN[Planner Agent]
        T1[Task 1: Check metrics]
        T2[Task 2: Analyze logs]
        T3[Task 3: Review deployments]
        T4[Task 4: Synthesize findings]
    end

    GOAL --> PLAN
    PLAN --> T1 & T2 & T3
    T1 & T2 & T3 --> T4

    style T1 fill:#4CAF50,color:#fff
    style T2 fill:#4CAF50,color:#fff
    style T3 fill:#4CAF50,color:#fff
    style T4 fill:#2196F3,color:#fff
```

- **DAG-based execution** — Tasks are organized in directed acyclic graphs with sequential and parallel execution modes
- **Specialized roles** — Orchestrator, planner, researcher, executor, compliance agents
- **Budget scoping** — Each workflow has its own budget limit
- **Depth and task limits** — Prevents infinite loops and runaway workflows
- **Per-task timeouts** — Configurable timeout per task execution (default 300s)
- **Skill learning** — Tracks success rates, execution duration, cost per skill, and uses weighted scoring for task assignment

### Workflows API

```bash
# Submit a workflow
curl -X POST http://localhost:8080/v1/workflows \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"goal": "Investigate why API latency spiked", "budget_limit_usd": 2.0}'

# Poll for status
curl http://localhost:8080/v1/workflows/{id} \
  -H "Authorization: Bearer YOUR_API_KEY"

# List workflow tasks
curl http://localhost:8080/v1/workflows/{id}/tasks \
  -H "Authorization: Bearer YOUR_API_KEY"

# Cancel a workflow
curl -X POST http://localhost:8080/v1/workflows/{id}/cancel \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### Agent Mode

Agents are headless workers that execute tasks assigned by the gateway. Two connectivity modes are available:

#### WebSocket Mode (Recommended)

Agents connect to the gateway via WebSocket for real-time task dispatch. No database required on the agent side.

```bash
./akili agent --config configs/akili-agent.json --gateway-url ws://gateway:8080/ws/agents
```

- Real-time task assignment (no polling delay)
- Heartbeat-based health monitoring
- Automatic reconnection with exponential backoff
- Progress updates streamed back to the gateway
- Skill-based routing (agents register their capabilities)

#### Legacy DB Polling Mode

Agents poll PostgreSQL for pending tasks. Requires shared database access between gateway and agents.

```bash
./akili agent --config configs/akili-agent.json --concurrency 5
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent-id` | `hostname-pid` | Override agent identifier |
| `--concurrency` | `5` | Maximum concurrent tasks |
| `--poll-interval` | `2` | Poll interval in seconds (legacy mode) |
| `--gateway-url` | — | WebSocket gateway URL (enables WebSocket mode) |

Agents report heartbeats, support draining, and stale tasks are automatically reassigned.

### Skill Definitions

Define agent skills as Markdown files with YAML frontmatter:

```markdown
---
name: log-analysis
category: incident-response
tools_required: [shell_exec, file_read]
risk_level: medium
default_budget: 0.50
---

# Log Analysis

Analyze application logs to identify error patterns...
```

Place skill files in a directory and configure `orchestrator.skills_dir` in your config. Skills are validated against the tool registry, seeded into the database at startup, and their descriptions are injected into the agent's system prompt.

## LLM Provider Fallback Chain

Configure multiple LLM providers with automatic failover. When the primary provider fails, subsequent providers in the fallback chain are tried in order.

```json
{
  "providers": {
    "default": "anthropic",
    "fallback": ["openai", "gemini", "ollama"],
    "anthropic": { "model": "claude-sonnet-4-5-20250929" },
    "openai": { "model": "gpt-4o" },
    "gemini": { "model": "gemini-2.0-flash" },
    "ollama": { "model": "llama3.1", "base_url": "http://localhost:11434" }
  }
}
```

Or in YAML:

```yaml
providers:
  default: anthropic
  fallback:
    - openai
    - gemini
    - ollama
  anthropic:
    model: claude-sonnet-4-5-20250929
  openai:
    model: gpt-4o
  gemini:
    model: gemini-2.0-flash
  ollama:
    model: llama3.1
    base_url: http://localhost:11434
```

| Field | Default | Description |
|-------|---------|-------------|
| `default` | `"anthropic"` | Primary LLM provider |
| `fallback` | `[]` | Ordered list of fallback providers tried on failure |
| `anthropic.model` | `"claude-sonnet-4-5-20250929"` | Anthropic model ID |
| `openai.model` | `"gpt-4o"` | OpenAI model ID |
| `gemini.model` | `"gemini-2.0-flash"` | Google Gemini model ID |
| `ollama.model` | `"llama3.1"` | Ollama model name |
| `ollama.base_url` | `"http://localhost:11434"` | Ollama API endpoint |

All fallback attempts are logged for observability. The first successful response is returned.

## Smart Auto-Approval

Auto-approval learns from manual approval patterns to reduce human intervention for trusted, repetitive operations — without compromising security.

```json
{
  "approval": {
    "ttl_seconds": 300,
    "auto_approval": {
      "enabled": true,
      "max_auto_approvals": 10,
      "allowed_tools": ["shell_exec", "git_write"],
      "required_approvals": 3,
      "window_hours": 24
    }
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `auto_approval.enabled` | `false` | Enable auto-approval learning |
| `auto_approval.max_auto_approvals` | `10` | Maximum auto-approvals per user per hour |
| `auto_approval.allowed_tools` | `[]` | Explicit tool allowlist (required — no blanket approval) |
| `auto_approval.required_approvals` | `3` | Manual approvals needed before auto-approval activates |
| `auto_approval.window_hours` | `24` | Lookback window for counting previous approvals |

**How it works:**

1. User requests a tool that requires approval
2. User manually approves the operation
3. The approval is recorded (user + tool + parameters)
4. After the same operation is manually approved `required_approvals` times within `window_hours`, future identical requests are auto-approved
5. Auto-approvals count toward the hourly cap and are logged as `auto_approved` in the audit trail

## Cron Scheduler

Schedule recurring workflows with distributed-safe execution:

```mermaid
graph LR
    subgraph Scheduler
        TICK[Tick Loop<br/>every 30s] --> POLL[GetDueJobs<br/>FOR UPDATE<br/>SKIP LOCKED]
        POLL --> FIRE[Submit Workflow<br/>as job creator]
        FIRE --> RECORD[RecordExecution<br/>Advance NextRunAt]
    end

    subgraph Security
        FIRE --> RBAC2[Same RBAC Pipeline<br/>as Interactive Use]
    end

    STARTUP[Gateway Startup] --> RECOVER[Recover Missed Jobs]
    RECOVER --> TICK
```

- **Jobs run as their creator's identity** — RBAC, budget, and audit enforced per-user
- **Distributed-safe** — `SELECT FOR UPDATE SKIP LOCKED` prevents double-firing across gateway instances
- **Crash-safe** — Missed jobs recovered on startup within a configurable window (default 1 hour)
- **Manual triggers** — Run as the requesting user, not the creator (prevents privilege escalation)
- **Failure notifications** — Optional integration with the notification dispatcher sends alerts through configured channels when jobs fail
- **Conversational creation** — Users can create cron jobs via natural language through any gateway using the `create_cronjob` tool (e.g., "Every 15 minutes check failed Kubernetes pods and restart them if safe")

### CronJobs API

```bash
# Create a cron job
curl -X POST http://localhost:8080/v1/cronjobs \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "daily-cost-audit",
    "cron_expression": "0 9 * * *",
    "goal": "Audit cloud costs and report anomalies",
    "budget_limit_usd": 1.0
  }'

# List cron jobs
curl http://localhost:8080/v1/cronjobs \
  -H "Authorization: Bearer YOUR_API_KEY"

# Manually trigger a cron job
curl -X POST http://localhost:8080/v1/cronjobs/{id}/trigger \
  -H "Authorization: Bearer YOUR_API_KEY"
```

## Alerting & Notifications

Akili includes a built-in alerting engine and multi-channel notification dispatcher. Alert rules monitor targets on a cron schedule, evaluate conditions, and dispatch notifications when status changes — all with the same security, audit, and credential isolation as the rest of the system.

```mermaid
graph LR
    subgraph "Alert Checker"
        POLL2[Poll DB<br/>FOR UPDATE<br/>SKIP LOCKED] --> EVAL[Evaluate Check<br/>HTTP status / command]
        EVAL --> COMPARE[Status Changed?<br/>Cooldown elapsed?]
        COMPARE -->|Yes| NOTIFY[Dispatch Notifications]
        COMPARE -->|No| HISTORY2[Record History]
        NOTIFY --> HISTORY2
    end

    subgraph "Notification Channels"
        TG3[Telegram]
        SL3[Slack]
        EM3[Email SMTP]
        WH3[Webhook HTTP]
        WA3[WhatsApp]
        SG3[Signal]
    end

    NOTIFY --> TG3 & SL3 & EM3 & WH3 & WA3 & SG3

    HISTORY2 --> PG2[(PostgreSQL)]
```

- **Distributed-safe** — `SELECT FOR UPDATE SKIP LOCKED` prevents double-evaluation across gateway instances
- **Status change detection** — Notifications fire on transitions (ok → alert, ok → error), not on every check
- **Cooldown suppression** — Configurable minimum interval between notifications per rule (default 300s) prevents alert storms
- **Append-only history** — Every check evaluation is recorded with status, duration, and notification results
- **SSRF protection** — HTTP checks and webhook notifications block private IPs (10.x, 172.16-31.x, 192.168.x, loopback, link-local)
- **Credential isolation** — Email SMTP passwords and webhook secrets resolved per-channel from `CredentialRef` environment variables. Never stored in config or exposed to the LLM
- **Conversational creation** — Users can create alerts via natural language through any gateway using the `create_alert` tool (e.g., "Check https://api.example.com/health every 5 minutes and notify me on telegram")

### Notification Channels

| Channel | Transport | Configuration |
|---------|-----------|---------------|
| Telegram | Bot API `sendMessage` | `chat_id` in channel config, `TELEGRAM_BOT_TOKEN` env var |
| Slack | `chat.postMessage` API | `channel_id` in channel config, `SLACK_BOT_TOKEN` env var |
| Email | SMTP with TLS | `to` in channel config, SMTP password from `CredentialRef` env var |
| Webhook | HTTP POST (JSON) | `url` in channel config, SSRF-protected |
| WhatsApp | Cloud API `messages` | `phone_number_id` + `recipient` in channel config, `WHATSAPP_ACCESS_TOKEN` env var |
| Signal | signal-cli-rest-api | `recipient` in channel config, `SIGNAL_API_URL` + `SIGNAL_SENDER_NUMBER` env vars |

### Check Types

| Type | Description |
|------|-------------|
| `http_status` | HTTP GET request, compare response status code against expected (default 200) |

### Alerts API

```bash
# Create a notification channel
curl -X POST http://localhost:8080/v1/notification-channels \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "ops-telegram",
    "channel_type": "telegram",
    "config": {"chat_id": "-100123456789"}
  }'

# Create an alert rule
curl -X POST http://localhost:8080/v1/alerts \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "api-health",
    "target": "https://api.example.com/health",
    "check_type": "http_status",
    "cron_expression": "*/5 * * * *",
    "channels": ["ops-telegram"],
    "cooldown_seconds": 300
  }'

# List alert rules
curl http://localhost:8080/v1/alerts \
  -H "Authorization: Bearer YOUR_API_KEY"

# View alert history
curl http://localhost:8080/v1/alerts/{id}/history \
  -H "Authorization: Bearer YOUR_API_KEY"

# Send a test notification
curl -X POST http://localhost:8080/v1/notification-channels/{id}/test \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### Configuration

```json
{
  "notification": {
    "enabled": true,
    "email": {
      "host": "smtp.example.com",
      "port": 587,
      "username": "alerts@example.com",
      "from": "akili@example.com",
      "tls": true
    },
    "whatsapp": {
      "access_token": ""
    },
    "signal": {
      "api_url": "http://localhost:8080",
      "sender_number": ""
    }
  },
  "alerting": {
    "enabled": true,
    "poll_interval_seconds": 30,
    "max_concurrent_checks": 10,
    "default_cooldown_seconds": 300
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `notification.enabled` | `false` | Enable the notification dispatcher (requires database) |
| `notification.email.host` | — | SMTP server hostname |
| `notification.email.port` | `587` | SMTP server port |
| `notification.email.username` | — | SMTP authentication username |
| `notification.email.from` | — | Sender email address |
| `notification.email.tls` | `true` | Use TLS for SMTP connection |
| `alerting.enabled` | `false` | Enable the alert checker loop (requires notification) |
| `alerting.poll_interval_seconds` | `30` | How often to poll for due alert rules |
| `alerting.max_concurrent_checks` | `10` | Maximum concurrent check evaluations per tick |
| `alerting.default_cooldown_seconds` | `300` | Default minimum interval between notifications |
| `notification.whatsapp.access_token` | — | WhatsApp Business API access token (or use `WHATSAPP_ACCESS_TOKEN` env var) |
| `notification.signal.api_url` | — | Base URL of signal-cli-rest-api (e.g. `http://localhost:8080`) |
| `notification.signal.sender_number` | — | Registered Signal phone number |

Telegram and Slack senders are auto-registered when `TELEGRAM_BOT_TOKEN` or `SLACK_BOT_TOKEN` environment variables are set. WhatsApp is registered when `WHATSAPP_ACCESS_TOKEN` is set. Signal is registered when both `SIGNAL_API_URL` and `SIGNAL_SENDER_NUMBER` are set. The webhook sender is always registered. Email requires explicit SMTP configuration.

## Heartbeat Tasks

Heartbeat tasks are periodic agent-executed tasks defined in Markdown files. Unlike cron jobs (created via API, submit multi-agent workflows), heartbeat tasks are defined on disk and execute directly via `agent.Process()`.

```mermaid
graph LR
    subgraph "File Poller"
        SCAN[Scan .md files] --> HASH[SHA-256<br/>Change Detection]
        HASH --> PARSE[Parse YAML<br/>Frontmatter + Body]
        PARSE --> UPSERT[Upsert to DB]
    end

    subgraph "Task Runner"
        TICK2[Tick Loop<br/>every 30s] --> DUE[GetDueTasks<br/>FOR UPDATE<br/>SKIP LOCKED]
        DUE --> EXEC2[agent.Process<br/>with timeout]
        EXEC2 --> RESULT[Persist Result<br/>Append-only]
        RESULT --> NOTIFY2[Notify Channels]
    end

    UPSERT -.-> DUE
```

- **Markdown-defined** — Tasks are defined in `.md` files with YAML frontmatter, similar to skill definitions. Drop files in a directory, and the poller automatically syncs them to the database.
- **Two execution modes** — `quick` tasks (default 60s timeout) for simple responses, `long` tasks (default 5m timeout) for complex operations requiring web search, file operations, etc.
- **Per-task scheduling** — Each task has its own cron expression. File-level defaults apply when per-task overrides are not specified.
- **Budget enforcement** — Per-task budget limits flow through the same budget reservation pipeline as interactive use.
- **Change detection** — SHA-256 file hashing detects modifications. Changed files are re-parsed and tasks are upserted. Deleted files trigger removal of associated tasks.
- **Distributed-safe** — `SELECT FOR UPDATE SKIP LOCKED` prevents double-execution across gateway instances.
- **Append-only results** — Every execution produces an immutable `HeartbeatTaskResult` record with status, output, duration, tokens used, and notification results.

### Task File Format

```markdown
---
name: daily-ops
description: Daily operational heartbeat tasks
user_id: cli-user
default_cron: "*/30 * * * *"
default_budget_usd: 0.50
notification_channels: []
enabled: true
---

## Quick Tasks

### Report current time
- cron: "* * * * *"
- mode: quick
- budget_usd: 0.05

Report the current time and date.

## Long Tasks

### Search AI news
- cron: "0 9 * * *"
- mode: long
- budget_usd: 1.00

Search the web for the latest AI news and provide a summary.
```

**Parsing rules:**
- `## Quick Tasks` / `## Long Tasks` headings set the default mode for tasks below
- `### <Task Name>` starts a new task definition
- Bullet lines with `- cron:`, `- mode:`, `- budget_usd:` are per-task metadata overrides
- All remaining text under a task heading becomes the prompt sent to `agent.Process()`

### Configuration

```json
{
  "heartbeat_tasks": {
    "enabled": true,
    "tasks_dirs": ["configs/heartbeat-tasks"],
    "poll_interval_seconds": 30,
    "file_poll_interval_seconds": 60,
    "max_concurrent_tasks": 3,
    "quick_task_timeout_seconds": 60,
    "long_task_timeout_seconds": 300,
    "default_user_id": "cli-user"
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the heartbeat task runner |
| `tasks_dirs` | `[]` | Directories to scan for `.md` task files |
| `poll_interval_seconds` | `30` | How often to poll DB for due tasks |
| `file_poll_interval_seconds` | `60` | How often to scan directories for file changes |
| `max_concurrent_tasks` | `3` | Maximum concurrent task executions per tick |
| `quick_task_timeout_seconds` | `60` | Timeout for quick mode tasks |
| `long_task_timeout_seconds` | `300` | Timeout for long mode tasks |
| `default_user_id` | `"system"` | User ID for tasks without an explicit `user_id` |

## Runbook Library

Akili ships with a curated collection of SRE skill definitions in the `runbooks/` directory. These are production-ready runbooks that the agent uses to handle common operational scenarios — and serve as templates for writing your own.

### Included Runbooks

| Runbook | Category | Role | Risk | Description |
|---------|----------|------|------|-------------|
| `incident-triage` | incident-response | researcher | medium | Gather metrics, check endpoints, review logs, classify severity |
| `log-analysis` | incident-response | researcher | low | Analyze logs for error patterns, frequency, and root cause |
| `service-health-check` | monitoring | executor | medium | Verify HTTP endpoints, process status, resources, dependencies |
| `capacity-planning` | capacity | researcher | low | Analyze utilization trends and forecast scaling needs |
| `deployment-rollback` | deployment | executor | high | Safely revert to a known-good version |
| `deployment-status` | deployment | researcher | low | Check running versions and pending changes across environments |
| `database-performance` | database | researcher | low | Identify slow queries, lock contention, and storage growth |
| `cost-optimization` | cost | researcher | low | Find idle resources, right-sizing opportunities, and savings |
| `certificate-check` | security | executor | medium | Audit TLS certificate validity and expiry across endpoints |
| `network-diagnostics` | networking | executor | medium | Diagnose DNS, connectivity, latency, and routing issues |

### Usage

Point `orchestrator.skills_dir` at the `runbooks/` directory in your configuration:

```json
{
  "orchestrator": {
    "enabled": true,
    "skills_dir": "runbooks/"
  }
}
```

The skill loader automatically parses, validates, and seeds runbooks into the database at startup. Runbook descriptions are injected into the agent's system prompt so the LLM knows which operational procedures are available.

### Writing Custom Runbooks

Create a Markdown file in the `runbooks/` directory with YAML frontmatter:

```markdown
---
name: My Custom Runbook
agent_role: executor
category: my-category
tools_required:
  - shell_exec
  - file_read
risk_level: medium
default_budget: 0.50
---

Description of what this runbook does and step-by-step procedures...
```

**Required fields:**

| Field | Description |
|-------|-------------|
| `name` | Human-readable runbook name |
| `agent_role` | Agent role: `executor`, `researcher`, `analyst`, `validator` |
| `category` | Grouping category (free-form string) |
| `tools_required` | List of tools the runbook needs (must exist in the tool registry) |
| `risk_level` | `low`, `medium`, `high`, or `critical` |
| `default_budget` | Default budget in USD (must be > 0) |

The Markdown body after the frontmatter becomes the runbook description injected into the agent prompt. Include purpose, when to use, step-by-step procedures, expected outputs, and escalation criteria.

Validate your runbooks with:

```bash
make validate-runbooks
```

## MCP Integration

Extend the agent's capabilities by connecting external [MCP](https://modelcontextprotocol.io) servers:

```mermaid
graph LR
    subgraph Akili
        BRIDGE[MCP Bridge] --> REG[Tool Registry]
        REG --> SEC[Security Pipeline]
    end

    subgraph "MCP Servers"
        FS[Filesystem Server<br/>stdio]
        GH[GitHub Server<br/>stdio]
        JIRA[Jira Server<br/>SSE/HTTP]
    end

    BRIDGE -- "ListTools + CallTool" --> FS & GH & JIRA
```

MCP tools are auto-discovered at startup and namespaced as `mcp__<server>__<tool>`. They flow through the same RBAC, approval, budget, and audit pipeline as native tools.

### Configuration

```json
{
  "tools": {
    "mcp": [
      {
        "name": "filesystem",
        "transport": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/safe"],
        "permission": "mcp_filesystem",
        "risk_level": "medium"
      },
      {
        "name": "github",
        "transport": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-github"],
        "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_TOKEN}"},
        "permission": "mcp_github",
        "risk_level": "high"
      },
      {
      "name": "kubernetes-mcp-server",
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "kubernetes-mcp-server@latest"],
      "permission": "mcp__infra",
      "risk_level": "high"
    }
    ]
  }
}
```

Then grant the permission to roles that should access MCP tools:

```json
{
  "security": {
    "roles": {
      "operator": {
        "permissions": ["read_files", "web_search", "mcp_filesystem"],
        "max_risk_level": "medium"
      }
    }
  }
}
```

Supported transports: `stdio` (subprocess), `sse` (Server-Sent Events), `streamable_http` (modern HTTP+SSE hybrid). Environment variable values support `${VAR}` expansion so secrets stay in the environment. HTTP headers also support `${VAR}` expansion for authentication tokens.

## Infrastructure Management

Akili includes a built-in infrastructure registry that enables the agent to discover, manage, and execute commands on remote nodes — without ever seeing raw credentials.

```mermaid
graph LR
    subgraph "LLM (sees aliases only)"
        QUERY["'Check disk on VM10'"]
    end

    subgraph "Akili"
        LOOKUP[infra_lookup<br/>Resolve alias → node]
        CREDS[Secret Provider<br/>Resolve credential_ref]
        MCP_CALL[Forward to MCP Server<br/>with injected credentials]
        SANITIZE[Sanitize output<br/>Redact any leaked secrets]
    end

    subgraph "Remote"
        NODE[VM10<br/>192.168.1.10]
    end

    QUERY --> LOOKUP --> CREDS --> MCP_CALL --> NODE
    NODE --> SANITIZE --> QUERY

    style CREDS fill:#f44336,color:#fff
```

- **Credential isolation** — The LLM only sees node names and aliases. Credentials are resolved at the execution layer via a pluggable `SecretProvider` and injected into MCP calls. Raw secrets never appear in conversation history, audit logs, or LLM context.
- **Node registry** — Store infrastructure metadata (host, type, port, tags, MCP server mapping) in PostgreSQL or in-memory. Nodes support aliases for natural language reference (e.g., "VM10", "prod-db").
- **Remote execution** — Commands are forwarded to the node's configured MCP server with credentials injected automatically. Output is sanitized to redact any leaked secret material before returning to the LLM.
- **CRUD via conversation** — Operators can add, update, and remove nodes conversationally through any gateway.

### Infrastructure Tools

| Tool | Risk Level | Description |
|------|------------|-------------|
| `infra_lookup` | Low | Query nodes by name, alias, or ID. Returns sanitized metadata (no credentials). |
| `infra_exec` | Critical | Execute a command on a remote node. Credentials auto-resolved. Always requires approval. |
| `infra_manage` | High | Add, update, or remove nodes from the registry. |

### Supported Node Types

| Type | Description | Execution Method |
|------|-------------|------------------|
| `vm` | Virtual machine | SSH via MCP server |
| `kubernetes` | Kubernetes cluster | kubectl via MCP server |
| `docker_host` | Docker host | Docker API via MCP server |
| `bare_metal` | Physical server | SSH via MCP server |

### Configuration

```json
{
  "infrastructure": {
    "enabled": true
  },
  "secrets": {
    "providers": [
      {"type": "env"}
    ]
  }
}
```

## Secret Providers

Akili uses a pluggable secret provider chain to resolve credential references at execution time. Secrets are never stored in the node registry or exposed to the LLM.

```mermaid
graph LR
    REF["credential_ref:<br/>env://VM10_SSH_KEY"] --> CHAIN[Composite Provider]
    CHAIN --> ENV[Environment Provider<br/>os.Getenv]
    CHAIN --> VAULT[Vault Provider<br/>vault://path/key]
    CHAIN --> AWS[AWS Secrets Manager<br/>aws://secret-id]

    style ENV fill:#4CAF50,color:#fff
```

- **Environment provider** — Resolves `env://VAR_NAME` references from environment variables
- **Vault provider** — Resolves `vault://secret/data/path#field` references from HashiCorp Vault KV v2. Token-based auth (`VAULT_TOKEN`). Optional `#field` selector extracts a single key from the secret data.
- **Composite provider** — Chains multiple providers; first match wins
- **Extensible** — Implement the `Provider` interface to add AWS Secrets Manager or any custom backend

### Vault Configuration

```json
{
  "secrets": {
    "providers": [
      {
        "type": "vault",
        "config": {
          "address": "https://vault.example.com:8200",
          "namespace": "admin/team-a",
          "timeout": "5s"
        }
      },
      {"type": "env"}
    ]
  }
}
```

Set `VAULT_ADDR` and `VAULT_TOKEN` as environment variables (these override config values). Optionally set `VAULT_NAMESPACE` for enterprise namespaces.

**Credential reference format:**

```
vault://secret/data/myapp/db#password
│       │                     │
│       │                     └── Field selector (optional, extracts single key)
│       └── Full KV v2 API path
└── Provider prefix
```

| Config Key | Env Override | Default | Description |
|------------|-------------|---------|-------------|
| `address` | `VAULT_ADDR` | — | Vault server URL (required) |
| `token` | `VAULT_TOKEN` | — | Auth token (required) |
| `namespace` | `VAULT_NAMESPACE` | `""` | Enterprise namespace |
| `timeout` | — | `5s` | HTTP request timeout |
| `tls_skip_verify` | — | `false` | Skip TLS verification (dev only) |

### Provider Interface

```go
type Provider interface {
    Resolve(ctx context.Context, credentialRef string) (*Secret, error)
    Name() string
}
```

## Security Model

```mermaid
graph TD
    REQ[Tool Request] --> VALIDATE[Validate Params]
    VALIDATE --> RBAC3[RBAC Check<br/>Role has permission?]
    RBAC3 -->|No| DENY[Denied + Logged]
    RBAC3 -->|Yes| RISK[Risk Check<br/>Exceeds role max?]
    RISK -->|Yes| APPROVAL2[Approval Required<br/>Human must approve]
    RISK -->|No| BUDGETCHECK[Budget Check<br/>Sufficient funds?]
    APPROVAL2 -->|Approved| BUDGETCHECK
    APPROVAL2 -->|Denied| DENY
    BUDGETCHECK -->|No| DENY
    BUDGETCHECK -->|Yes| RESERVE[Reserve Budget<br/>Atomic lock]
    RESERVE --> INTENT[Log Intent]
    INTENT --> EXEC[Execute Tool]
    EXEC --> RESULT[Log Result]
    RESULT --> COST[Record Cost]

    style DENY fill:#f44336,color:#fff
    style EXEC fill:#4CAF50,color:#fff
```

### Risk Levels

| Level | Description | Example Tools |
|-------|-------------|---------------|
| Low | Read-only, no side effects | `file_read`, `web_fetch`, `database_read` |
| Medium | Writes to scoped resources | `file_write`, `create_cronjob`, `send_notification` |
| High | System changes, may need approval | `shell_exec`, `git_write`, `code_exec`, `create_alert` |
| Critical | Destructive, always needs approval | `infra_exec`, custom tools |

### Approval Workflows

Approval requests have a configurable TTL (default 300s). Once expired, they cannot be approved. The approval state machine is immutable: once resolved (approved/denied/expired), the status cannot change.

When approval is granted and the tool executes, the result is persisted back into conversation history — replacing the "pending" placeholder. This ensures the agent never re-requests approval for an already-completed action.

### Audit Events

Every action produces an immutable audit record:

```json
{
  "timestamp": "2026-02-17T12:30:45Z",
  "correlation_id": "req-abc123",
  "user_id": "cli-user",
  "action": "shell_exec",
  "tool": "shell_exec",
  "parameters": {"command": "ls /tmp"},
  "result": "success",
  "cost_usd": 0.0,
  "tokens_used": 150
}
```

## Capability-Based Policies

Beyond RBAC roles, Akili supports fine-grained capability policies that can be bound to specific agents or roles. Policies define explicit allowlists and denylists for tools, paths, domains, commands, and skills.

```json
{
  "policies": {
    "capabilities": {
      "restricted-agent": {
        "allowed_tools": ["file_read", "web_fetch", "git_read"],
        "denied_tools": ["shell_exec", "code_exec"],
        "allowed_paths": ["/opt/app/logs", "/tmp"],
        "denied_paths": ["/etc", "/root"],
        "allowed_domains": ["api.example.com"],
        "denied_domains": ["internal.corp.net"],
        "allowed_commands": ["ls", "cat", "grep"],
        "denied_commands": ["rm", "dd", "mkfs"],
        "max_risk_level": "medium",
        "require_approval": ["git_write"]
      }
    },
    "bindings": {
      "agent-123": "restricted-agent",
      "operator": "restricted-agent"
    }
  }
}
```

| Field | Description |
|-------|-------------|
| `allowed_tools` / `denied_tools` | Explicit tool allowlist/denylist |
| `allowed_paths` / `denied_paths` | Filesystem path restrictions |
| `allowed_domains` / `denied_domains` | Network domain restrictions |
| `allowed_commands` / `denied_commands` | Shell command restrictions |
| `allowed_skills` / `denied_skills` | Skill-level access control |
| `max_risk_level` | Maximum permitted risk level |
| `require_approval` | Tools that always require human approval |

Policies are evaluated after RBAC checks. When no policy is bound, no additional restrictions apply (backward compatible).

## Sandbox

All external commands execute in isolation:

```mermaid
graph LR
    subgraph Process Sandbox
        TEMP[Temp Directory] --> ULIMIT[ulimit enforcement]
        ULIMIT --> PGROUP[Process Group Isolation]
        PGROUP --> CLEANENV[Clean Environment<br/>No credential leakage]
    end

    subgraph Docker Sandbox
        EPHEMERAL[Ephemeral Container] --> CAPDROP[cap-drop=ALL]
        CAPDROP --> NOROOT[Non-root UID 65534]
        NOROOT --> READONLY[Read-only FS]
        READONLY --> LIMITS[CPU + Memory + PID Limits]
    end
```

- **Process sandbox** — Temporary directory, process group isolation, `ulimit` enforcement, clean environment (no credential leakage)
- **Docker sandbox** — Ephemeral containers with `--cap-drop=ALL`, non-root user (UID 65534), read-only filesystem, PID/memory/CPU limits

The Docker sandbox uses a minimal Alpine 3.21-based runtime image with Python3, Bash, Git, and Curl. No compiler or package manager cache included.

### Build the runtime image

```bash
docker build -t akili-runtime:latest -f docker/Dockerfile.runtime .
```

### Configuration

```json
{
  "sandbox": {
    "type": "docker",
    "max_memory_mb": 256,
    "max_execution_seconds": 30,
    "docker": {
      "image": "akili-runtime:latest",
      "cpu_cores": 0.5,
      "pids_limit": 64
    }
  }
}
```

## Gateways

```mermaid
graph LR
    USER_CLI[Terminal User] --> CLI2[CLI Gateway<br/>Interactive REPL]
    USER_HTTP[API Client] --> HTTP2[HTTP Gateway<br/>REST API :8080]
    USER_SLACK[Slack User] --> SLACK2[Slack Gateway<br/>Events + Commands]
    USER_TG[Telegram User] --> TG2[Telegram Gateway<br/>Long-poll / Webhook]
    USER_SIG[Signal User] --> SIG2[Signal Gateway<br/>Polling]
    REMOTE_AGENT[Remote Agents] <-- "WebSocket<br/>/ws/agents" --> HTTP2

    CLI2 & HTTP2 & SLACK2 & TG2 & SIG2 --> AGENT[Agent Core]
```

### CLI

```
$ ./akili

Akili — Autonomous AI Operator System for DevOps, SRE, and Platform Teams
Type your message (or "exit" to quit).

akili> Show me the git log for /home/project
```

The CLI gateway maintains a persistent conversation session for the duration of the process.

### HTTP API

```bash
# Send a query
curl -X POST http://localhost:8080/v1/query \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"message": "Check the status of /opt/repos/myapp"}'

# Send a query with conversation continuity
curl -X POST http://localhost:8080/v1/query \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"message": "What about the logs?", "conversation_id": "uuid-from-previous-response"}'

# Approve a pending action
curl -X POST http://localhost:8080/v1/approve \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"approval_id": "a3f8c1...", "decision": "approve"}'
```

### Slack

Configure the Slack gateway with HMAC signature verification and user mapping:

```json
{
  "gateways": {
    "slack": {
      "enabled": true,
      "listen_addr": ":3000",
      "user_mapping": {"U12345": "slack-user"},
      "approval_channel": "C_APPROVALS"
    }
  }
}
```

Set `SLACK_SIGNING_SECRET` and `SLACK_BOT_TOKEN` in your environment. Approval notifications can be routed to a dedicated channel via `approval_channel`.

### Telegram

```json
{
  "gateways": {
    "telegram": {
      "enabled": true,
      "allowed_users": [123456789],
      "user_mapping": {"123456789": "telegram-user"},
      "poll_timeout_seconds": 30
    }
  }
}
```

Set `TELEGRAM_BOT_TOKEN` in your environment. The bot uses long polling by default (no public URL needed) or webhook mode if `webhook_url` is set. Long messages exceeding Telegram's 4096-character limit are automatically split at paragraph/line/word boundaries with code fence tracking across chunks.

### Signal

```json
{
  "gateways": {
    "signal": {
      "enabled": true,
      "api_url": "http://localhost:8080",
      "sender_number": "+1234567890",
      "allowed_users": ["+1234567890"],
      "user_mapping": {"+1234567890": "signal-user"},
      "poll_interval_seconds": 2
    }
  }
}
```

Set `SIGNAL_GATEWAY_API_URL` and `SIGNAL_GATEWAY_SENDER_NUMBER` in your environment, or configure them in the JSON. Requires a self-hosted [signal-cli-rest-api](https://github.com/bbernhard/signal-cli-rest-api) instance. Approvals use text-based commands (`/approve <id>`, `/deny <id>`) since Signal doesn't support inline buttons.

## Soul Evolution

Akili includes an optional event-sourced self-improvement layer that allows the system to evolve its operational identity over time.

- **Event-sourced architecture** — All soul evolution is recorded as immutable events in an append-only database table
- **Six evolution categories** — Principles (immutable axioms), learned patterns, operational philosophy, reflections, reasoning strategies, and self-imposed guardrails
- **Periodic reflection** — LLM-powered self-reflection cycles analyze past behavior and propose improvements at configurable intervals
- **Security-constrained** — Core principles (Default Deny, Explicit Allow, Auditability) are immutable and seeded on first run. Soul evolutions cannot override hard policy constraints
- **Materialized snapshot** — Current soul state is rendered as `SOUL.md` in the workspace for human review

### Configuration

```json
{
  "soul": {
    "enabled": true,
    "reflection_interval_mins": 60,
    "max_patterns": 100,
    "max_reflections": 50,
    "max_strategies": 25
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `enabled` | `false` | Enable the soul evolution layer |
| `reflection_interval_mins` | `60` | Interval between LLM-powered reflection cycles |
| `max_patterns` | `100` | Maximum stored learned patterns |
| `max_reflections` | `50` | Maximum stored reflections |
| `max_strategies` | `25` | Maximum stored reasoning strategies |

## Token Optimization

Akili includes built-in optimizations to reduce LLM token usage without sacrificing capability.

- **Lazy Tool Loading** — Only intent-relevant tools are sent per LLM call, reducing token overhead for large tool registries
- **Runbook Matching** — Runbooks are scored against conversation context; only top matches above a threshold are injected into the system prompt
- **Skill Summary Modes** — Control how much skill documentation is included: `"full"`, `"compact"`, or `"none"`

### Configuration

```json
{
  "optimization": {
    "lazy_tool_loading": true,
    "max_runbook_matches": 2,
    "runbook_match_threshold": 0.05,
    "skill_summary_mode": "compact"
  }
}
```

| Field | Default | Description |
|-------|---------|-------------|
| `lazy_tool_loading` | `false` | Send only intent-relevant tools per LLM call |
| `max_runbook_matches` | `2` | Maximum runbooks injected per call |
| `runbook_match_threshold` | `0.05` | Minimum similarity score for runbook injection |
| `skill_summary_mode` | `"full"` | Skill documentation verbosity: `"full"`, `"compact"`, `"none"` |

## API Documentation (OpenAPI & Swagger UI)

You can enable automatic OpenAPI specification generation and Swagger UI by setting `enable_docs` to `true` in the `http` configuration block.

### Example Configuration

```json
{
  "http": {
    "enabled": true,
    "listen_addr": ":8080",
    "enable_docs": true,
    "max_request_size_bytes": 1048576,
    "api_key_user_mapping": {},
    "rate_limit": {
      "requests_per_minute": 60,
      "burst_size": 10
    }
  }
}
```

### Accessing the Documentation

Once enabled, the documentation will be served by the HTTP server:

* **Swagger UI**: `/docs`
* **OpenAPI Specification (JSON)**: `/openapi.json`

## Observability

```mermaid
graph LR
    AKILI[Akili] --> METRICS["/metrics<br/>Prometheus"]
    AKILI --> TRACES["OTLP Exporter<br/>OpenTelemetry"]
    AKILI --> HEALTH2["/healthz + /readyz<br/>Probes"]
    AKILI --> ANOMALY["Anomaly Detection<br/>Error rate + Budget spikes"]
    AKILI --> AUDITLOG["Audit Log<br/>Append-only JSONL + DB"]
```

- **Prometheus metrics** — Tool execution counts, LLM token usage, budget consumption, scheduler tick duration, workflow status, skill performance
- **OpenTelemetry tracing** — End-to-end traces from gateway through security pipeline to tool execution. Supports OTLP/gRPC export.
- **Health checks** — Liveness (`/healthz`) always returns OK if the process is running. Readiness (`/readyz`) checks dependencies (database, sandbox) with a 3-second timeout.
- **Anomaly detection** — Threshold-based alerting for error rate spikes and budget anomalies with configurable sliding windows

### Configuration

```json
{
  "observability": {
    "metrics": {
      "enabled": true,
      "path": "/metrics"
    },
    "tracing": {
      "enabled": false,
      "endpoint": "localhost:4317",
      "protocol": "grpc",
      "service_name": "akili",
      "sample_rate": 1.0
    },
    "anomaly": {
      "enabled": true,
      "error_rate_threshold": 0.5,
      "budget_spike_multiplier": 3.0,
      "window_seconds": 300
    }
  }
}
```

## Configuration

Akili supports both JSON and YAML configuration files. The format is detected by file extension (`.yml`/`.yaml` for YAML, `.json` for JSON). By default, SQLite is used for storage — no external database required.

```json
{
  "storage": {
    "driver": "sqlite"
  },
  "providers": {
    "default": "anthropic",
    "fallback": ["openai", "gemini"],
    "anthropic": { "model": "claude-sonnet-4-5-20250929" },
    "openai": { "model": "gpt-4o" },
    "gemini": { "model": "gemini-2.0-flash" },
    "ollama": { "model": "llama3.1", "base_url": "http://localhost:11434" }
  },
  "security": {
    "default_role": "operator",
    "roles": {
      "viewer": {
        "permissions": ["read_files", "web_search", "database_read", "infra_lookup"],
        "max_risk_level": "low"
      },
      "operator": {
        "permissions": ["read_files", "web_search", "shell_exec", "write_files", "git_read", "git_write", "database_read", "create_cronjob", "create_alert", "send_notification", "infra_lookup", "infra_exec"],
        "max_risk_level": "high",
        "require_approval": ["shell_exec", "git_write", "create_cronjob", "create_alert", "infra_exec"]
      },
      "admin": {
        "permissions": ["read_files", "web_search", "shell_exec", "write_files", "git_read", "git_write", "code_exec", "database_read", "create_cronjob", "create_alert", "send_notification", "infra_lookup", "infra_exec", "infra_manage"],
        "max_risk_level": "critical",
        "require_approval": ["code_exec", "create_alert", "infra_exec", "infra_manage"]
      }
    }
  },
  "budget": { "default_limit_usd": 10.0 },
  "sandbox": { "type": "process", "max_memory_mb": 512, "max_execution_seconds": 30 },
  "tools": {
    "file": { "allowed_paths": ["/tmp/akili"], "max_file_size_bytes": 10485760 },
    "web": { "allowed_domains": [], "timeout_seconds": 15 },
    "database": { "max_rows": 1000, "timeout_seconds": 30 },
    "code": { "allowed_languages": ["python3", "sh"] },
    "git": { "allowed_paths": [] },
    "mcp": []
  },
  "gateways": {
    "cli": { "enabled": true },
    "http": { "enabled": true, "listen_addr": ":8080" },
    "websocket": {
      "enabled": false,
      "path": "/ws/agents",
      "heartbeat_interval_seconds": 30,
      "task_reassign_timeout_seconds": 120
    }
  },
  "memory": { "enabled": true, "max_history_messages": 100, "max_message_bytes": 32768 },
  "orchestrator": { "enabled": true, "max_depth": 5, "max_tasks": 50, "skills_dir": "skills/" },
  "scheduler": { "enabled": true, "poll_interval_seconds": 30, "max_concurrent_jobs": 5, "missed_job_window_seconds": 3600 },
  "notification": { "enabled": true, "email": { "host": "smtp.example.com", "port": 587, "from": "akili@example.com", "tls": true }, "whatsapp": { "access_token": "" }, "signal": { "api_url": "http://localhost:8080", "sender_number": "" } },
  "alerting": { "enabled": true, "poll_interval_seconds": 30, "max_concurrent_checks": 10, "default_cooldown_seconds": 300 },
  "heartbeat_tasks": { "enabled": false, "tasks_dirs": ["configs/heartbeat-tasks"], "default_user_id": "cli-user" },
  "approval": {
    "ttl_seconds": 300,
    "auto_approval": {
      "enabled": false,
      "max_auto_approvals": 10,
      "allowed_tools": [],
      "required_approvals": 3,
      "window_hours": 24
    }
  },
  "observability": {
    "metrics": { "enabled": true, "path": "/metrics" },
    "tracing": { "enabled": false, "endpoint": "localhost:4317", "service_name": "akili", "sample_rate": 1.0 },
    "anomaly": { "enabled": true, "error_rate_threshold": 0.5, "budget_spike_multiplier": 3.0, "window_seconds": 300 }
  },
  "infrastructure": { "enabled": false },
  "secrets": {
    "providers": [
      { "type": "env" }
    ]
  }
}
```

For PostgreSQL (production/multi-tenant):

```json
{
  "storage": {
    "driver": "postgres",
    "postgres": {
      "dsn": "postgres://user:pass@localhost/akili?sslmode=disable",
      "max_open_conns": 25,
      "max_idle_conns": 5,
      "conn_max_lifetime_s": 1800,
      "default_org_name": "default"
    }
  }
}
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic API key (required when using Anthropic provider) |
| `OPENAI_API_KEY` | OpenAI API key (required when using OpenAI provider) |
| `GEMINI_API_KEY` | Google Gemini API key (required when using Gemini provider) |
| `AKILI_WORKSPACE` | Override workspace directory (default: `~/.akili/`) |
| `AKILI_DB_DSN` | PostgreSQL DSN for internal storage (overrides config) |
| `AKILI_TOOL_DB_DSN` | PostgreSQL DSN for the database read tool (overrides config) |
| `AKILI_API_KEYS` | HTTP API key mappings (format: `key:user,key2:user2`) |
| `AKILI_WS_AGENT_TOKEN` | Token for WebSocket agent authentication |
| `SLACK_SIGNING_SECRET` | Slack gateway signing secret |
| `SLACK_BOT_TOKEN` | Slack gateway bot token |
| `TELEGRAM_BOT_TOKEN` | Telegram gateway bot token |
| `WHATSAPP_ACCESS_TOKEN` | WhatsApp Business API access token for notification sender |
| `SIGNAL_API_URL` | Signal notification sender API URL (signal-cli-rest-api) |
| `SIGNAL_SENDER_NUMBER` | Signal notification sender phone number |
| `SIGNAL_GATEWAY_API_URL` | Signal gateway API URL (signal-cli-rest-api) |
| `SIGNAL_GATEWAY_SENDER_NUMBER` | Signal gateway sender phone number |

## Tool Details

### Database Read Tool

Runs read-only SQL queries against an external PostgreSQL database.

- **Allowed:** `SELECT`, `EXPLAIN`, `SHOW`, `DESCRIBE`, `WITH` (CTEs)
- **Blocked:** All write/DDL operations (`INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `CREATE`, `TRUNCATE`, etc.)
- **Protections:** Comment stripping, multi-statement blocking, row limits (default 1000), per-query timeout

### Git Write Tool

Performs local git write operations inside a sandbox.

- **Allowed:** `add`, `commit`, `checkout`, `branch`, `tag`, `stash`, `merge`, `rebase`, `reset`, `rm`, `mv`, `restore`, `switch`, `cherry-pick`
- **Blocked:** `push`, `fetch`, `pull`, `remote`, `clone`, `init`, `clean` (all remote and destructive-init operations)
- **Protections:** Credential environment variables stripped, `GIT_TERMINAL_PROMPT=0`, no SSH agent forwarding

### Web Fetch Tool

HTTP client with SSRF protection:

- Domain allowlist enforcement
- Private IP range blocking (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, loopback, link-local)
- Redirect target validation
- Response body size limit

### Tool Parameter Reference

| Tool | Required Params | Optional Params |
|------|----------------|-----------------|
| `shell_exec` | `command` (string) | `timeout` (string), `working_dir` (string) |
| `file_read` | `path` (string) | `operation` ("read" or "list") |
| `file_write` | `path` (string), `content` (string) | |
| `web_fetch` | `url` (string) | `method` ("GET" or "HEAD") |
| `git_read` | `subcommand` (string), `repo_path` (string) | `args` ([]string) |
| `git_write` | `subcommand` (string), `repo_path` (string) | `args` ([]string) |
| `code_exec` | `language` (string), `code` (string) | |
| `database_read` | `query` (string) | `max_rows` (number) |
| `create_cronjob` | `name`, `cron_expression`, `goal` | `budget_limit_usd`, `description` |
| `create_alert` | `name`, `target`, `check_type`, `cron_expression`, `channels` | `check_config`, `cooldown_seconds`, `description`, `enabled` |
| `send_notification` | `channels`, `message` | `subject` |
| `infra_lookup` | | `query` (string), `operation` ("lookup" or "list"), `node_type` (string) |
| `infra_exec` | `node` (string), `command` (string) | `timeout` (string) |
| `infra_manage` | `operation` ("add", "update", "remove") | `name`, `aliases`, `node_type`, `host`, `port`, `user`, `credential_ref`, `tags`, `mcp_server`, `node_id` |

## Project Structure

```
akili/
├── cmd/akili/           # CLI entry point and mode initialization
│   ├── main.go             # Root command (defaults to gateway mode)
│   ├── gateway.go          # Gateway mode startup
│   ├── agent.go            # Agent mode startup (headless task poller)
│   ├── query.go            # One-shot query command
│   ├── shared.go           # Shared component initialization
│   ├── onboarding.go       # Interactive setup wizard
│   └── version.go          # Version command
├── internal/
│   ├── agent/              # Agent interface, orchestrator, conversation memory, WebSocket client
│   ├── alerting/           # Alert checker engine (poll, evaluate, dispatch)
│   ├── approval/           # Approval workflow management (in-memory + DB)
│   ├── config/             # Configuration loading and validation
│   ├── domain/             # Cross-cutting entity types (CronJob, InfraNode, AlertRule, HeartbeatTask, etc.)
│   ├── gateway/            # Gateway interface and implementations
│   │   ├── cli/            # Interactive CLI with session persistence
│   │   ├── httpapi/        # REST API with workflow, cron, alert, channel endpoints
│   │   ├── slack/          # Slack integration (HMAC-verified)
│   │   ├── telegram/       # Telegram bot (long-poll + webhook + message splitting)
│   │   ├── signal/           # Signal bot (polling + text-based approvals)
│   │   └── ws/             # WebSocket gateway for remote agent communication
│   ├── heartbeat/          # Agent heartbeat tracking (stale checker)
│   ├── heartbeattask/      # Periodic heartbeat tasks (file loader, poller, runner, metrics)
│   ├── infra/              # Infrastructure node registry (Store interface + in-memory impl)
│   ├── llm/                # LLM provider abstraction
│   │   ├── anthropic/      # Anthropic Claude implementation
│   │   ├── openai/         # OpenAI GPT implementation
│   │   ├── gemini/         # Google Gemini implementation
│   │   └── ollama/         # Ollama (local models) implementation
│   ├── notification/       # Multi-channel notification dispatcher (Telegram, Slack, WhatsApp, Signal, Email, Webhook)
│   ├── observability/      # Metrics, tracing, health, anomaly detection
│   ├── orchestrator/       # Multi-agent workflow engine + skill tracking
│   ├── protocol/           # WebSocket protocol types (envelopes, messages, task assignments)
│   ├── ratelimit/          # Per-user token bucket rate limiting
│   ├── sandbox/            # Process and Docker execution isolation
│   ├── scheduler/          # Distributed cron job scheduler (with failure notifications)
│   ├── secrets/            # Secret provider chain (env, vault, composite)
│   ├── security/           # RBAC, audit logging, budget management
│   ├── skillloader/        # Markdown skill definition parser
│   ├── storage/            # Unified storage interface
│   │   ├── postgres/       # PostgreSQL persistence layer (GORM)
│   │   └── sqlite/         # SQLite persistence layer (GORM, zero-config default)
│   ├── soul/               # Event-sourced self-improvement layer
│   ├── workspace/          # Workspace directory management (~/.akili/)
│   └── tools/              # Tool interface and implementations
│       ├── shell/          # Sandboxed shell execution
│       ├── file/           # File read/write with path restrictions
│       ├── web/            # HTTP client with SSRF protection
│       ├── git/            # Git read and write operations
│       ├── code/           # Code execution (Python, shell)
│       ├── database/       # Read-only SQL queries
│       ├── alert/          # Alert tools (create_alert, send_notification)
│       ├── cronjob/        # Conversational cron job creation
│       ├── infra/          # Infrastructure tools (lookup, exec, manage)
│       └── mcp/            # MCP client bridge (external tool servers)
├── configs/                # Configuration files
│   └── heartbeat-tasks/   # Heartbeat task Markdown files
├── examples/               # Deployment examples (Ollama, monitoring, etc.)
├── runbooks/               # SRE runbook skill definitions (community library)
├── skills/                 # Custom skill definition Markdown files
└── docker/                 # Docker build files (runtime image)
```


## Contributing

Contributions are welcome! Please open an issue to discuss proposed changes before submitting a pull request.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
