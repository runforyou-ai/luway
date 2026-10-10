---
title: Upgrade and rollback
order: 2
---

Upgrade the server and provide matching client versions.

## Before you upgrade

- Back up the database before upgrading. See [Backup and restore](/docs/en/deployment/operate/backup/).
- Run `{{slug}}-server version` to see the current version, and check the releases page for new versions.

## Upgrading the standalone program

Upgrade the standalone program by hand. See [Standalone program](/docs/en/deployment/install/standalone/):

1. Stop the server.
2. Download the server archive of the target version from the release, extract it, and replace the old program.
3. Run `{{slug}}-server download-clients` to download the release manifest and client files that match the new program. If the server can't reach the release, use `--from <directory>`.
4. Start the server. The new version runs database migrations automatically when it joins the deployment.

For several servers, replace the program and start it on each server in turn. Once the first new-version server starts, the remaining old-version servers exit automatically; start them again after replacing the program.

## Upgrading containers

Recreate the containers with the new image. For several containers, switch every container to the new image first, then start them one by one.

## Database migrations

The server runs database migrations automatically at startup, with no manual step. When several servers start at once, they join the deployment one at a time and migrations run only once.

`{{slug}}-server migrate status` shows how many database migrations are applied and pending.

## Rollback

Rolling back to a lower version reverts the database migrations that the lower version doesn't include, and deletes the data those migrations hold. Stop every server in the deployment first: a process manager or container restarts a higher-version server that is still running, and it changes the deployment version back.

For the standalone program, prepare the lower-version program on one server, then run:

```bash
./<lower version directory>/{{slug}}-server version --json > lower.json
./<current version directory>/{{slug}}-server migrate down --to lower.json
./<lower version directory>/{{slug}}-server preflight --accept-downgrade
```

Then replace the program on every server with the lower version, run `download-clients`, and start it.

For containers, stop every container, then run these with the same configuration file:

```bash
docker run --rm --entrypoint /server <image>:<lower version> version --json \
  | docker run -i --rm -v ./config:/etc/{{slug}}-server --entrypoint /server <image>:<current version> migrate down --to -
docker run --rm -v ./config:/etc/{{slug}}-server --entrypoint /server <image>:<lower version> preflight --accept-downgrade
```

Then start the containers with the lower-version image.

`migrate down --to` reads the output of the lower-version program's `version --json` and reverts the migrations the lower version doesn't include; `-` reads from standard input. If a server in the deployment is still running, the command stops without changing the database. `preflight --accept-downgrade` changes the deployment version to the program's own version; without the option, it only checks and changes nothing.

If reverting migrations fails, restore the database backup taken before the upgrade. See [Backup and restore](/docs/en/deployment/operate/backup/).

## Version compatibility

All servers in a deployment run the same version. Each server records its version in the database at startup:

- A server older than the deployment version refuses to start, and the log says the server version is older than the deployment version.
- A server newer than the deployment version updates the deployment version, waits for servers of other versions to exit, then runs database migrations and starts serving. For servers that exited abnormally, it waits until their last heartbeat is more than 30 seconds old.
- Running servers notice a changed deployment version within 10 seconds, stop serving, and exit.
- Servers of the same version can run together.

When you upgrade several servers, the old-version servers exit automatically once the first new-version server starts, and the service is briefly unavailable until the new version finishes migrating. The remaining servers then start on the new version. Process manager and container restart policies keep restarting old-version servers, which refuse to start until they are replaced with the new version.
