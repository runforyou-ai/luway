---
title: Standalone program
order: 2
---

Run the server program directly on Linux, Windows, or macOS.

## Download

The server needs PostgreSQL. Have its connection details ready before you run it. When several servers run, they also need NATS with JetStream enabled. See [Multiple servers](/docs/en/deployment/operate/multi-server/).

Download the server archive for your platform and architecture from the release and extract it: `{{slug}}-server_<version>_<os>_<arch>.tar.gz` on Linux and macOS, `{{slug}}-server_<version>_windows_<arch>.zip` on Windows. The archive contains only the program `{{slug}}-server` (`{{slug}}-server.exe` on Windows), which runs from any directory.

## Write the startup configuration

Startup configuration is stored in a configuration file at a fixed location. Write it one field at a time with `config set`. If the file doesn't exist, it is created and only the current user can read and write it:

```bash
./{{slug}}-server config set database.host 127.0.0.1
./{{slug}}-server config set database.port 5432
./{{slug}}-server config set database.user {{slug}}
./{{slug}}-server config set database.password <password>
./{{slug}}-server config set database.name {{slug}}
./{{slug}}-server config set database.sslMode disable
```

`config path` prints the location of the configuration file, and `config check` validates it. For all fields, see [Configuration reference](/docs/en/deployment/configure/configuration/).

The locations of the configuration file and the data directory depend on the system user running the program:

| System | Configuration file | Data directory |
| --- | --- | --- |
| Linux | `~/.config/{{slug}}-server/server.yaml` | `~/.local/share/{{slug}}-server` |
| macOS | `~/Library/Application Support/{{slug}}-server/server.yaml` | `~/Library/Application Support/{{slug}}-server` |
| Windows | `%LOCALAPPDATA%\{{slug}}-server\server.yaml` | `%LOCALAPPDATA%\{{slug}}-server` |

When the deployment doesn't use object storage, local files are saved in `files` under the data directory. To keep data on another disk, set an absolute path with `data.directory`.

## Download client files

```bash
./{{slug}}-server download-clients
```

Run it while the server is stopped. The command downloads the release manifest and client files that match this program's version from the release, verifies the signature and checksums, and puts them in the program directory so the download page can offer desktop installers and executors. If the server can't reach the release, download `release.json`, `release.json.sig`, and the client files listed in `release.json` from the same release on another machine, put them in one directory, and pass it with `--from <directory>`. Without this step, the download page shows that this server provides no installers or executors. See [Client distribution](/docs/en/deployment/operate/client-distribution/).

## Run

```bash
./{{slug}}-server run
```

The server runs in the foreground and writes logs to standard error. Press `Ctrl+C` to stop it. By default it listens on `0.0.0.0:8080`, so other machines on the same network can reach it.

> [!WARNING]
> Complete the first-time setup soon after starting, or restrict access with a firewall first. Until setup is complete, anyone who can reach the server can create the platform administrator.

For first-time setup and further configuration, see [First-time setup](/docs/en/deployment/configure/first-install/).

The program doesn't register a system service. To start it at boot or restart it after a crash, run `{{slug}}-server run` under your operating system's own tools, such as systemd, launchd, or Windows Task Scheduler, as the same system user that wrote the configuration.

## Commands

| Command | Description |
| --- | --- |
| `run` | Run the server in the foreground |
| `status` | Show the deployment version and each server's version and latest heartbeat |
| `config path` | Print the location of the configuration file |
| `config show` | Print the effective configuration with the database password masked |
| `config check` | Validate the configuration |
| `config set <field> <value>` | Change one field in the configuration file, keeping its comments. Takes effect after the server restarts |
| `download-clients` | Download the release manifest and client files that match this program's version; run it while the server is stopped. `--from <directory>` reads them from a local directory |
| `preflight` | Check whether this program can join the deployment with the current configuration |
| `public-url <deployment address>` | Change the deployment address when the admin console is unreachable. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/#change-the-deployment-address-when-the-admin-console-is-unreachable) |
| `migrate status` | Show how many database migrations are applied and pending |
| `reset-server-id` | Generate a new server ID for a new platform built from a copied database |
| `version` | Print the version and the database migration version |

See [Upgrade and rollback](/docs/en/deployment/operate/upgrade/).
