---
title: First-time setup
order: 2
---

Create the platform administrator account and open the platform admin console.

## Initialize

Complete first-time setup in a browser. Open the access address and go to the sign-in entry. The setup form appears when the server has not been initialized.

First check the access address. The form prefills it with the address your browser is using; it is the address members and customers use to reach this server, and you can change it later in the platform admin console. See [Access address and name](/docs/en/deployment/configure/address/). When the server serves HTTPS directly and the address starts with `https://`, a certificate is issued automatically for its domain after you submit; see [HTTPS and reverse proxies](/docs/en/deployment/configure/https/#automatic). Then enter the default workspace name, your name, email, and password, and submit. Passwords require at least 8 characters.

After setup, the default workspace is created, a platform admin session is opened, and you enter the admin console. The administrator belongs to that default workspace.

## Platform administrator

The account created during setup is a platform administrator. Platform administrators sign in to the admin console to manage platform accounts, workspaces, sign-up policy, and self-created workspace quota, and can open any workspace with support access.

## Sign-up and self-create quota

After setup, sign-up is invitation-only and self-create quota is 0. Platform administrators can change sign-up policy under **Platform admin → System settings → Site settings**, and raise quota after activating a license. See [Accounts and workspaces](/docs/en/deployment/manage/accounts-workspaces/).

Desktop and mobile clients connect to an initialized server before signing in to the user app. Use the web app admin entry for the platform admin console.
