---
title: Customer identity verification
order: 3
---

Pass signed-in users from your business system to {{product}} with an identity secret.

This page is being written.

## Identity secret

## Sign the identity

## Sign in and sign out

## Bind channel identities

Messages that arrive through channels such as a direct Telegram connection carry no signed identity. After your business system confirms that a channel account belongs to a user (for example, through a one-time linking link), call the channel identity endpoint to mark the account as verified. When the user unlinks the account, call the same URL to revoke it.

```http
PUT /api/public/channels/<channel ID>/identities/<external ID>/verification
Authorization: Bearer <signed identity>
```

```http
DELETE /api/public/channels/<channel ID>/identities/<external ID>/verification
Authorization: Bearer <signed identity>
```

- The external ID is the customer's ID in the channel, such as the Telegram user ID or the Official Account user's OpenID.
- Sign the identity with the customer identity secret using HS256. Set `sub` to the user ID in your business system and include `channel_id` (the channel ID) and `external_id` (the external ID), both matching the request URL. Write `name`, `email`, `attributes`, and `tags` the same way as for the website widget: `name` becomes the customer's name, and values in `attributes` whose names match customer fields are saved to the customer profile, such as the agent the customer belongs to.
- Once verified, the account's conversations move to the customer profile that user already has. This also works before the account has sent any message.
- Revoking only takes effect when the account is currently verified as the user in `sub`.
- With business system forwarding and the website widget, every message carries its own signed identity and updates the verification, so this endpoint is not used. Channels connected through business system forwarding return 404.

| Status | Meaning |
| --- | --- |
| 204 | Done |
| 400 | The channel ID or external ID is malformed |
| 401 | The signed identity is missing, invalid, expired, or does not match the request URL |
| 404 | The channel does not exist, is disabled, or does not support this endpoint |

## How verified identities are used
