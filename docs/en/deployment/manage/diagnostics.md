---
title: Status and diagnostics
order: 8
---

Check the platform's runtime status, and export or report diagnostics.

## Runtime status

Platform administrators open the **Runtime status** tab of **Settings → Platform → Overview** to see:

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
- **Message bus** at the top of the page shows how many messages all servers send per second. Over PostgreSQL with heavy traffic it shows **Consider switching to NATS**, and when 1% or more of the notification queue is in use it shows **Notification queue backlog**. See [Multiple servers](/docs/en/deployment/operate/multi-server/#when-to-switch-to-nats) for how to judge and what to do.
- The top of the page shows the version running on online servers. It also shows how many servers are unreachable or have message bus issues. When online servers use different message buses, it shows **Message bus transports differ**. See [Multiple servers](/docs/en/deployment/operate/multi-server/#message-bus) for how to fix it.

## Licensing service sync

**Licensing service sync** on the **License** page shows when the last sync succeeded. A sync also counts as successful when the licensing service has no license, or only an expired one, for this server. If the connection fails or the license code can't be verified or saved, it shows when it failed and why. It returns to normal after the next successful sync or online activation. The server syncs automatically after it starts and then once a day, and you can also sync manually on the license page.

## Recent errors

The **Recent errors** tab of **Overview** lists every task waiting to retry and tasks that failed in the last 7 days, most recent failure first. Each entry shows the task name, its workspace (or **Platform task** for platform-level tasks), the last error message, the attempt count, and when it failed. Click a task to see the full error message in a side panel. Records of finished tasks are kept for 7 days and then cleaned up automatically.

## Logs

The **Logs** tab of **Overview** lists server logs from all servers for the last 30 days at every level: debug, info, warning, and error. The most recent entries come first, and older ones load as you scroll to the bottom. Each entry shows the log message, the entry point or background task, the workspace, the server hostname, the first line of the error message, and the time it was written.

- Use **Level** to show only info and above, warnings and above, or errors. Use **Server** to show only the logs written by one server.
- Enter a trace ID in the search box to see every log entry written for one request and the background tasks it triggered, across all servers. The **Error ID** in an error message that a member reports is this trace ID.
- Click an entry to see its trace ID, entry point, workspace, server, version, task run, queue, account, event ID, other recorded details, and the full error message in a side panel. The filter buttons next to the trace ID, entry point, workspace, and server filter the list by that value; remove these filters from the toolbar.
- When reporting is on, the event ID of an error entry matches the error event reported to the licensing service.

Logs are kept for 30 days and cleaned up a whole day at a time. Entries written after a server starts are queued and stored once it joins the deployment. Entries from a server that exits before joining, or written while the database is unavailable or while writes are backed up beyond the buffer, don't appear here; they go only to the server's local log. See [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/#logs).

## Export diagnostics

When you need help from your operations team or technical support, click **Export diagnostics** at the top of the **Runtime status** or **Recent errors** tab of **Overview** to create a JSON file. The web app downloads it directly; the desktop app asks where to save it. The file name is `diagnostics-` followed by the export time.

The file contains:

| Content | Details |
| --- | --- |
| Basics | When the file was created, and the instance ID of the server that created it |
| Platform overview | Installation time, statistics time zone, number of accounts, workspaces, and members, active and new accounts and workspaces in the last 7 and 30 days, and the daily trend for the last 30 days |
| License | Server ID, license status, customer, term, the capabilities in effect, and the licensing service sync result |
| Deployment settings | Deployment name and address, object storage, email delivery, and branding |
| Servers | Each server's version, start time, last heartbeat, message bus transport and connection status, and the configuration it started with |
| Object storage | When object storage is enabled, the server that creates the file accesses the bucket once with the configured credentials and records why it failed, if it did. This check needs permission to access the bucket itself, so credentials that can only read and write files are also recorded as failed |
| Database | PostgreSQL version and the latest applied migration |
| Background tasks | Task counts for each queue and tasks that failed in the last 7 days, with full error messages |

Deployment settings include only items such as the deployment address, storage mode and bucket, and mail server address. Server configuration includes only items such as the listen address, TLS mode, database and NATS addresses, and the local file directory. The database password, object storage keys, and SMTP user name and password are never written to the file, and user names and passwords are removed from addresses. Background tasks show only their workspace ID, not the workspace name. The file contains no messages, customer data, or other business content, and no server logs. To read logs, see [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/).

## Reporting settings

The **Report runtime metrics and errors** switch under **Deployment → Basics** controls whether this platform reports runtime data to the licensing service. It is on by default. After you save, the change takes effect immediately on the current server instance and within 10 seconds on other instances.

When it is on, the following is reported:

- Runtime metrics: once a minute for the whole deployment, the number of accounts, workspaces, and members, plus active accounts and active workspaces over the last 7 days. When several servers run, only one of them reports;
- Errors: when the server fails to handle a request, hits an unexpected exception, or a background task still fails on its last retry, the name of the failed operation or task, the error types, the database error code, the stack trace for exceptions, the server instance, the server version, and the runtime environment.

The original error message is never reported and stays on this platform: you can see it in the **Logs** tab with the level set to **Error**, and errors that occur while a server can't reach the database are written only to the server's local log. The event ID in the **Logs** side panel and the `event_id` in the local log match the reported error event, so you can use it to find the full error message. Message content, customer data, file content, access tokens, passwords, keys, and the server host name are never included.

When reporting is off, the server still syncs its license with the licensing service once a day.
