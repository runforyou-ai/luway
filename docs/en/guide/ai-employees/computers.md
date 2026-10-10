---
title: Computers
order: 4
---

AI employees run commands and work with files on computers.

This page is being written.

## Computer types

- Personal computer: a member's own computer. It runs operations only for that member's personal AI employees and stops working when the member is deactivated.
- Workspace computer: a computer that belongs to the workspace. It runs operations for the service AI employees that choose it and keeps working when members are deactivated.

## Register a computer

After you install and sign in to the desktop app, that computer registers automatically as your personal computer in each of your workspaces and appears under **Settings › Computers**. If you revoke it, it registers again the next time you sign in to the desktop app.

Add workspace computers under **AI employees › Workspace computers** and start the headless executor with the connection details. See [Workspace computers](/docs/en/integrations/computers/#register-a-workspace-computer).

## Shared file copy

Each conversation has its own default folder on the computer. `shared/` inside it is a copy of the conversation's [shared file area](/docs/en/guide/daily/chats/#shared-conversation-files), with the same paths as `shared/` in the AI employee's file tools. Commands and each local agent round sync before and after they run:

- Files added or changed in the shared file area are copied to the computer. Files a member deleted are also removed from the computer if they were not changed there.
- Files created or changed on the computer are written back to the shared file area, with the AI employee shown as the last editor. A command's result lists the files written back.
- If a file changed or was deleted in the shared file area after the computer last synced, the computer's version is saved with `（电脑上的版本）` added to its name, and the original takes the shared file area's content; if it was deleted, it is removed from the computer too.
- Deleting a file on the computer does not delete it from the shared file area; it comes back at the next sync.

Local MCP, browser, and desktop actions do not sync.

## Local agents

Local agents are agents on a computer that connect through the ACP protocol, such as Codex and Claude Code. An AI employee can hand off coding, file organization, and other tasks that need continuous work on the computer. You keep talking to the AI employee.

- Available local agents: when the `codex` or `claude` command-line tool is installed on the computer and `npx` is available, Codex or Claude Code is offered automatically. Add other agents to `agents.json` in the data directory in the form `{"agents": {"name": {"description": "what it is for", "command": "start command", "args": [], "env": {}}}}`, then restart the desktop app or the executor.
- Enable: select them under **Local agents** in the AI employee's profile. Personal AI employees use the local agents on their own computer, and service AI employees use the ones on their workspace computer. Local agents are not used when serving customers.
- Hand off: the AI employee passes your original words to the local agent, together with context such as earlier conclusions and the files and errors you mentioned. The local agent cannot see the full conversation or the AI employee's memory.
- Progress and reply: a card for the local agent appears under the AI employee's reply and shows its replies, thinking, steps, file changes, and task list as they happen. When it finishes, its final reply is posted to the conversation as is, marked with the local agent it came from.
- Follow-ups: handing off to the same local agent again in the same conversation continues its session, so it remembers earlier rounds. Asking to start over opens a new session. A local agent that supports resuming sessions is shut down after 30 minutes idle and restarts with its earlier session the next time it gets a hand-off.
- Permissions: before the local agent runs a step that needs permission, a confirmation card appears in the conversation. The person who started this round allows or rejects it. Requests left for 24 hours are cancelled.
- Stop: stop it from the card. The running round is interrupted and queued rounds are cancelled. The next hand-off opens a new session.

Local agents call their models with the account signed in on the computer. If an agent needs you to sign in, the conversation asks you to sign in on the computer first.

## Authorized folders

## In-use indicator and stopping

## Execution status
