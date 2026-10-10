---
title: Deployment address and name
order: 3
---

Set the address members and customers use to reach the deployment, the deployment name shown when clients connect, and the platform time zone.

## Deployment address

The deployment address is the address every client connects to and the web app is served from, such as `https://support.example.com`, without a path. Links the server generates start with it, including invitation links, website visitor return links, channel callback addresses, and file addresses.

During first-time setup, the form prefills the deployment address with the address your browser is using. Check it and submit; see [First-time setup](/docs/en/deployment/configure/first-install/). Afterwards, platform administrators can change it under **Settings → Platform → Deployment → Address & HTTPS**, and every server uses the new address within 10 seconds. Links already sent keep the old address.

A deployment address that starts with `https://` uses HTTPS. When servers serve HTTPS directly, the same tab sets the certificate source, automatic or uploaded. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).

## Deployment name

The deployment name appears on the server connection screen of the desktop and mobile apps, so members can confirm they reached the right deployment. It can be up to 64 characters. When blank, the deployment address is shown instead.

## Platform time zone

The platform time zone decides which day operations data is counted under. First-time setup uses the platform administrator's time zone. Platform administrators change it under **Settings → Platform → Deployment → Basics**. After you save, all historical operations data is recounted in the new time zone, and **Overview** shows that it is recounting until it finishes. See [How metrics are counted](/docs/en/deployment/manage/overview/#how-metrics-are-counted).

**Basics** also has the **Report runtime metrics and errors** switch; see [Reporting settings](/docs/en/deployment/manage/diagnostics/#reporting-settings).
