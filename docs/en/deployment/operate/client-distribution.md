---
title: Client distribution
order: 3
---

Provide desktop and mobile installs and updates to members.

This page is being written.

## Download page

Members get desktop installers that match the server version from the download page. It is at `/en/download/` under the deployment address (`/zh-cn/download/` in Chinese), and both the product home page and the docs header link to it. **Use in the app** in the web app and the update prompt shown by outdated clients also open this page.

The server reads installers and desktop update packages from the client directory, set by `clients.directory` in the configuration file or the `CLIENTS_DIRECTORY` environment variable, and `data/clients` by default. The directory's `clients.json` lists the installers and update packages for each platform with their sizes and checksums. These files sit next to it and download from `/clients/<file name>`.

- The container image includes desktop installers and update packages for the same version, with `/clients` as the client directory. No extra configuration is needed.
- For binary deployments, put `clients.json` from the release and every file it lists into the client directory.
- The server reads the client directory at startup. If `clients.json` is missing or its version differs from the server version, the download page shows that this server provides no installers, and desktop apps don't update from this server. If a listed file is missing, its size or checksum does not match, or an update package has an invalid signature, the server does not start.
- Mobile apps are not available for download yet, and the download page shows them as coming soon.

## Desktop updates

The Windows and macOS desktop apps update from the server they connect to, so the app version always follows the server version. After you update the server and its client directory, there's nothing else to publish.

- Each time the desktop app connects, it reads the update manifest at `/clients/update`. When the server is newer than the app, the app downloads the update package for its platform in the background, checks its signature, and prompts the member to restart.
- When the server no longer supports the app, the **Update required** page downloads the new version, and the app restarts once the member confirms. If the app can't replace itself (see the Linux and folder permission note below), the page opens the download page instead.
- The release process signs each update package, and the signature covers both the version number and the package contents. The app checks it with its built-in public key and never installs a package whose signature is invalid or whose contents don't match it, so a server can't present an old package as a new version. The app never updates to an older version.
- The Linux desktop app, and Windows or macOS desktop apps whose folder the current user can't write to (for example a system-wide install without administrator rights), don't replace themselves. When a new version is available, they prompt the member with **Go to downloads** to install it from the download page.

## Mobile apps

## Code signing
