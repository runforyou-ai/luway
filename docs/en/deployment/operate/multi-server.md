---
title: Multiple servers
order: 7
---

A deployment can run the server on several machines at once, with a load balancer distributing requests.

## Requirements

- All servers connect to the same PostgreSQL and the same NATS with JetStream enabled. See [Internal messaging and background jobs](#internal-messaging-and-background-jobs).
- Each server uses up to about 30 database connections, including its notification listeners. Set PostgreSQL's `max_connections` to at least "number of servers × 30" plus what other clients need.
- All servers run the same version. See [Upgrade and rollback](/docs/en/deployment/operate/upgrade/).
- The deployment has working S3 storage configured. See [File storage](/docs/en/deployment/configure/storage/).
- The load balancer terminates HTTPS, or each server serves HTTPS directly behind DNS round robin or a TCP-only load balancer and shares the access address's certificate. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).


The access address, file storage, email delivery, and branding are stored in the database, so every server uses the same settings automatically. Changes take effect within 10 seconds, with no need to edit or restart each server.

## Internal messaging and background jobs

All application instances share one external NATS account. The only application setting is `nats.url`, whether NATS runs on one node or a cluster. The service address and advertised node addresses must be reachable from every application server. Jobs, realtime history, wakeups and run snapshots share the internal connection; clients connect to the application's `/realtime` gateway.

Initialize resources as described in the [configuration reference](/docs/en/deployment/configure/configuration/#nats). For a three-node cluster, run `nats-init --replicas=3 --storage=file` during deployment. Application startup preserves resource replicas. Business transactions persist jobs, revocations, conversation removals and user notifications in the outbox; ephemeral business hints do not use the outbox. Application instances share delivery work. Each gateway receives broadcasts and manages its own connections.

JetStream consumers distribute background jobs to application instances. NATS triggers recurring schedules; jobs left unacknowledged by a crashed process are redelivered. Handlers must remain idempotent. Diagnostics expose failures and retry state.

Default embedded NATS supports one application process, enforced by a database lock even on the same host. To add application instances, drain the original queue, stop the application, configure and provision external NATS, then start the instances. Existing NATS jobs and realtime history are not migrated automatically.

## Unreachable servers

When a server has no heartbeat for more than 2 minutes, the other servers close all of its database connections, releasing the row locks and message listeners it holds. When that server resumes, it finds its connections closed and exits, and restarts automatically when a process manager or container runs it. All servers must use the same database account, or accounts allowed to terminate each other's connections.

When the message bus runs over PostgreSQL, all servers share its notification queue. If a server hangs without closing its connections, the queue keeps growing, and once it's full both the message bus and writes that send notifications fail. Closing the hung server's connections as described above frees the queue it holds. The **Runtime status** tab shows how much of the notification queue is in use and shows **Notification queue backlog** once it reaches 1%.

## Time

Time-based decisions such as invitation and sign-in expiry, service timeouts, computer online status, and scheduled tasks all use the PostgreSQL clock, so differences between server clocks do not affect them. Signed member realtime credentials, website customer identity tokens, and WeChat Official Account push timestamps are checked against the server's local clock, and logs use the local clock, so servers still need time synchronization.

## Load balancing

Have the load balancer take servers out of rotation based on `/readyz`. A server returns `503` when its message bus connection drops, or when PostgreSQL is unavailable. See [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/).
