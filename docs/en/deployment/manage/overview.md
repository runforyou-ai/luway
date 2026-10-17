---
title: Platform overview
order: 1
---

Platform administrators review the platform's scale, activity, customer service usage, and model usage. For how servers and background tasks are running, see **Runtime status** and **Recent errors** under the **Monitor** sidebar group, described in [Status and diagnostics](/docs/en/deployment/manage/diagnostics/).

## Open platform administration

Open `/admin` (or continue there after first-time setup) and sign in with a platform admin account. You land on the **Dashboard**. The sidebar groups **Operations**, **Monitor**, **System**, and related items such as the dashboard, workspace management, user management, business usage, runtime status, recent errors, system settings, and license.

## Dashboard

**Operations → Dashboard** is the admin home page. It prioritizes:

- **Needs attention**: license status, waiting and delayed tasks, and recent error count;
- **Business usage (last 7 days)**: service sessions, AI resolution rate, knowledge gaps, and model failure rate;
- **Platform scale**: account and workspace totals and growth, activity for the last 7 / 30 days, and the daily active trend for the last 30 days.

Click account or workspace metric cards to open user or workspace management. Click license, runtime, errors, or business usage summaries to open those detail pages. **Monitor** keeps business usage, runtime status, and recent errors.

The deployment ID and license status are also on the **License** page. Self-created workspace quota is under **System settings → Site settings**. See [License](/docs/en/deployment/manage/license/).

The workspace total includes active and suspended workspaces. For each workspace's scale, storage, and last activity, see [Accounts and workspaces](/docs/en/deployment/manage/accounts-workspaces/).

## Business usage

Sidebar **Monitor → Business usage** summarizes customer service data and model usage for the whole platform and for each workspace over the selected period (last 7, 30, or 90 days):

| Metric | Description |
| --- | --- |
| Service sessions | Service sessions closed in the period, and how many conversations they came from |
| AI resolution rate | Among sessions handled by AI employees, the share resolved by AI employees on their own, along with how many sessions AI employees handled |
| Handoff rate | Among sessions handled by AI employees, the share handed off to teammates |
| Teammate first response | Median time from needing a teammate to the teammate's first reply, counted within business hours only. For the whole platform, the page also shows how long 90% of sessions waited |
| Knowledge gaps | All pending knowledge gaps, regardless of the period |
| Model calls | Model calls started in the period, and their failure rate |
| Model tokens | Input and output tokens used by model calls, with the cached share of input shown separately |

The AI resolution rate, handoff rate, teammate first response, and knowledge gaps are counted the same way as the **AI performance** and **Team performance** reports inside a workspace, so workspace administrators see the same numbers in their reports without filters. The workspace list can be sorted by service sessions, conversations, teammate first response, knowledge gaps, or model tokens.

## AI and resources

**Model calls** and **Model tokens** include workspace and shared models.

- Failure rate = (failed + timed out) ÷ finished calls. Finished calls are calls that succeeded, failed, or timed out. Calls still running or canceled aren't counted.
- A call that tried several sources counts once, by its final status.
- Model tokens are input plus output tokens. Cached input tokens are included in input.

For the storage each workspace uses, see [Accounts and workspaces](/docs/en/deployment/manage/accounts-workspaces/).

## How metrics are counted

- Active account: an account that sent a message or handled a customer conversation (replied, took over, closed, or transferred it) that day. An account in several workspaces counts once at the platform level.
- Active workspace: a workspace with an active member, or with customer messages or AI employee replies that day.
- Activity and new records are split into days by the **platform time zone**. You change it under **Platform admin → System settings → Site settings**; see [Platform time zone](/docs/en/deployment/configure/address/#platform-time-zone). After a change, all history is recounted in the new time zone, and the page shows that it is recounting until it finishes.
- Activity is summarized every 10 minutes, so recent actions may appear a little later.
- Business usage counts service sessions by the time they ended and model calls by the time they started, across active and suspended workspaces.
- Usage data contains only counts, durations, and statuses. It never includes message content, customer profiles, or credentials.
