---
title: Client distribution
order: 3
---

Provide desktop installs and updates to members. Mobile installers are not included in the current release.

## Download page

Members get desktop installers that match the server version from the download page, and administrators download the headless executor from its **Shared computer executor** section. It is at `/en/download/` under the access address (`/zh-cn/download/` in Chinese), and both the product home page and the docs header link to it. **Use in the app** in the web app and the update prompt shown by outdated clients also open this page.

Each release includes a release manifest, `release.json`, which lists the version's server archives, desktop installers, update packages, and executor archives for each platform with their sizes and checksums. The server reads the release manifest in its program directory and serves the client files from the `clients` subdirectory of the same directory at `/clients/<file name>`.

- The container image includes the release manifest and client files of the same version, with no extra configuration.
- When you run the program directly, `{{slug}}-server download-clients` downloads the release manifest and client files that match the program's version from the release, verifies their sizes and checksums, and puts them in the program directory. If the server can't reach the release, pass a directory downloaded in advance with `--from`. See [Standalone program](/docs/en/deployment/install/standalone/#download-client-files).
- The server reads the release manifest at startup. If there is no manifest or its version differs from the server version, the download page shows that this server provides no installers or executors, and desktop apps don't update from this server. If the manifest is invalid, a listed file is missing, or its size or checksum does not match, the server does not start.
- Desktop packages cover Windows amd64, macOS arm64, Linux amd64, and Linux arm64. Mobile apps are not available for download yet, and the download page shows them as coming soon.

## Desktop updates

The Windows and macOS desktop apps update from the server they connect to, so the app version always follows the server version. After you update the server, there's nothing else to publish.

- Each time the desktop app connects, it reads the update manifest at `/clients/update`. When the server is newer than the app, the app downloads the update package for its platform in the background, checks its SHA-512 checksum, and prompts the member to restart. Update packages do not use a separate update signature; the app trusts the manifest and checksums from the server it is connected to.
- When the server no longer supports the app, the **Update required** page downloads the new version, and the app restarts once the member confirms. If the app can't replace itself (see the Linux and folder permission note below), the page opens the download page instead.
- The app never installs an update package whose contents don't match the checksum in the update manifest. It never updates to an older version.
- The Linux desktop app, and Windows or macOS desktop apps whose folder the current user can't write to (for example a system-wide install without administrator rights), don't replace themselves. When a new version is available, they prompt the member with **Go to downloads** to install it from the download page.

## Mobile apps

Mobile apps and the official push relay are not part of the current release. Members should use the web or desktop app. The download page and docs will be updated when they become available.

## Code signing

The macOS desktop app and headless executor are signed with Developer ID and submitted to Apple for notarization. The DMG and the app inside the update ZIP carry notarization tickets; command-line executables do not support stapling. Windows installers are signed with a code-signing certificate in the release pipeline. That OS-level code signing is separate from the SHA-512 checksum check used for desktop updates.
