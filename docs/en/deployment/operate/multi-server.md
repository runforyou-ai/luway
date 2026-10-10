---
title: Multiple servers
order: 7
---

A deployment can run the server on several machines at once, with a load balancer distributing requests.

## Requirements

- All servers connect to the same PostgreSQL and the same NATS with JetStream enabled. See [Background tasks](#background-tasks).
- Each server uses up to about 30 database connections, including its notification listeners. Set PostgreSQL's `max_connections` to at least "number of servers × 30" plus what other clients need.
- All servers run the same version. See [Upgrade and rollback](/docs/en/deployment/operate/upgrade/).
- The deployment has object storage turned on. See [File storage](/docs/en/deployment/configure/storage/).
- The load balancer terminates HTTPS, or each server serves HTTPS directly behind DNS round robin or a TCP-only load balancer and shares the deployment address's certificate. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).

While other servers are running, a server refuses to start if the deployment doesn't use object storage. Servers that exited abnormally don't count. Object storage can't be turned off while several servers run.

The deployment address, file storage, email delivery, and branding are stored in the database, so every server uses the same settings automatically. Changes take effect within 10 seconds, with no need to edit or restart each server.

## Message bus

Servers use a message bus for internal wakeups and cross-server signals. Member notifications, run updates, website visitor events and computer work notifications use jetcast over NATS. All clients share the deployment address’s `/nats` WebSocket endpoint. Servers with `nats.url` set use NATS for their internal message bus; point every server's `nats.url` at the same NATS and use the same `nats.namespace`. See [Configuration reference](/docs/en/deployment/configure/configuration/#nats). A single server without NATS uses PostgreSQL.

If some servers use PostgreSQL and others use NATS, the two groups can't receive each other's messages, and running replies, visitors, and executors may miss updates produced on other servers. The servers still start, and the **Runtime** tab under **Settings → Platform → Overview** shows **Message bus transports differ**. Make the configuration consistent as described above, then restart the servers one by one.

### When to switch to NATS

Over PostgreSQL, every message is delivered to every server, so the PostgreSQL cost grows with "messages sent per second by all servers × number of servers". Roughly 200,000 deliveries per second take one PostgreSQL CPU core. Business activity maps to messages roughly as follows:

- An AI reply that is still generating sends about 20 messages per second to each server viewing it.

For example, 600 AI replies generating at once produce about 12,000 messages per second. With 8 servers that's about 100,000 deliveries per second, using about half a PostgreSQL core. A single server can send up to about 17,000 messages per second; beyond that, sends fail.

The **Runtime status** tab under **Settings → Platform → Overview** shows the messages each server sends per second and the total for the deployment. Switch to NATS when:

- The page shows **Consider switching to NATS**, meaning messages per second times the number of online servers reaches 100,000.
- PostgreSQL stays short on CPU.
- Servers stay marked **Dropping messages**.

NATS delivers each message only to the servers subscribed to its topic and doesn't load PostgreSQL.

## Background tasks

Background tasks and realtime event history share the NATS APP account, and client connections use the CLIENT account. Configure the signer seed, APP/SYS credentials, and WebSocket address as described in the [configuration reference](/docs/en/deployment/configure/configuration/#nats), using the same settings on every server. The SYS account enforces member, visitor and computer connection revocation. Before closing each original connection, the application persists its server ID, connection ID and deadline. Background tasks retry failed management requests while other authorized identities remain connected. Background tasks are stored in NATS JetStream. All servers share them, and each task runs on only one server at a time. NATS triggers scheduled tasks, so each runs once each time it comes due, and only one server reports runtime metrics. If a server exits abnormally, other servers run the tasks it was running again after 2 minutes. A task that has used up its retries is marked as failed, and a scheduled task runs again the next time it comes due.

Without NATS, the server starts NATS inside its own process, which works for a single server process only. For a NATS cluster, use three nodes and set `nats.replicas` to `3`. A server using the built-in NATS refuses to start while a server on another host is online in the deployment, and a server with `nats.url` set refuses to start while another server uses the built-in NATS. To grow from one server to several, stop the server during a quiet period, point every server at the same NATS, then start them. Tasks still queued in the built-in NATS don't move to the new NATS.

## Unreachable servers

When a server has no heartbeat for more than 2 minutes, the other servers close all of its database connections, releasing the row locks and message listeners it holds. When that server resumes, it finds its connections closed and exits, and restarts automatically when a process manager or container runs it. All servers must use the same database account, or accounts allowed to terminate each other's connections.

When the message bus runs over PostgreSQL, all servers share its notification queue. If a server hangs without closing its connections, the queue keeps growing, and once it's full both the message bus and writes that send notifications fail. Closing the hung server's connections as described above frees the queue it holds. The **Runtime status** tab shows how much of the notification queue is in use and shows **Notification queue backlog** once it reaches 1%.

## Time

Time-based decisions such as invitation and sign-in expiry, service timeouts, computer online status, and scheduled tasks all use the PostgreSQL clock, so differences between server clocks do not affect them. Signed member realtime credentials, website customer identity tokens, and WeChat Official Account push timestamps are checked against the server's local clock, and logs use the local clock, so servers still need time synchronization.

## Load balancing

Have the load balancer take servers out of rotation based on `/readyz`. A server returns `503` when its message bus connection drops, or when PostgreSQL is unavailable. See [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/).
