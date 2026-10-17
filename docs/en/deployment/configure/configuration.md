---
title: Configuration reference
order: 1
---

All server startup configuration fields.

Server configuration has two layers. Startup configuration lives in each server's configuration file. It covers the database and the NATS that multiple servers need, plus settings that can differ per server: listen address and HTTPS port. Changes take effect after a restart. The access address and certificate, deployment name, platform time zone, file storage, email delivery, and branding are shared by the whole deployment and stored in the database. Platform administrators change them under **Platform admin → System settings**, and every server picks them up within 10 seconds. See [Access address and name](/docs/en/deployment/configure/address/), [File storage](/docs/en/deployment/configure/storage/), [Email](/docs/en/deployment/configure/email/), and [Custom branding](/docs/en/deployment/configure/branding/).

## Configuration file

The server reads startup configuration only from a configuration file at a fixed location. The file is YAML and can't contain fields outside the tables below; fields left out use their defaults. The location depends on the system user running the program:

| Runs on | Configuration file |
| --- | --- |
| Linux | `~/.config/{{slug}}-server/server.yaml` |
| macOS | `~/Library/Application Support/{{slug}}-server/server.yaml` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server\server.yaml` |
| Container | `/etc/{{slug}}-server/server.yaml`; mount the configuration directory `/etc/{{slug}}-server` at deployment |

Use `{{slug}}-server config set <field> <value>` to change one field in the configuration file; comments in the file are kept. If the file doesn't exist, it is created and only the current user can read and write it. `config path` prints the location of the configuration file. See [Standalone program](/docs/en/deployment/install/standalone/) and [Docker](/docs/en/deployment/install/docker/).

## Validate configuration

- `{{slug}}-server config check` validates the configuration without connecting to the database.
- `{{slug}}-server config show` prints the effective configuration, with the database password masked and credentials removed from the NATS address.
- `{{slug}}-server preflight` also connects to the database and checks whether this program can join the deployment.

## Server

| Field | Default | Description |
| --- | --- | --- |
| `server.host` | `0.0.0.0` | Listen address. Use `127.0.0.1` to accept connections only from this machine, such as from a reverse proxy on the same host |
| `server.port` | `8080` | Listen port that reverse proxies forward to. When the server serves HTTPS directly, it must be reachable from the internet on port 80 |
| `server.httpsPort` | Empty | Port this server serves HTTPS on directly, such as `443`, using the access address's certificate and serving the same routes as `server.port`. Leave empty when a proxy in front provides HTTPS; see [HTTPS and reverse proxies](/docs/en/deployment/configure/https/) |
| `server.clientIPHeader` | Empty | Request header in which the reverse proxy writes the visitor's real IP, such as `CF-Connecting-IP` or `X-Real-IP`. Sign-in, sign-up, website visitor messages and uploads, and help center search are rate limited by this IP. The reverse proxy must overwrite any header of the same name sent by the visitor. Requests without the header, or with a value that isn't a valid address, use the connection's peer address. When empty, a server that serves HTTPS directly uses the visitor address written by its HTTPS entry point, and other servers use the connection's peer address. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/#reverse-proxy-requirements) |
| `server.countryHeader` | Empty | Request header in which the reverse proxy writes the visitor's two-letter country code (such as `CN` or `US`), such as Cloudflare's `CF-IPCountry`. Used to record the region of website visitors and of newly created workspaces. The reverse proxy must overwrite any header of the same name sent by the visitor. When empty, regions are not recorded |

## Database

| Field | Default | Description |
| --- | --- | --- |
| `database.host` | Empty; required | PostgreSQL address |
| `database.port` | Empty; required | PostgreSQL port, usually `5432` |
| `database.user` | Empty; required | Database account |
| `database.password` | Empty; required | Database password |
| `database.name` | Empty; required | Database name |
| `database.sslMode` | Empty; required | Connection encryption: `disable`, `allow`, `prefer`, `require`, `verify-ca`, or `verify-full` |

## NATS

Background jobs, realtime history and inter-server communication share internal NATS. Leave `nats.url` empty to start persistent embedded NATS in the data directory's `nats` subdirectory. This needs no additional service or network listener and supports one application process. Back up the entire data directory.

| Setting | Default | Description |
| --- | --- | --- |
| `nats.url` | Empty | Optional external NATS connection URL, using `nats://` or `tls://` with credentials in the URL. Separate bootstrap URLs with commas, for example `nats://app:password@nats:4222` |

Application configuration is the same for a standalone server and a cluster. A unified service address can be used; the NATS client also discovers the addresses advertised by the cluster, which must be reachable by application servers. External connection failures prevent startup. Each deployment uses its own NATS account; its name is chosen by the operator.

External NATS requires version 2.15 or later with JetStream enabled. A minimal standalone configuration, accessible only to application servers:

```text
listen: 0.0.0.0:4222
jetstream { store_dir: /var/lib/nats }
authorization { users: [{ user: app, password: $APP_PASSWORD }] }
```

After setting `nats.url`, initialize resources before starting the application:

```sh
luway-server nats-init --replicas=1 --storage=file
```

For a three-node cluster, configure routing, persistent storage and authentication in NATS, then initialize with `--replicas=3`. Replicas and storage belong to the provisioning command. Application startup only validates resource contracts and preserves replicas, capacity and placement. If existing resources have different replicas or storage, initialization reports an error; change these policies explicitly with NATS administration tools.

Initialization creates `APP_JOBS`, `APP_JOBS_DEAD`, `KV_APP_JOBS_STATE` and `APP_REALTIME`. Runtime credentials need task consumer management, JetStream publish/read, KV read/write, and access to `app.>`, `app-jobs.>`, JetStream APIs and reply subjects. Provisioning additionally requires resource creation and update permissions. Jobs, authorization revocations, conversation removals and user notifications are recorded in a database outbox in the business transaction, removed after publish acknowledgement and retried on failure. Conversation updates, typing, computer wake-ups and similar hints are sent only after commit and are not replayed if the process crashes. Job handlers remain idempotent.

Members, website visitors and computers connect to the application's `/realtime` WebSocket gateway. Authenticated HTTP endpoints return the URL and a short-lived ticket: up to two minutes for members and five minutes for visitors and computers. Long-lived credentials are used only over HTTP. The gateway checks identity and exact channel access. Durable authorization events close affected connections across nodes, supplemented by periodic revalidation. When NATS disconnects, gateways close client connections; clients fetch new tickets and reconnect.

Realtime history lasts five minutes. Clients resume by cursor or read a fresh HTTP snapshot when history expires, resources are recreated or recovery limits are exceeded. Typing events are ephemeral. Run streams retain business sequence numbers and chunked cross-server snapshots. Computers retain independent HTTP heartbeats and 30-second work polling. Route `/realtime` to the application; see [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).

Multiple application processes must share one external NATS. An exclusive database lock limits embedded mode to one process, including processes on the same host. Before switching to external NATS or another account, drain the original queue and stop the application. Existing NATS jobs and history are not migrated automatically. See [Multiple servers](/docs/en/deployment/operate/multi-server/).

Delivery is at least once. The job deduplication window is two hours and the realtime window is five minutes; retries after those windows can be delivered again. Clients deduplicate event IDs within a bounded cache for each active channel. Recovery is limited to 1000 frames or 4 MiB; on overflow, expiration or stream recreation, the server reports failed recovery with a new cursor and clients fetch HTTP snapshots. Transient notifications are not guaranteed to replay.

## Logs

| Setting | Default | Description |
| --- | --- | --- |
| `log.level` | `info` | Lowest level written to the local log: `debug`, `info`, `warn`, or `error`. Console output and daily rotated files use the same level. See [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/#logs) |

## Data directory

| Setting | Default | Description |
| --- | --- | --- |
| `data.directory` | Platform data directory | This server's data directory, as an absolute path. Without NATS, background tasks are kept in `nats` under it |

Without `data.directory`, the platform data directory is used:

| Runs on | Data directory |
| --- | --- |
| Linux | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server` |
| Container | `/var/lib/{{slug}}-server`; mount a volume to keep the files |

The release manifest and the client installers, update packages, and executors always sit next to the server program, with no configuration. See [Client distribution](/docs/en/deployment/operate/client-distribution/).
