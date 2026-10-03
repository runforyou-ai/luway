---
title: Deployment overview
order: 1
---

Deployment administrators review the deployment's scale, activity, and customer service usage.

## Open deployment administration

Deployment administrators open **Settings** in any workspace. The **Deployment** group in the left navigation contains the deployment's administration pages. Only deployment administrators see this group.

## Accounts and workspaces

The **Deployment overview** page shows:

- The total number of accounts and the accounts added in the last 30 days;
- The total number of workspaces and the total members across all workspaces;
- Active accounts and active workspaces in the last 7 and 30 days;
- Daily active accounts for the last 30 days. Hover over a bar to see that day's active workspaces and new accounts;
- The instance ID, installation time, and workspace limit.

The workspace total includes active and suspended workspaces. For each workspace's scale, storage, and last activity, see [Accounts and workspaces](/docs/en/guide/admin/accounts-workspaces/).

## Service usage

The **Business usage** page summarizes customer service data for the whole deployment and for each workspace over the selected period (last 7, 30, or 90 days):

| Metric | Description |
| --- | --- |
| Service sessions | Service sessions that ended in the period, and how many conversations they came from |
| AI resolution rate | Among sessions handled by AI employees, the share resolved by AI employees on their own |
| Handoff rate | Among sessions handled by AI employees, the share handed off to teammates |
| Teammate first response | Median time from needing a teammate to the teammate's first reply, counted within business hours only. For the whole deployment, the page also shows how long 90% of sessions waited |
| Knowledge gaps | All pending knowledge gaps, regardless of the period |

These metrics are counted the same way as the **AI performance** and **Team performance** reports inside a workspace, so workspace administrators see the same numbers in their own reports. The workspace list can be sorted by service sessions, conversations, teammate first response, or knowledge gaps.

## AI and resources

This section is being written.

## How metrics are counted

- Active account: an account that sent a message or handled a customer conversation (replied, took over, closed, or transferred it) that day. An account in several workspaces counts once at the deployment level.
- Active workspace: a workspace with an active member, or with customer messages or AI employee replies that day.
- Activity and new records are split into days by the **statistics time zone**. You change it on the Deployment overview page. After a change, all history is recounted in the new time zone, and the page shows that it is recounting until it finishes.
- Activity is summarized every 10 minutes, so recent actions may appear a little later.
- Business usage counts service sessions by the time they ended, across active and suspended workspaces.
- Usage data contains only counts, durations, and statuses. It never includes message content, customer profiles, or credentials.
