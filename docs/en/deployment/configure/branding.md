---
title: Custom branding
order: 7
---

Replace the product name, icons, and client branding.

## Deployment branding

Platform administrators can set the following for the whole deployment under **Settings → Platform → Deployment → Branding**:

- The product name for each interface language. Languages left blank keep the build-time brand.
- The global object name the website embed script registers on the host page. It must start with a letter and contain only letters and digits.
- A PNG image, up to 512 KB, that replaces the web app's site icon.

Every server uses the new branding within 10 seconds of saving. Open pages update after a refresh.

## Build-time branding

Installer names, app identifiers, client icons, and other things fixed at build time come from the build-time brand. Deployment branding doesn't change them.

## License requirements

Deployment branding takes effect only while the license grants custom branding and hasn't expired. You can save it before then, and it takes effect once the license grants it. When the license expires, the build-time brand returns. See [License](/docs/en/deployment/operate/license/).
