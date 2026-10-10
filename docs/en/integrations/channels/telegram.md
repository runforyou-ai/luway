---
title: Telegram
order: 4
---

Serve customers through a Telegram bot.

This page is being written.

## Create a bot

## Connect the bot

## Webhook

## Receiving messages

While the channel is enabled, messages customers send to the bot in private chats are written to their conversations and assigned according to the channel's reception settings. Messages in group chats aren't received.

| Message the customer sends | Content written to the conversation |
| --- | --- |
| Text | A text message |
| Photo, file, voice, video, video message, music, animation | An attachment, with any caption as the attachment caption |
| Stickers and other messages | An "Unsupported message" entry; the bot also tells the customer this type of message isn't supported |

Messages the customer quotes appear as quotes in the conversation. The customer's Telegram name and avatar are synced to the contact; the avatar syncs at most once a day.

## Account linking and identification

With a direct connection, once your business system confirms that a Telegram account belongs to a user, mark the account as verified through the [channel identity endpoint](/docs/en/integrations/channels/identity-verification/#bind-channel-identities), using the Telegram user ID as the external ID. Later messages from the customer keep that verification.

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
