---
title: Branding
order: 4
---

Use your own deployment brand while a license is active.

## What you can change

Under **Platform admin → System settings → Site settings**, platform administrators can change the product name per UI language, the global object name for the website embed script, and a PNG that replaces the web favicon. Field rules are in [Branding](/docs/en/deployment/configure/branding/).

Installer names, app identifiers, and client icons come from the build-time brand and are not changed by deployment branding. Full white-label packages use a branded container image, not this page.

## License requirements

Branding is editable and takes effect only while a license is active. Without a license or after expiry, branding fields are read-only and show build-time defaults. See [License](/docs/en/deployment/manage/license/).

## Where changes apply

All servers pick up the new brand within about 10 seconds; open pages update after refresh. When the license expires, the build-time brand returns automatically.
