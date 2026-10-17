---
title: License
order: 3
---

Activate a commercial license to unlock deployment branding and allow raising self-created workspace quota.

## Free and commercial licenses

Without a license, the platform keeps only the default workspace from setup, accounts cannot create workspaces, and branding fields on Site settings are read-only with build-time defaults. While a license is active, you may raise each account’s self-created workspace quota and edit deployment branding. See [Branding](/docs/en/deployment/configure/branding/).

## Activate a license

Under **Platform admin → License**, click **Activate license**. Choose online activation or offline licensing, enter the code, and submit. On success, the dialog closes and the page refreshes. Operational detail is in [License activation](/docs/en/deployment/operate/license/).

## License status

The page shows the deployment ID, status (not activated / active / expired), customer name, expiry, and license ID. Provide the deployment ID when requesting a license or contacting support.

## Renewal and changes

Near expiry, the license page and dashboard remind you. Renewal means your vendor issues a new license code for the same deployment ID; paste it with **Replace license code**. The new code’s issue time must be later than the current license. Activating the same activation code again while the license is still valid does not extend expiry.

## Expiry

When the license expires, self-created workspace quota locks to 0; existing workspaces stay available. Deployment branding reverts to the build-time brand, and branding fields become read-only. Restore by replacing with a new valid license code your vendor issued for the same deployment ID.
