---
title: Client distribution
order: 3
---

Provide desktop and mobile installs and updates to members.

This page is being written.

## Download page

Members get desktop installers that match the server version from the download page. It is at `/en/download/` under the deployment address (`/zh-cn/download/` in Chinese), and both the product home page and the docs header link to it. **Use in the app** in the web app and the update prompt shown by outdated clients also open this page.

The server reads installers from the client directory, set by `clients.directory` in the configuration file or the `CLIENTS_DIRECTORY` environment variable, and `data/clients` by default. The directory's `clients.json` lists the installer for each platform with its size and checksum. Installers sit next to it and download from `/clients/<file name>`.

- The container image includes desktop installers for the same version, with `/clients` as the client directory. No extra configuration is needed.
- For binary deployments, put `clients.json` from the release and the installers it lists into the client directory.
- The server reads the client directory at startup. If `clients.json` is missing or its version differs from the server version, the download page shows that this server provides no installers. If a listed file is missing, or its size or checksum does not match, the server does not start.
- Mobile apps are not available for download yet, and the download page shows them as coming soon.

## Desktop updates

## Mobile apps

## Code signing
