---
title: Status and diagnostics
order: 7
---

Check the platform's runtime status, and export or report diagnostics.

## Runtime status

Platform administrators open **Settings → Platform → Runtime status** to see:

- The server version currently running;
- How many background tasks are due and still queued, and how long the oldest one has waited;
- How many tasks are running, and how many are paused because their workspace is suspended;
- How many tasks failed in the last 7 days and won't be retried, and how many are waiting to retry after a failure.

**Task queues** lists queued, running, retrying, paused, and failed tasks for each queue. If the waiting count keeps growing or tasks wait a long time, the server instances handling that queue are not keeping up or are blocked. The page refreshes every 15 seconds.

## Recent errors

**Retrying and failed tasks** lists every task waiting to retry and tasks that failed in the last 7 days, most recent failure first. Each entry shows the task name, its workspace (or **Platform task** for platform-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel.

Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Export diagnostics

This section is being written.

## Reporting settings

The **Report runtime metrics and errors** switch on the **Platform overview** page controls whether this platform reports runtime data to the licensing service. It is on by default. A change takes effect immediately on the current server instance and within a minute on other instances.

When it is on, the following is reported:

- Runtime metrics: every minute, the number of accounts, workspaces, and members, plus active accounts and active workspaces over the last 7 days;
- Errors: when the server fails to handle a request, hits an unexpected exception, or a background task still fails on its last retry, the name of the failed operation or task, the error types, the database error code, the stack trace for exceptions, the server version, and the runtime environment.

The original error message is written only to the server's local log and is never reported. The `event_id` on that log line matches the reported error event, so you can use it to find the full error message in the log. Message content, customer data, file content, access tokens, passwords, keys, and the server host name are never included.
