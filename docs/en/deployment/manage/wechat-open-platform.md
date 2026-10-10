---
title: WeChat Open Platform
order: 9
---

Configure the WeChat third-party platform used by this deployment, check its credential status, and publish it to all accounts. Only platform admins can open this page.

> [!NOTE]
> The deployment operator registers and creates the third-party platform on [WeChat Open Platform](https://open.weixin.qq.com/). Each third-party platform serves only one deployment.

## Enter the platform credentials

Under **Settings → Platform → Deployment → WeChat Open Platform → Platform credentials**, enter the following values from the development settings of your third-party platform, then save:

| Field | Description |
| --- | --- |
| Component AppID | The third-party platform's AppID, starting with `wx` |
| Component AppSecret | The third-party platform's AppSecret |
| Message token | 3 to 32 letters or digits. You can click **Generate** |
| Message encryption key | 43 letters or digits. You can click **Generate** |

The message token and message encryption key must match what you entered on the third-party platform exactly.

## Enter the access settings and IP whitelist

After you save the platform credentials, the page shows **Access settings**. Copy the authorization page domain, authorization event URL, and message and event URL into the development settings of the third-party platform. Save on this page before configuring WeChat, so the verify ticket WeChat pushes when you save its development settings can be received.

**IP whitelist** lists each server's egress IP. Add all of them to the third-party platform's whitelist. Each server's egress IP comes from [`server.egressIP`](/docs/en/deployment/configure/configuration/) in its configuration. If a server shows **Egress IP not set**, add it to that server's configuration and restart it.

## Check the platform status

WeChat pushes a verify ticket to the authorization event URL every 10 minutes. Once a ticket arrives, the server gets the platform credential automatically and renews it with the latest ticket before it expires.

| Status | Meaning and what to do |
| --- | --- |
| Waiting for WeChat to push a verify ticket | No ticket yet. Confirm the authorization event URL, message token and message encryption key are set correctly, then wait for the next push |
| Getting the platform credential | A ticket has arrived, but the platform credential hasn't been obtained yet or has expired. The server fetches it again automatically; you can also click **Check again** |
| Available | The platform credential is valid |
| Couldn't get the platform credential | Follow the message, then click **Check again**. If an egress IP isn't on the whitelist, add the IP shown to the third-party platform's whitelist |

## Choose permission sets and publish to all accounts

In the third-party platform's **Permission sets**, select the Official Account message management, user management, and material management permissions. Don't select Mini Program permissions; {{product}} doesn't answer the Mini Program release checks.

Before the third-party platform is published to all accounts, only Official Accounts in the **Authorization test account list** can authorize it. Add the Official Account you want to connect to that list to test first.

Once the platform status is **Available**, submit the platform for full publication. WeChat checks it automatically with dedicated test Official Accounts, and {{product}} answers automatically: the text message and event checks get the expected reply directly, and the customer service message check exchanges the pushed authorization code for an authorization and replies through the customer service message API. Messages from test accounts aren't written to any conversation.

> [!NOTE]
> After an Official Account authorizes the third-party platform, WeChat pushes each message to both places if the server configuration in the Official Account Platform is still enabled. Only one of the [key-access](/docs/en/integrations/channels/wechat-official-account-key/) channel and the authorized-access channel for the same Official Account can be enabled at a time; disable one before enabling the other.

## Replace the third-party platform

Change the Component AppID and save to switch to a different third-party platform. The previous platform's verify ticket and credential are cleared, and you need to wait for the new platform to push a verify ticket. Authorized [authorized-access Official Account](/docs/en/integrations/channels/wechat-official-account-authorization/) channels need to be authorized again.
