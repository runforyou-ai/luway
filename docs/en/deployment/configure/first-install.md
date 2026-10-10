---
title: First-time setup
order: 2
---

Create the platform administrator and the first workspace.

## Initialize

Complete first-time setup in a browser. Open the deployment address and go to the sign-in entry. The setup form appears when the server has not been initialized.

First check the deployment address. The form prefills it with the address your browser is using; it is the address members and customers use to reach this server, and you can change it later in platform settings. See [Deployment address and name](/docs/en/deployment/configure/address/). When the server serves HTTPS directly and the address starts with `https://`, a certificate is issued automatically for its domain after you submit; see [HTTPS and reverse proxies](/docs/en/deployment/configure/https/#automatic). Then enter a workspace name, your name, email, and password, and submit. Workspace names can use Chinese characters and must be unique within the deployment, ignoring case. Names are limited to 32 characters, and surrounding whitespace is removed when saving. Passwords require at least 8 characters.

After setup, you are signed in and enter the first workspace. The server generates its access address, which you can view and copy in general settings. Renaming a workspace keeps its address unchanged.

## Platform administrator

The account created during setup is a platform administrator and joins the first workspace. Platform administrators manage platform accounts, workspaces, and sign-up and workspace creation policies.

## Sign-up and workspace creation

After setup, sign-up is invitation-only and only platform administrators can create workspaces. Platform administrators can change both policies under **Settings → Platform → Workspaces & accounts → Sign-up & creation**. See [Accounts and workspaces](/docs/en/deployment/manage/accounts-workspaces/).

Desktop and mobile clients connect to an initialized server before signing in.
