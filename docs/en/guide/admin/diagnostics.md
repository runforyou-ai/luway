---
title: Status and diagnostics
order: 7
---

Check the platform's runtime status, and export or report diagnostics.

## Runtime status

Platform administrators open **Settings → Platform → Runtime status** to see:

- How many servers are online, and which versions they run;
- How many background tasks are due and still queued, and how long the oldest one has waited;
- How many tasks are running, and how many are paused because their workspace is suspended;
- How many tasks failed in the last 7 days and won't be retried, and how many are waiting to retry after a failure.

**Task queues** lists queued, running, retrying, paused, and failed tasks for each queue. If the waiting count keeps growing or tasks wait a long time, the servers handling that queue are not keeping up or are blocked. The page refreshes every 15 seconds.

## Servers

**Servers** lists each running server process with its host name, version, instance ID, start time, and last heartbeat. Each server sends a heartbeat every 30 seconds:

- A server with no heartbeat for more than 2 minutes is marked **Unreachable**. This usually means the process exited unexpectedly or can't reach the database. Records that stay unreachable for more than an hour are removed automatically, and servers that stop normally disappear from the list right away.
- Each server uses one NATS connection for background tasks and another for real-time updates. When the task connection drops, the server is marked **Tasks disconnected from NATS** and stops picking up background tasks. When the real-time connection drops, it's marked **Real-time disconnected from NATS** and members connected to that server stop getting live updates. When both drop, it's marked **NATS disconnected**.
- The top of the page shows the versions running on online servers. Several versions may run at once during a rolling upgrade. It also shows how many servers are unreachable or have NATS issues.

## External dependencies

**External dependencies** shows the external services the server relies on:

| Dependency | Status |
| --- | --- |
| Object storage | Shows **Off** when object storage isn't enabled, and files are stored in a local directory on the server. When it's enabled, each refresh makes the server access the bucket with the configured credentials. If that fails, it shows **Check failed** with the error. This check needs permission to access the bucket itself, so credentials that can only read and write files also show a failed check |
| Licensing service | Shows when the last sync succeeded. A sync also counts as successful when the licensing service has no license, or only an expired one, for this server. If the connection fails or the license code can't be verified or saved, it shows when it failed and why. It returns to normal after the next successful sync or online activation |

Click a dependency with a problem to see the full error message in a side panel. The server syncs with the licensing service once a day, and you can also sync manually on the license page.

## Recent errors

**Retrying and failed tasks** lists every task waiting to retry and tasks that failed in the last 7 days, most recent failure first. Each entry shows the task name, its workspace (or **Platform task** for platform-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel.

Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Export diagnostics

This section is being written.

## Reporting settings

By default, the server reports runtime metrics to the licensing service every minute: the number of accounts, workspaces, and members, and active accounts and active workspaces in the last 7 days. Only these counts are sent. Messages, customer data, and other business content are never included.

Platform administrators can turn off **Report runtime metrics** on the **Platform overview** page. The server still syncs its license with the licensing service once a day.
