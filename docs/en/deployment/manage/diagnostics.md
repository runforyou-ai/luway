---
title: Status and diagnostics
order: 8
---

Check the platform's runtime status, and export or report diagnostics.

## Runtime status

Platform administrators open sidebar **Monitor → Runtime status** to see:

- How many servers are online, and which versions they run;
- How many messages all servers send per second over the message bus, which transport they use, and how much of the PostgreSQL notification queue is in use;
- How many background tasks haven't started yet, plus how many more are delayed or waiting to retry;
- How many tasks are running;
- How many tasks used up their retries and failed in the last 7 days.

**Task queues** lists waiting, running, and failed tasks for each queue. If the waiting count keeps growing, the servers handling that queue are not keeping up or are blocked. While a workspace is suspended, its tasks wait and continue after it resumes. The page refreshes every 15 seconds.

## Servers

**Servers** lists each running server process with its host name, version, instance ID, message bus, start time, and last heartbeat. Each server sends a heartbeat every 10 seconds:

- A server with no heartbeat for more than 30 seconds is marked **Unreachable**. This usually means the process exited unexpectedly or can't reach the database. Records that stay unreachable for more than an hour are removed automatically, and servers that stop normally disappear from the list right away.
- Servers exchange real-time messages over a message bus, and the **Message bus** column shows whether a server uses PostgreSQL or NATS for it. The column also shows the messages it sent per second over the 10 seconds before its last heartbeat. When that connection drops, the server is marked **Message bus disconnected** and members connected to that server stop getting live updates. Background tasks keep running. If messages failed to send or were dropped because they couldn't be processed in time during that period, the server is marked **Dropping messages**, and some members may miss live updates. This usually means the message volume exceeds what that server or PostgreSQL can handle.
- **Message bus** shows the total messages sent per second. Inter-server traffic uses internal NATS; see [Multiple servers](/docs/en/deployment/operate/multi-server/#internal-messaging-and-background-jobs).
- The page shows the versions running on online servers and counts unreachable servers or message bus failures. For connection failures, check networking and NATS credentials.

## Recent errors

Sidebar **Monitor → Recent errors** lists every task waiting to retry and tasks that failed in the last 7 days, most recent failure first. Each entry shows the task name, its workspace (or **Platform task** for platform-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel. Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Export diagnostics

In **Monitor → Runtime status** or **Recent errors**, select **Export diagnostics** to download a ZIP file. `manifest.json` contains the redacted runtime summary and `logs/` contains up to 20 MiB of recent logs from the server handling the export.

Select **Upload diagnostics** and confirm to upload to the official support service. Download and inspect the bundle before sharing if needed. The deployment must already be registered after first-time setup, and object storage must be enabled on the support service, or the upload fails.

Run `{{slug}}-server diagnostics --output diagnostics.zip` to export from the command line. Add `--upload` to upload explicitly. CLI export also works while the server is stopped and includes the version and redacted startup configuration.
