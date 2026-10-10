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

Object storage and the licensing service don't affect the health checks. The licensing service sync result is under **Settings → Platform → License**, and the object storage check is included in exported diagnostics. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/).

## Logs

The server writes logs to two places:

- Database: every server writes logs at all levels to PostgreSQL in asynchronous batches, and they are kept for 30 days. Platform administrators view and filter them across servers in the **Logs** tab of **Overview**. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/#logs). Entries written after a server starts are queued and stored once it joins the deployment. Entries from a server that exits before joining, or written while the database is unavailable or while writes are backed up beyond the buffer, aren't stored in the database.
- Local: written to standard error, not to log files. When you run the program directly, logs appear in the terminal; a process manager running it collects them. In a container, read them with your container runtime, for example `docker logs <container name>`. The local log shows only entries at or above the `log.level` startup setting, `info` by default. See [Configuration](/docs/en/deployment/configure/configuration/).

Log entries written while handling a request or running a background task carry a trace ID, `trace_id`. A request and the background tasks it triggers share one trace ID, including tasks that run on other servers, and each run of a scheduled task gets a new one. Entries that don't belong to a request or task, such as server startup and shutdown, have no trace ID. The server returns the trace ID of each request in the `X-Trace-ID` response header. If the request carries a valid W3C `traceparent` header, the server uses the trace ID from that header. Depending on where it was written, a log entry also carries the entry point `operation`, the background task `task_run_id` and `action`, the workspace `workspace_id`, and the account `account_id`. To investigate an operation, find its trace ID in the response header or the logs, then search the logs on every server for that `trace_id`.

## Client and executor logs

- The desktop app writes logs to standard error and to the local file `logs/desktop.log`, including the logs of its built-in executor. The file is in the desktop app's data directory: `~/Library/Application Support/{{slug}}/logs/desktop.log` on macOS, `%LOCALAPPDATA%\{{slug}}\logs\desktop.log` on Windows, and `~/.local/share/{{slug}}/logs/desktop.log` on Linux.
- The headless executor writes to `logs/executor.log` in its data directory. For the data directory, see [Workspace computers](/docs/en/integrations/computers/#start-the-executor).
- When a log file exceeds 10 MB, it's renamed with a `.1` suffix and a new file starts. Only one old file is kept. The mobile app doesn't write log files.

When the executor runs an operation dispatched to its computer, its logs carry the trace ID `trace_id` from when the operation was dispatched. Its requests for that operation, such as syncing shared files and reporting the result, carry the same ID, so they match the server log entries with that trace ID.

When the server fails to process a request, the message in the app ends with an **Error ID**, which is the trace ID of that request. Give it to a platform administrator, who can search for it in the **Logs** tab of **Overview**. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/#logs).

## Export diagnostics

Platform administrators can export diagnostics from the **Runtime status** tab of **Overview** and send them to operations or technical support. The file includes each server's status and configuration, the object storage check, the licensing service sync result, the database version, and background task errors. It contains no passwords, keys, business content, or logs. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/).

## Remote reporting

By default, the deployment reports runtime metrics such as the number of accounts and workspaces to the licensing service once a minute. Only counts are sent, and platform administrators can turn this off. See [Status and diagnostics](/docs/en/deployment/manage/diagnostics/). Diagnostics are never reported automatically.
