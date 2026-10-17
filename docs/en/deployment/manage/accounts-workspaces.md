---
title: Accounts and workspaces
order: 2
---

Manage accounts, workspaces, sign-up policy, and self-created workspace quota for the platform.

## User management

Open **Operations → User management** in the sidebar to view all platform accounts. Search by email or name, and filter by account status. Each account shows how many workspaces it has joined and when it signed up.

From an account's menu you can:

- Deactivate the account: it is signed out immediately and can no longer sign in;
- Reactivate the account: a deactivated account can sign in again;
- Make it a platform administrator: the account must be an active member of a workspace that is not suspended;
- Remove platform administrator access.

Platform administrators can't change their own account status or administrator access.

## Workspace management

**Workspace management** lists the platform's workspaces. Search by name or the identifier in its access address, filter by status, and sort by newest, recently active, most members, or most storage. Each workspace shows its members, AI employees, channels, computers, storage, and last activity.

Creating a workspace only requires a name; the server generates its access address. Names must be unique within the deployment. Creating and renaming trim surrounding whitespace and compare existing names without regard to case. General settings let you rename a workspace and view or copy its address. Existing links continue working after renaming. Workspace management dialogs also show the address to identify the workspace being managed.

### Suspend a workspace

After suspension:

- Members can't enter the workspace, and it shows as **Suspended** in their workspace list;
- Customer channels such as website and Telegram stop serving customers, and messages customers send during suspension are not received;
- AI employees and background tasks stop running, and queued tasks wait until the workspace resumes;
- All workspace data is kept.

A workspace with a platform administrator among its members can't be suspended, and the suspend item in its menu is unavailable.

### Resume a workspace

After resuming, members can enter again, channels serve customers again, and tasks held during suspension continue.

## Sign-up policy

In platform admin sidebar **System → Site settings**, choose how accounts sign up:

- Open sign-up: anyone can create an account from the sign-in page;
- Invitation only: only people with a valid workspace invitation can create an account.

Workspace member invitations always work: existing accounts only join the workspace; new users may register along the way. Joining by invitation does not consume self-create quota.

## Self-created workspace quota

On **Site settings**, set how many workspaces each account may create. Workspaces joined by invitation do not count. Without a license the quota is locked to 0 (the platform keeps the default workspace from setup). After activation you may raise the quota; once set, it can only stay the same or increase.

With open sign-up and quota 0, self-serve registrations join the default workspace. With quota greater than 0, self-serve registration creates a personal workspace and consumes one quota slot.
