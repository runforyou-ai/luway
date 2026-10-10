---
title: WeCom
order: 5
---

Let colleagues chat one-on-one with AI employees in WeCom. Each WeCom intelligent bot is one channel. The channel passes colleagues' messages to an AI employee that serves employees, and hands them to a colleague when needed.

## Create a bot

1. In the WeCom admin console, create an intelligent bot and choose long connection for receiving messages.
2. Note the bot's Bot ID and Secret.
3. Make the bot visible to the colleagues who will use it.

{{product}} connects to WeCom itself, so your server doesn't need a public callback URL.

## Connect to your workspace

1. In **Channels**, add a channel and choose **WeCom bot**.
2. In **Reception**, route new conversations to an AI employee whose audiences include **Employees**, and choose the team that takes over when the AI employee can't help.
3. On the **Connection** tab, enter the Bot ID and Secret. Once the status shows **Connected**, the bot is ready.

> [!NOTE]
> A bot can keep only one connection at a time. If the status shows **Bot connected elsewhere**, stop the other connection to this bot (for example, a test environment), then click **Reconnect**.

## Link members

AI employees only serve linked members:

- The first time a colleague messages the bot, they get a link. They open it, sign in with their workspace account and confirm. The link is valid for 15 minutes; after that, sending another message gets a new link.
- Admins can also link, change or unlink members on the channel's **Members** tab. The tab lists colleagues by their WeCom account ID. If the bot's creator isn't a WeCom super admin, WeCom provides encrypted account IDs; in that case, have colleagues link themselves through the link.
- After you switch to a different bot (change the Bot ID), all existing links are removed and colleagues need to link again.
- After unlinking, the colleague gets a new link the next time they send a message. After changing the member, new messages go to a conversation started by the new member.

Once linked, AI employees serve the colleague as that member, for example when querying business systems.

## Conversations and handoff

- Each colleague's private chat with the bot is one conversation. Find it on the Messages page by filtering by channel or by the **Employees** audience. It doesn't appear in the colleague's own chat list.
- Text, voice (using WeCom's transcription), images, videos and files are supported. Unsupported messages appear as "Unsupported message" in the conversation, and the colleague gets a notice.
- When someone mentions the bot in a group chat, the bot asks them to message it directly.
- When an AI employee needs the colleague to confirm an action, the bot sends a link. Opening it shows the action under **Pending** in {{product}}, where the colleague can review and confirm it within 24 hours. Actions that need approval from the AI employee's owner are handled by the owner in {{product}}.
- After an AI employee hands off, the handler replies on the Messages page and the reply goes to the colleague through the bot. Replies are sent as Markdown; attachments can't be sent yet.
