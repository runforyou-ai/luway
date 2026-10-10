---
title: Configuration reference
order: 1
---

All server startup configuration fields.

Server configuration has two layers. Startup configuration lives in each server's configuration file. It covers the database and the NATS that multiple servers need, plus settings that can differ per server: listen address, HTTPS port, and egress IP. Changes take effect after a restart. The deployment address and certificate, deployment name, platform time zone, file storage, email delivery, and branding are shared by the whole deployment and stored in the database. Platform administrators change them under **Settings → Platform → Deployment**, and every server picks them up within 10 seconds. See [Deployment address and name](/docs/en/deployment/configure/address/), [File storage](/docs/en/deployment/configure/storage/), [Email](/docs/en/deployment/configure/email/), and [Custom branding](/docs/en/deployment/configure/branding/).

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
| `server.httpsPort` | Empty | Port this server serves HTTPS on directly, such as `443`, using the deployment address's certificate and serving the same routes as `server.port`. Leave empty when a proxy in front provides HTTPS; see [HTTPS and reverse proxies](/docs/en/deployment/configure/https/) |
| `server.clientIPHeader` | Empty | Request header in which the reverse proxy writes the visitor's real IP, such as `CF-Connecting-IP` or `X-Real-IP`. Sign-in, sign-up, website visitor messages and uploads, and help center search are rate limited by this IP. The reverse proxy must overwrite any header of the same name sent by the visitor. Requests without the header, or with a value that isn't a valid address, use the connection's peer address. When empty, a server that serves HTTPS directly uses the visitor address written by its HTTPS entry point, and other servers use the connection's peer address. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/#reverse-proxy-requirements) |
| `server.countryHeader` | Empty | Request header in which the reverse proxy writes the visitor's two-letter country code (such as `CN` or `US`), such as Cloudflare's `CF-IPCountry`. Used to record the region of website visitors and of newly created workspaces. The reverse proxy must overwrite any header of the same name sent by the visitor. When empty, regions are not recorded |
| `server.egressIP` | Empty | Public egress IP this server uses to call external APIs. It appears in the IP whitelist on the [WeChat Open Platform](/docs/en/deployment/manage/wechat-open-platform/) page and on the connection tab of [key-access WeChat Official Account](/docs/en/integrations/channels/wechat-official-account-key/) channels; set it on each server |

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

Background tasks and realtime notifications use NATS JetStream. With `nats.url` empty, the server starts embedded NATS with APP, CLIENT and SYS accounts: APP holds tasks and realtime notifications, CLIENT holds member, visitor and computer connections, and SYS provides administration. Service credentials are random on each start. The signing seed is persisted at `nats/callout.seed` under the data directory; back up the entire `nats` directory. Its WebSocket listener binds to a random loopback port, and the server proxies `/nats`, so every client uses the deployment address. This mode supports one server process on one host.

The inter-server message bus remains separate: it uses PostgreSQL when `nats.url` is empty and NATS when configured. Run streams share the account connection with member notifications. Private run channels require conversation read access; clients read an HTTP snapshot after subscribing, and cross-server snapshots use chunked NATS responses. Website visitors and computer executors also use jetcast through `/nats`. Their authenticated HTTP endpoints issue realtime credentials valid for at most five minutes. Each connection checks current channel, workspace, identity key or computer credential state. Independent HTTP heartbeats maintain computer presence, and executors still poll for work every 30 seconds when notifications are unavailable. Multiple servers require the same external NATS. See [Multiple servers](/docs/en/deployment/operate/multi-server/).

| Setting | Default | Description |
| --- | --- | --- |
| `nats.url` | Empty | External NATS APP service account URL with credentials, such as `nats://app:password@nats:4222`; separate multiple addresses with commas |
| `nats.systemURL` | Empty | SYS account URL with credentials on the same NATS, used to forcibly close revoked connections; required externally |
| `nats.calloutSeed` | Empty | Account private seed matching `auth_callout.issuer`; required externally and identical on all application nodes |
| `nats.webSocketURL` | Empty | Internal HTTP or HTTPS address of the external NATS WebSocket listener, such as `http://nats:8222`; required externally |
| `nats.namespace` | `app` | Message and task prefix. Realtime escapes `_` as `__` and `-` as `_h`, then appends `_realtime`; for example, `app-prod` becomes `app_hprod_realtime`. Must match on all nodes and contain lowercase letters, digits, underscores or hyphens, starting with a letter or digit |
| `nats.replicas` | `1` | Replicas for tasks, realtime history and connection registry, from 1 to 5; use `3` on three-node clusters and `1` in embedded mode |

External NATS requires version 2.15 or later, persistent JetStream, APP, CLIENT and SYS accounts, auth callout and WebSocket. Include both service users in `auth_users`. Keep `auth_callout.account` set to APP; the auth callout places client connections in the account named `CLIENT`, and this name is fixed. The CLIENT account exchanges only the event, control and request subjects under the realtime prefix with APP and limits the subscriptions of each connection and the size of requests clients send; events published by the server are not subject to this limit. Member clients send business session tokens; visitors and computers send short-lived realtime credentials. The application verifies each identity and grants its exact private channels. Service credentials stay on the server. Each NATS APP account serves one deployment; all application nodes share the namespace, signing seed and storage settings.

```text
listen: 0.0.0.0:4222
jetstream { store_dir: /var/lib/nats }
websocket { listen: 0.0.0.0:8222, no_tls: true }
accounts {
  APP {
    jetstream: enabled
    users: [{ user: app, password: $APP_PASSWORD }]
    exports: [
      { stream: "app_realtime.ev.>", accounts: [CLIENT] }
      { stream: "app_realtime.c.>", accounts: [CLIENT] }
    ]
    imports: [{ stream: { account: CLIENT, subject: "app_realtime.rq.>" } }]
  }
  CLIENT {
    limits: { max_subscriptions: 1000, max_payload: 65536 }
    exports: [{ stream: "app_realtime.rq.>", accounts: [APP] }]
    imports: [
      { stream: { account: APP, subject: "app_realtime.ev.>" } }
      { stream: { account: APP, subject: "app_realtime.c.>" } }
    ]
  }
  SYS { users: [{ user: sys, password: $SYS_PASSWORD }] }
}
system_account: SYS
authorization {
  timeout: 5s
  auth_callout { issuer: "<account public key>", account: APP, auth_users: [app, sys] }
}
```

`app_realtime` in the example is the realtime prefix of the default namespace; replace it with the prefix of your namespace. Generate the account key with NATS `nsc generate nkey --account`. Store the private seed in `nats.calloutSeed` and its public key in `issuer`. Expose the WebSocket port only to application servers and reverse proxies, with HTTPS/WSS at the public edge. Clients may originate from different websites; Origin is not authentication, and `same_origin` must not reject these clients. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).

The APP service user needs access to task streams, `KV_<UPPERCASE_NAMESPACE>_JOBS_STATE`, the realtime stream and connection registry KV, message bus, task, realtime and run snapshot subjects (`app.>`, `app-jobs.>`, `app_realtime.>` and `app_realtime_runs.>` for the default namespace; `<namespace>.>`, `<namespace>-jobs.>`, `<realtime prefix>.>` and `<realtime prefix>_runs.>` otherwise), JetStream APIs, `$SYS.REQ.USER.AUTH` and reply subjects. SYS needs `$SYS.REQ.SERVER.*.KICK` and replies. Both connections need `$SYS.REQ.USER.INFO` for startup account verification. Configuration display masks the signing seed and connection credentials.

When the data directory contains task data from the default `$G` account, startup refuses to create the APP account and preserves the old data. Process the old queue and archive its account data before upgrading.

Member connections recover events across short interruptions and reload business data when history expires. Typing is not retained. Drain the old queue before switching to external NATS or changing accounts; tasks and history do not migrate automatically.

Revoking membership forcibly closes the original connection, with persistent tasks retrying temporary SYS administration failures. Each connection's retry registration has an independent timeout. Retries target only the original NATS connection; valid new connections for other workspaces remain usable. After a standalone embedded NATS restart, revocations targeting the stopped instance are complete; external NATS still confirms forced disconnection through SYS. Restore SYS connectivity and KICK permissions during an outage, and inspect revocation errors and the maintenance queue. Member connection credentials last at most twenty minutes, and clients switch to a new, reauthenticated connection before they expire; retries continue until the connection closes or this lifetime ends. Keep NATS and application server clocks synchronized. Clients also retry temporary HTTP failures while restoring business snapshots.

## Logs

| Setting | Default | Description |
| --- | --- | --- |
| `log.level` | `info` | Lowest level written to the local log: `debug`, `info`, `warn`, or `error`. Logs stored in the database always include every level. See [Monitoring and diagnostics](/docs/en/deployment/operate/monitoring/#logs) |

## Data directory

| Setting | Default | Description |
| --- | --- | --- |
| `data.directory` | Platform data directory | This server's data directory, as an absolute path. When the deployment doesn't use object storage, files are saved in `files` under it. Without NATS, background tasks are kept in `nats` under it |

Without `data.directory`, the platform data directory is used:

| Runs on | Data directory |
| --- | --- |
| Linux | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server` |
| Container | `/var/lib/{{slug}}-server`; mount a volume to keep the files |

The release manifest and the client installers, update packages, and executors always sit next to the server program, with no configuration. See [Client distribution](/docs/en/deployment/operate/client-distribution/).
