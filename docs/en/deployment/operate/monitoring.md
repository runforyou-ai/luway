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
| `/readyz` | Readiness check. Queries PostgreSQL with a 3-second limit, and returns `200` with `{"status":"ready"}` on success or `503` with `{"status":"unavailable"}` on failure. Use it to decide when a load balancer should take a server out of rotation |

NATS, object storage, and the licensing service don't affect the health checks. Check them under **Settings → Platform → Runtime status**. See [Status and diagnostics](/docs/en/guide/admin/diagnostics/).

## Logs

The server writes logs to standard output and standard error, not to log files. In a container, read them with your container runtime, for example `docker logs <container name>`. When the server runs as a systemd service, use `journalctl -u <service name>`. To keep logs long term, collect them with your container runtime or system log service.

## Export diagnostics

Platform administrators can export diagnostics from the **Runtime status** page and send them to operations or technical support. The file includes each server's status and configuration, external dependencies, the database version, background task errors, and recent model provider results. It contains no passwords, keys, business content, or logs. See [Status and diagnostics](/docs/en/guide/admin/diagnostics/).

## Remote reporting

By default, the server reports runtime metrics such as the number of accounts and workspaces to the licensing service every minute. Only counts are sent, and platform administrators can turn this off. See [Status and diagnostics](/docs/en/guide/admin/diagnostics/). Diagnostics are never reported automatically.
