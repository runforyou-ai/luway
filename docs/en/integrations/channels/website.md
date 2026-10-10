---
title: Website widget
order: 1
---

Embed the support widget on your website so visitors can talk to AI employees and agents.

This page is being written.

## Chat window

To prevent abuse, when one visitor or one IP sends too many messages or uploads too many files in a short time, the chat window shows "Too many attempts. Try again later." The visitor can continue after a short wait. With multiple conversations turned on, a visitor who already has 3 ongoing conversations without a reply from an agent or AI employee can't start another one, but can keep writing in existing conversations.

## Home and help center

## Embed on your site

## Allowed websites

## Open from your own button

## Chat link and QR code

## Email follow-up for visitors

## Realtime connections and identity recovery

The realtime client loads when the chat window opens. Returning anonymous visitors recover their identity through a channel-specific HttpOnly cookie; signed-in customers use the identity token supplied by the website. Once subscriptions are ready, including after reconnecting, the window reloads conversations and periodically checks messages. Closing the window cancels the connection and recovery requests. Switching website accounts rebuilds the window for that identity and ends the previous connection and message requests. The server forcibly closes invalid connections when a channel is disabled, a workspace is suspended or the customer identity key is reset.
