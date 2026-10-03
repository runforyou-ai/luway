---
title: Credits
order: 6
---

Workspaces pay for platform models with credits at each model's price. Platform admins set the daily grant and can add or deduct credits for individual workspaces. For platform model prices, see [Platform model service](/docs/en/guide/admin/platform-models/#prices).

## Daily grant

In **Settings → Platform → Credits**, enter the **Daily credit grant**. Every workspace receives that many credits each day. Enter 0 to turn the grant off.

- The grant is issued the first time a workspace uses or views credits that day, and only once per day.
- Days follow the platform time zone, which you change on the [Platform overview](/docs/en/guide/admin/overview/) page. Unused grant credits reset at the end of the day in that time zone.
- Changes to the grant or the platform time zone apply from the next grant. Grants already issued don't change.

## View and adjust workspace credits

In **All workspaces**, click a workspace or choose **Credits** from its row actions to open its credits panel. It shows available credits, what's left from today's grant, and the credit history.

Click **Adjust credits**, choose add or deduct, and enter the credits and a note:

- Added credits don't expire.
- Deductions use grant credits that reset soonest first and stop at a balance of 0. If the balance runs out, you're told how many credits were actually deducted.
- The note appears in the workspace's credit history, where members can see it.

## Charging rules

- Before each call starts, credits are reserved based on the input length and the model's maximum output. If available credits aren't enough, the call doesn't run and members see "This workspace doesn't have enough credits."
- When the call ends, the difference from actual usage is refunded or charged. If actual cost exceeds the reservation and the balance can't cover the rest, charging stops at 0. The shortfall is recorded on the call, and the balance never goes negative.
- If a call fails without any usage, the reservation is fully refunded.
- When a call tries several sources, usage from every source is charged.

Each record in **Model calls** shows the credits charged. For what workspaces see, see [Credits](/docs/en/guide/workspace/credits/).
