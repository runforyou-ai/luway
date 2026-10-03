---
title: Status and diagnostics
order: 7
---

Check the deployment's runtime status, and export or report diagnostics.

## Runtime status

Deployment administrators open **Settings → Deployment → Runtime status** to see:

- The server version currently running;
- How many background tasks are waiting, and how long the oldest one has waited;
- How many tasks are running, and how many are paused because their workspace is suspended;
- How many tasks failed in the last 7 days, meaning they used up their retries without succeeding.

**Task queues** lists waiting, running, paused, and failed tasks for each queue. If the waiting count keeps growing or tasks wait a long time, the server instances handling that queue are not keeping up or are blocked. The page refreshes every 15 seconds.

## Recent errors

**Failed and retrying tasks** lists tasks that failed in the last 7 days and tasks waiting to retry, most recent failure first. Each entry shows the task name, its workspace (or **Deployment task** for deployment-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel.

Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Export diagnostics

This section is being written.

## Reporting settings

This section is being written.
