---
title: WeChat Official Account (authorized access)
order: 5
---

An Official Account administrator scans to authorize a verified service account for the deployment's WeChat third-party platform, so you can serve customers without entering account credentials. If the deployment has no WeChat Open Platform configured, use [key access](/docs/en/integrations/channels/wechat-official-account-key/) instead.

This page is being written.

## Prerequisites

- A platform administrator has configured the third-party platform under **Settings → Platform → Deployment → WeChat Open Platform** and its status is **Available**. See [WeChat Open Platform](/docs/en/deployment/manage/wechat-open-platform/). Authorized-access channels can't be added before it is configured.
- A verified service account, and an Official Account administrator who can scan with WeChat to authorize it. Subscription accounts and unverified service accounts can't be authorized.
- An Official Account can be authorized for only one authorized-access channel in a deployment. The same account can also be connected to one key-access channel.

## Authorize the Official Account

1. On the channel's **Connection** tab, select **Continue to WeChat**. The authorization page opens in your browser.
2. On that page, select **Continue to WeChat**. The Official Account administrator scans with WeChat, chooses the account, and confirms the permissions.
3. When authorization completes, the browser shows the result and the **Connection** tab updates with the account's name, avatar, and owner.

Keep the message management, user management, and material management permissions when authorizing. If any is missing, the channel still connects and the **Connection** tab lists the missing permissions; authorize again with them selected.

Once a channel is authorized, it can't switch to a different Official Account, and reauthorizing only accepts the same account. Create a new channel for a different account.

## Authorization status

| Status | Meaning and action |
| --- | --- |
| Not authorized | The channel hasn't been authorized yet |
| Authorized | The authorization is valid; **API credential** shows the status of the account's API credential |
| Authorization revoked | An administrator revoked the authorization in the WeChat Official Account Platform; select **Authorize again** |
| Reauthorization required | The deployment switched to a different WeChat third-party platform; select **Authorize again** |

When the API credential shows **Failed**, the reason is displayed; fix it and select **Check again**.

## Receiving messages

After authorization, the WeChat third-party platform forwards messages sent to the Official Account to {{product}}. While the channel is enabled and the authorization is valid, messages customers send to the Official Account are written to their conversations and assigned according to the channel's reception settings. Messages received while the authorization is revoked or requires reauthorization aren't written.

| Message the customer sends | Content written to the conversation |
| --- | --- |
| Text | A text message |
| Image, voice, video, short video | An attachment; voice is saved as the original audio file from WeChat |
| Location | Text with the location name and coordinates |
| Link | Text with the title, description, and URL |

Follows, QR code scans, and menu clicks aren't written to conversations; they only open a reply window, see [Message replies](#message-replies). WeChat doesn't provide users' nicknames or avatars to Official Accounts, so customers appear in Contacts by number. Only one of the key-access and authorized-access channels for the same Official Account can be enabled at a time; after switching between the two connection methods, the same customer still maps to one contact.

## Message replies

Replies from handlers and AI employees in a conversation are sent to the customer as Official Account customer service messages. WeChat only allows replies for a period after the customer interacts with the Official Account:

| Customer interaction | Reply period | Messages allowed |
| --- | --- | --- |
| Sends a message | 48 hours | 5 |
| Follows the Official Account, scans a parameterized QR code, or clicks a menu | 1 minute | 3 |

The conversation composer shows the reply deadline and the number of messages left. After the period ends or the messages are used up, replies can't be sent until the customer sends another message or interacts again.

- Text: up to 2,048 bytes per message, about 680 Chinese characters. AI employee replies over the limit are split into several messages, each counted toward the messages allowed; if too few messages are left for all parts, the reply isn't sent.
- Images: JPG, PNG, or GIF, up to 10 MB.
- Voice: AMR or MP3, up to 2 MB.
- Attachments can't include a caption. Quoting messages and sending other file types aren't supported.

## Account linking
