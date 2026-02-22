---
name: Database Performance Analysis
agent_role: researcher
category: database
tools_required:
  - database_read
  - shell_exec
risk_level: low
default_budget: 0.50
---

# Database Performance Analysis

Analyze database performance by reviewing slow queries, connection pool utilization, lock contention, and storage growth to identify bottlenecks and optimization opportunities.

## When to Use

- Application latency traced to database queries
- Connection pool exhaustion alerts
- Storage growth exceeding projections
- Periodic database health reviews

## Procedure

1. **Check connection metrics** — Review active connections, idle connections, and connection pool utilization. Identify connection leaks.
2. **Identify slow queries** — Query `pg_stat_statements` or slow query logs to find the top queries by total execution time and frequency.
3. **Analyze query plans** — Run `EXPLAIN ANALYZE` on the top slow queries to identify missing indexes, sequential scans, and join inefficiencies.
4. **Check lock contention** — Review `pg_locks` and `pg_stat_activity` for blocked queries, deadlocks, and long-running transactions.
5. **Review table statistics** — Check table sizes, index usage rates, dead tuple counts, and autovacuum status.
6. **Assess storage growth** — Calculate daily/weekly growth rates and project when current storage will be exhausted.

## Expected Output

A database performance report containing: top 10 slow queries with execution plans, connection pool utilization, lock contention summary, tables needing vacuum or reindex, and storage growth forecast.

## Escalation Criteria

- Connection pool utilization above 80%: immediate scaling or connection leak investigation needed.
- Queries taking longer than 30 seconds: review for missing indexes or query rewrite.
- Dead tuples exceeding 10% of live tuples: schedule manual vacuum.
