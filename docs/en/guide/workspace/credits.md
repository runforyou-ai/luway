---
title: Credits
order: 6
---

When your workspace uses [platform models](/docs/en/guide/workspace/models/#platform-models), each call is paid for with workspace credits at the model's price. Models your workspace configures itself don't use credits. Open **Settings → Integrations → Credits**.

## Balance and daily grant

The top of the Credits page shows:

- **Available credits**: all credits the workspace can use right now.
- **Left from today's grant**: how much of today's daily grant is left, and when it resets.

When a platform admin turns on the daily grant, every workspace receives the same number of credits each day. Credits not used by the end of the day reset and don't carry over. Credits a platform admin adds manually and purchased credits don't expire.

Credits that reset soonest are used first, so today's grant is used before credits that don't expire.

## How calls are charged

Platform admins set each platform model's price. It appears after the model name when you choose a model, for example "Input 2 · Output 8 credits/1M tokens":

- Chat models are priced by input and output tokens.
- Embedding and rerank models are priced by input tokens.
- Decision models are priced per call.
- Some models also charge per call. Models priced at 0 are free.

Before each call starts, some credits are reserved based on the input length and the model's maximum output. When the call ends, the difference from actual usage is refunded or charged. If a call fails without any usage, all reserved credits are refunded. If the actual cost exceeds the reservation and the balance can't cover the rest, charging stops at 0. The balance never goes negative.

## Credit history

The lower part of the Credits page lists changes, newest first:

| Entry | Meaning |
| --- | --- |
| Daily grant | Credits granted for the day |
| Platform adjustment | Credits a platform admin added or deducted, with their note |
| Model call | One entry per call, with the model, usage, tokens, and credits charged. Calls still running are marked **Reserved** |
| Top-up | Credits the workspace purchased and paid for |
| Top-up refunded | When a top-up order is refunded, its unused credits are taken back. Credits already used aren't reclaimed |
| Credits expired | Credits that are no longer valid, such as unused grant credits that reset at the end of the day |

## Running out of credits

If available credits can't cover the reservation for a call, the call doesn't run. AI employees can't reply, and translation, summaries, knowledge base search, and other features that use platform models pause. Actions started by members show "This workspace doesn't have enough credits." Models your workspace configures itself aren't affected.

Calls work again after the next daily grant, or after a platform admin adds credits. You can also switch the affected settings to a model your workspace configures; see [Model services](/docs/en/guide/workspace/models/).
