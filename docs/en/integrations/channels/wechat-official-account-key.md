---
title: WeChat Official Account (key access)
order: 6
---

Connect a verified service account with its own AppID and AppSecret to serve customers. If the deployment has a WeChat Open Platform configured, you can use [authorized access](/docs/en/integrations/channels/wechat-official-account-authorization/) instead: an Official Account administrator scans to authorize, and no account credentials are entered.

This page is being written.

## Prerequisites

- A verified service account, and an administrator who can sign in to Settings & Development › Basic Configuration in the WeChat Official Account Platform.
- The deployer has set each server's egress IP (`server.egressIP`) in its server configuration. See the [configuration reference](/docs/en/deployment/configure/configuration/).
- An Official Account can be connected to only one key-access channel in a deployment. Once a channel is connected, its AppID can't be changed; create a new channel for a different Official Account.

## Connect the Official Account

Enter the Official Account credentials on the channel's **Connection** tab:

| Field | Value |
| --- | --- |
| AppID, AppSecret | The developer ID and developer password under Settings & Development › Basic Configuration |
| Token | 3 to 32 letters or digits; can be generated |
| Message encryption | Safe mode or plaintext mode; safe mode is recommended |
| EncodingAESKey | 43 letters or digits in safe mode; can be generated |

Before saving, add every server egress IP listed under **IP whitelist** on the **Connection** tab to the IP whitelist under Settings & Development › Basic Configuration. When you save, {{product}} uses the AppID and AppSecret to obtain an API credential from WeChat and saves only after it succeeds. If an egress IP isn't on the whitelist, the error names that IP.

## Connection status

| Item | Meaning |
| --- | --- |
| API credential | **Available** means the credential was last obtained successfully; when it is about to expire, it is renewed automatically before the next WeChat API call. **Failed** shows the reason, such as an egress IP missing from the whitelist or credentials rejected by WeChat; fix it and select **Check again** |

## Server configuration

After the Official Account is connected, the **Connection** tab shows **Server configuration**. In the Official Account Platform, under **Settings & Development › Basic Configuration › Server Configuration**:

1. For the server URL, enter the URL shown under **Server configuration**. You can select **Copy**.
2. Enter the same token, message encryption, and EncodingAESKey as the channel credentials.
3. Submit the configuration. WeChat verifies the server URL, and **URL verification** shows **Verified** with the verification time.
4. Enable the server configuration.

If you change the channel's token, **URL verification** returns to **Waiting for verification**. Change the token in the Official Account Platform to match and submit again.

> [!NOTE]
> Once the server configuration is enabled, messages sent to the Official Account are handled by {{product}}, and the auto-replies and custom menu set in the Official Account Platform stop taking effect. This channel can't be enabled while an authorized-access channel for the same Official Account is enabled.

## Receiving messages

While the channel is enabled, messages customers send to the Official Account are written to their conversations and assigned according to the channel's reception settings.

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
