---
title: Platform overview
order: 1
---

Platform administrators review the platform's scale, activity, customer service usage, and model usage. For how servers and background tasks are running, see the **Runtime status**, **Recent errors**, and **Logs** tabs, described in [Status and diagnostics](/docs/en/deployment/manage/diagnostics/).

## Open platform administration

Platform administrators open **Settings** in any workspace. The **Platform** group in the left navigation contains **Overview**, **Workspaces & accounts**, **Deployment**, and **License**. Only platform administrators see this group.

## Accounts and workspaces

The **Platform scale** tab of **Overview** shows:

- The total number of accounts and the accounts added in the last 30 days;
- The total number of workspaces and the total members across all workspaces;
- Active accounts and active workspaces in the last 7 and 30 days;
- Daily active accounts for the last 30 days. Hover over a bar to see that day's active workspaces and new accounts. A text message appears when there were no active accounts in that period.

The server ID, license status, and workspace limit are on the **License** page. See [License](/docs/en/deployment/manage/license/).

The workspace total includes active and suspended workspaces. For each workspace's scale, storage, and last activity, see [Accounts and workspaces](/docs/en/deployment/manage/accounts-workspaces/).

## Business usage

The **Business usage** tab of **Overview** summarizes customer service data and model usage for the whole platform and for each workspace over the selected period (last 7, 30, or 90 days):

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
- Activity and new records are split into days by the **platform time zone**. You change it under **Deployment → Basics**; see [Platform time zone](/docs/en/deployment/configure/address/#platform-time-zone). After a change, all history is recounted in the new time zone, and the page shows that it is recounting until it finishes.
- Activity is summarized every 10 minutes, so recent actions may appear a little later.
- Business usage counts service sessions by the time they ended and model calls by the time they started, across active and suspended workspaces.
- Usage data contains only counts, durations, and statuses. It never includes message content, customer profiles, or credentials.
