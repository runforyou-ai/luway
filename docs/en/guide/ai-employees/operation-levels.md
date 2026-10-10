---
title: Operation levels and approvals
order: 5
---

When an AI employee calls a business system or uses a workspace computer, the operation level and the authorization decide whether the call runs right away, waits for the initiator's confirmation, or goes to the responsible person for approval.

## Operation levels

Each tool of a business system gets an operation level from whether it is read-only, whether its result can be undone, and whether it binds an identity. See [Business systems](/docs/en/integrations/business/connectors/#tools-and-operation-levels) for how levels are derived:

| Level | Meaning | Examples |
| --- | --- | --- |
| L0 Public information | Read-only, no identity binding | Product lookup, public policies |
| L1 Own data | Read-only, bound to a customer or member identity | My orders, shipment tracking |
| L2 Reversible write | The written result can be undone | Update a profile, create a ticket |
| L3 Irreversible operation | The result cannot be undone, or money is involved | Refunds, balance adjustments |

Tools that send messages to third parties (such as email or SMS) are marked separately, independent of the level.

## Authorization settings

In the AI employee's run configuration, set for each business system:

- **Highest level**: the AI employee can only use tools up to this level.
- **Confirm L2**: when on, L2 operations run only after the initiator confirms them.
- **Allow outbound messages**: when on, the AI employee can use tools that message third parties, and every such call needs approval from the responsible person.

| Operation | How it runs |
| --- | --- |
| L0, L1 | Runs right away |
| L2 | Runs right away; with **Confirm L2** on, runs after the initiator confirms |
| L3 | Runs after the responsible person approves |
| Outbound message | Runs after the responsible person approves |

When the authorization includes L3 or outbound messages, the AI employee must have a responsible person, and the responsible person cannot be removed.

## Workspace computers

Tools on a workspace computer follow the same levels:

| Tool | Level |
| --- | --- |
| Read files, load skills | L0 |
| Write files, edit files | L2 |
| Run commands, browser and desktop actions, hand off to a local agent | L3 |
| Local MCP tools | Derived from the server's read-only and reversible hints, L3 when not declared |

When you choose a workspace computer in the AI employee's profile, also set **Allowed operations** (read files, read and edit files, or all operations) and **Require the requester's confirmation before editing files**. Personal AI employees use their owner's own computer and are not limited by level.

## Approval flow

In an AI chat or Copilot, operations that need your confirmation are handled on the spot: the AI employee pauses once the operation card appears in the conversation, and after you confirm it runs the operation and carries on with the task in the same reply. If you reject the operation or do not handle it within 24 hours, it does not run, and the AI employee continues its reply accordingly.

Other operations that need confirmation or approval are submitted without waiting for the result. The AI employee tells the other party that the operation is waiting to be handled:

1. An operation card appears in the conversation with the tool, its business system or computer, and the arguments the AI employee filled in; arguments filled in automatically from the customer's or member's identity are not shown on the card. A member who handles it gets a notification and also sees the operation in **Pending**.
2. That person confirms, approves, or rejects it on the card or in **Pending**. Arguments cannot be changed; to use different arguments, reject the operation and let the AI employee submit it again.
3. After confirmation or approval, the server runs the operation with the submitted arguments. It first checks the business system, the tool, and the authorization again, and does not run the operation if any of them was deleted, disabled, or no longer covers it. Workspace computer operations go to that computer, and do not run if the AI employee no longer uses the computer or the computer is offline.
4. The result, a rejection, expiry after 24 hours without a decision, or a cancellation is delivered to the AI employee, which then reports the outcome in the conversation.

Who handles it:

- **Confirmation**: the initiator, that is, the member who asked the AI employee in an AI chat, an employee service conversation, or Copilot, or the member who most recently @mentioned the AI employee in a group. In website chat, a signed-in customer confirms the operation in the chat window. Operations that need confirmation are not offered to anonymous visitors or on other customer service channels. Members can see a customer's confirmation card but cannot confirm it on the customer's behalf.
- **Approval**: the AI employee's responsible person. In customer conversations, approval cards are visible only to members, not to the customer.

## Automatic cancellation

An operation waiting for confirmation or approval is cancelled automatically and never runs when:

- The initiating member is deactivated, leaves the group, or is removed from it, or the AI employee's responsible person changes or is deactivated.
- The customer's signed-in identity changes.
- The service conversation is closed, or is transferred or returned to a queue so that the AI employee that submitted the operation no longer handles it.
- The AI employee is deactivated, or a personal AI employee is removed from a group along with its responsible person (only the operations it submitted in that group are cancelled).

The cancellation is also delivered to the AI employee. If the AI employee no longer handles the conversation, the cancellation is only recorded in the conversation.

## Operations pending review

An operation that was interrupted while running, with an unknown result that may already have had an external effect, is marked **Pending review**, and the AI employee does not run it again. The AI employee's responsible person sees these operations in **Pending**, checks the business system, and marks them as reviewed.

## Audit records

Every call an AI employee makes is recorded in its run process, including the tool, its business system or computer, the operation level, the arguments the AI employee filled in, the arguments filled in automatically from identities, and the result. Operations that need confirmation, approval, or review also record who handled them, the decision, and when. The operation card in the conversation keeps the final status.
