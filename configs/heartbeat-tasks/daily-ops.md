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

Search the web for the latest AI news and provide a summary of the top stories.

### Check weather forecast
- cron: "0 7 * * *"
- mode: long
- budget_usd: 0.50

Check the weather forecast for today and provide a brief summary.
