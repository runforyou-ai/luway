---
title: Client distribution
order: 3
---

Provide desktop and mobile installs and updates to members.

This page is being written.

## Download page

Members get desktop installers that match the server version from the download page. It is at `/en/download/` under the deployment address (`/zh-cn/download/` in Chinese), and both the product home page and the docs header link to it. **Use in the app** in the web app and the update prompt shown by outdated clients also open this page.

The server reads installers and update packages from the client directory, set by `clients.directory` in the configuration file or the `CLIENTS_DIRECTORY` environment variable, and `data/clients` by default. The directory's `clients.json` lists the installers and update packages for each platform with their sizes and checksums. These files sit next to it and download from `/clients/<file name>`.

- The container image includes desktop installers and update packages for the same version, with `/clients` as the client directory. No extra configuration is needed.
- For binary deployments, put `clients.json` from the release and the files it lists into the client directory.
- The server reads the client directory at startup. If `clients.json` is missing or its version differs from the server version, the download page shows that this server provides no installers. If a listed file is missing, or its size or checksum does not match, the server does not start.
- Mobile apps are not available for download yet, and the download page shows them as coming soon.

## Desktop updates

The desktop app updates from the server it is connected to. After you upgrade the server, members' desktop apps update to the same version.

- The desktop app checks once after it starts and every 6 hours after that. Members can also choose **Check for updates** in the user menu. When a new version is found, members are asked first, and the download starts only after they confirm.
- On macOS and Windows, the app downloads, verifies and installs the update, then restarts. On Linux, or when the current user cannot write to the folder the app is installed in (for example a system-wide install without administrator rights), the app announces the new version and opens the download page so members can install it themselves.
- Update packages are signed by the release pipeline. The desktop app verifies them with a public key built into it and refuses to install a package whose signature is invalid.
- If the client directory has no `clients.json`, or its version differs from the server version, the desktop app does not offer updates.

## Mobile apps

## Code signing
