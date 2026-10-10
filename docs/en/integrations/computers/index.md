---
title: Workspace computers
order: 1
---

Provide execution environments for AI employees.

A workspace computer belongs to the workspace and keeps working when a member leaves or is deactivated. Put it on a server, build machine, or dedicated computer where AI employees that serve employees read and write files, run commands, and call local MCP. For members' own computers, see [Computers](/docs/en/guide/ai-employees/computers/).

## Register a workspace computer

1. Add a computer under **AI employees › Workspace computers** and enter a name.
2. Copy the connection details from the dialog: the server address `EXECUTOR_SERVER_URL` and the computer credential `EXECUTOR_CREDENTIAL`. The credential is shown only once. If you lose it, choose **Reset credential** in the list; the old credential stops working immediately.
3. Start the headless executor on that computer with the connection details. Once it connects, the list shows the computer's platform and online status.

## Get the executor

The headless executor is a standalone program without a graphical interface for Linux, Windows, and macOS on x64 and ARM64. You can get it in one of these ways:

- Download: **Download executor** in the dialog opens this server's download page. In the **Workspace computer executor** section, download the archive that matches the server version and extract the executor program.
- Container image: the executor image is published with each release in the same registry as the server image. Its name is the server image name with the trailing `-server` replaced by `-executor`, and its tag matches the server version. It includes bash, git, and curl and supports x64 and ARM64.
- Build from source: run `wails3 task build:executor` in the repository root (set the architecture with `ARCH`); the program is written to `bin/`. Run `wails3 task build:docker:executor` to build the container image.

> [!NOTE]
> On macOS, a program downloaded in a browser must be removed from quarantine before its first run: `xattr -d com.apple.quarantine <executor program>`.

## Start the executor

Run it directly:

```bash
EXECUTOR_SERVER_URL=https://example.com \
EXECUTOR_CREDENTIAL=<computer credential> \
./<executor program>
```

Run it as a container:

```bash
docker run -d --restart unless-stopped \
  -e EXECUTOR_SERVER_URL=https://example.com \
  -e EXECUTOR_CREDENTIAL=<computer credential> \
  -v executor-data:/data \
  <executor image>
```

| Environment variable | Flag | Description |
| --- | --- | --- |
| `EXECUTOR_SERVER_URL` | `-server` | Server address, required |
| `EXECUTOR_CREDENTIAL` | `-credential` | Computer credential, required |
| `EXECUTOR_DATA_DIR` | `-data-dir` | Data directory. Defaults to a hidden directory named after the executor program in the user's home directory, or `/data` in the container |
| `EXECUTOR_CONCURRENCY` | `-concurrency` | Maximum operations running at once, 4 by default |

In the data directory:

- `folders/` holds each conversation's default folder. `shared/` inside it is a copy of the conversation's shared file area. See [Shared file copy](/docs/en/guide/ai-employees/computers/#shared-file-copy).
- `mcp.json` configures local MCP servers in the same format as the desktop app.
- `agents.json` configures local agents. See [Local agents](/docs/en/guide/ai-employees/computers/#local-agents).
- `skills/` holds skills. The executor also reads skills in `.agents/skills` and `.claude/skills` under the user's home directory.
- `logs/executor.log` is the executor log, also written to standard error. See [Client and executor logs](/docs/en/deployment/operate/monitoring/#client-and-executor-logs).

Restart the executor after you change the MCP configuration, the local agent configuration, or skills.

Commands use the tools on the system PATH, so install the runtimes they need on the computer or in the image first.

When the credential stops working (the computer was deleted or its credential was reset), the executor exits with a non-zero status. Start it again with the new credential. When the executor version does not match the server, the executor stops connecting. Upgrade it to the server's version and start it again.

## Purpose, authorization, and concurrency

After you choose a **Workspace computer** in an AI employee's profile, that AI employee can use the computer in direct chats, group chats, employee service, and customer conversations. The **Allowed operations** you set at the same time decide what it can do, and each command needs the owner's approval. See [Operation levels and approvals](/docs/en/guide/ai-employees/operation-levels/#workspace-computers).

When serving customers, reading files can only reach this conversation's default folder and the skill folders, and writing or editing files can only reach the conversation's default folder. Operations that need the requester's confirmation are not offered in customer conversations.

- Several AI employees can share one workspace computer. Each conversation has its own default folder under `folders/`.
- Operations beyond the concurrency limit wait in line.
- A single operation runs for up to 10 minutes. Operations that run longer are stopped, and the AI employee receives them as failed. A round handed off to a local agent has no time limit and does not count toward the concurrency limit.
- When the computer is offline, operations fail right away and the AI employee tells the person that the computer is offline.
- After you delete a workspace computer, its credential stops working, running operations are interrupted, and AI employees using it no longer use a computer.

## Security recommendations

The executor runs commands with the permissions of the system user that started it. Run it as a dedicated low-privilege user or in a container, mount only the directories AI employees need, and keep the computer credential safe.

## Presence and connection recovery

Computers report presence through an authenticated HTTP heartbeat every 25 seconds, and the server uses database time to determine whether they are online. If realtime notifications disconnect temporarily, the executor continues checking for work every 30 seconds. Resetting credentials, revoking a computer or suspending its workspace invalidates the old connection. Restart the executor with current credentials to resume work.
