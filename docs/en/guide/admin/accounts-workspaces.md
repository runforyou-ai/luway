---
title: Accounts and workspaces
order: 2
---

Manage accounts, workspaces, and sign-up and creation policies for the platform.

## Account management

The **All accounts** page lists the platform's accounts. Search by email or name, and filter by active or deactivated. Each account shows how many workspaces it has joined and when it signed up.

From an account's menu you can:

- Deactivate the account: it is signed out immediately and can no longer sign in;
- Reactivate the account: a deactivated account can sign in again;
- Make it a platform administrator: the account must be an active member of a workspace that is not suspended;
- Remove platform administrator access.

Platform administrators can't change their own account status or administrator access.

## Workspace management

The **All workspaces** page lists the platform's workspaces. Search by name or the identifier in its access address, filter by status, and sort by newest, recently active, most members, or most storage. Each workspace shows its members, AI employees, channels, computers, storage, and last activity.

Creating a workspace only requires a name; the server generates its access address. Names must be unique within the deployment. Creating and renaming trim surrounding whitespace and compare existing names without regard to case. General settings let you rename a workspace and view or copy its address. Existing links continue working after renaming. The platform usage list, credits panel, and management dialogs also show the address to identify the workspace being managed.

### Suspend a workspace

After suspension:

- Members can't enter the workspace, and it shows as **Suspended** in their workspace list;
- Customer channels such as website and Telegram stop serving customers, and messages customers send during suspension are not received;
- AI employees and background tasks stop running, and queued tasks are paused;
- All workspace data is kept.

A workspace with a platform administrator among its members can't be suspended, and the suspend item in its menu is unavailable.

### Resume a workspace

After resuming, members can enter again, channels serve customers again, and tasks paused during suspension continue.

## Sign-up policy

On the **Sign-up & creation** page, choose how accounts sign up:

- Open sign-up: anyone can create an account from the sign-in page;
- Invitation only: only people invited to a workspace can create an account.

## Workspace creation policy

On the **Sign-up & creation** page, choose who can create workspaces: any account, or platform administrators only. When the platform has a workspace limit, the page shows it, and no more workspaces can be created once it is reached.
