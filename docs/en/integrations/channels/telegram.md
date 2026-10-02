---
title: Telegram
order: 4
---

Serve customers through a Telegram bot.

This page is being written.

## Create a bot

## Connect the bot

## Webhook

## Account linking and identification

If your business system already runs its own Telegram bot (for example, for account linking), set the channel's connection to "Business system forwarding". Your business system keeps receiving the bot's webhook and forwards private messages to {{product}}, which replies to customers with the same bot token.

With business system forwarding, {{product}} neither sets nor deletes the bot's webhook. After switching from a direct connection, your business system needs to call Telegram's `setWebhook` with its own URL to replace the webhook {{product}} set earlier.

Forwarding requests:

- Use the "Forwarding URL" and "Forwarding secret" shown in the channel's connection information.
- Send the Telegram Update JSON as is, with the forwarding secret in the `X-Telegram-Bot-Api-Secret-Token` header.
- When the customer has linked their Telegram account in your business system, add the customer's signed identity in the `X-Customer-Token` header. It is signed the same way as for the website widget (the customer identity secret in settings, HS256, with `sub` set to the user ID in your business system). Customers with a signed identity are treated as verified, so AI agents can look up their business data.
- Messages without a signed identity are treated as coming from an unverified customer, and any earlier verification of that Telegram user is removed.
- Forwarding the same Update more than once records it only once and does not change the customer's current verification.

```http
POST <forwarding URL>
Content-Type: application/json
X-Telegram-Bot-Api-Secret-Token: <forwarding secret>
X-Customer-Token: <customer signed identity>

{"update_id": 1001, "message": { ... }}
```

| Status | Meaning |
| --- | --- |
| 204 | Accepted |
| 401 | Wrong forwarding secret |
| 403 | The customer signed identity is invalid or expired; the message was not accepted |
| 404 | The channel does not exist or is disabled |
| 503 | Temporarily unavailable; retry later |

The first time a customer sends a message with a given user identity, the conversations of that Telegram account move to the customer profile that user already has (for example, from signing in on your website). Only add a signed identity after your business system has confirmed that the Telegram account belongs to that user, for example through a one-time linking link.
