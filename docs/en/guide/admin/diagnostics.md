---
title: Status and diagnostics
order: 7
---

Check the deployment's runtime status, and export or report diagnostics.

## Runtime status

Deployment administrators open **Settings → Deployment → Runtime status** to see:

- The server version currently running;
- How many background tasks are due and still queued, and how long the oldest one has waited;
- How many tasks are running, and how many are paused because their workspace is suspended;
- How many tasks failed in the last 7 days and won't be retried, and how many are waiting to retry after a failure.

**Task queues** lists queued, running, retrying, paused, and failed tasks for each queue. If the waiting count keeps growing or tasks wait a long time, the server instances handling that queue are not keeping up or are blocked. The page refreshes every 15 seconds.

## Recent errors

**Retrying and failed tasks** lists every task waiting to retry and tasks that failed in the last 7 days, most recent failure first. Each entry shows the task name, its workspace (or **Deployment task** for deployment-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel.

Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Export diagnostics

This section is being written.

## Reporting settings

This section is being written.
