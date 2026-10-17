---
title: Monitoring and diagnostics
order: 6
---

Check service health, read logs, and handle diagnostics.

## Health checks

The server provides two health check endpoints. Both accept `GET` and `HEAD`. `GET` returns JSON, and `HEAD` returns only the same status code and headers:

| Endpoint | Purpose |
| --- | --- |
| `/healthz` | Liveness check. Returns `200` with `{"status":"ok"}` while the server is running, without checking dependencies. Use it as the liveness probe for your container platform or process manager |
| `/readyz` | Readiness check. Checks the message bus connection and queries PostgreSQL with a 3-second limit. Returns `200` with `{"status":"ready"}` when all are healthy, or `503` with `{"status":"unavailable"}` otherwise. Use it to decide when a load balancer should take a server out of rotation |


## Logs

The server writes JSON logs to standard output and `logs/server-YYYY-MM-DD-<process-id>.jsonl` in its data directory. Files rotate by UTC day and are kept for seven days. `log.level` sets the minimum level and defaults to `info`. Operators can collect either files or container output with Alloy and follow `trace_id` across HTTP requests and background tasks. HTTP request logs include `duration_ms`.

## Client and executor logs

- The desktop app writes logs to standard error and to the local file `logs/desktop.log`, including the logs of its built-in executor. The file is in the desktop app's data directory: `~/Library/Application Support/{{slug}}/logs/desktop.log` on macOS, `%LOCALAPPDATA%\{{slug}}\logs\desktop.log` on Windows, and `~/.local/share/{{slug}}/logs/desktop.log` on Linux.
- The headless executor writes to `logs/executor.log` in its data directory. For the data directory, see [Workspace computers](/docs/en/integrations/computers/#start-the-executor).
- When a log file exceeds 10 MB, it's renamed with a `.1` suffix and a new file starts. Only one old file is kept. The mobile app doesn't write log files.

When the executor runs an operation dispatched to its computer, its logs carry the trace ID `trace_id` from when the operation was dispatched. Its requests for that operation, such as syncing shared files and reporting the result, carry the same ID, so they match the server log entries with that trace ID.


## Export diagnostics

Platform administrators can download a ZIP containing runtime information and local logs, or upload it explicitly to support. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/).

## Official support service

After first-time setup, the server registers the deployment with the official support service. Online activation exchanges an activation code for a license code. Diagnostics upload sends logs to the support service when an administrator requests it. The mobile push relay is not available yet; related requests do not deliver.
