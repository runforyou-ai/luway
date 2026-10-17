---
title: License activation
order: 1
---

Activate and maintain the license on your server.

After first-time setup, the server registers this deployment’s ID and public key with the official support service. An activated license is bound to that deployment ID. The deployment ID is stored in the database. When you move servers, migrate the database and data directory together (or restore a full backup) so the deployment ID stays the same.

## Online activation

Under **Platform admin → License**, choose **Online activation** and enter the activation code from your purchase. The server must reach the official support service. On success, license status updates immediately: you can raise self-created workspace quota under sign-up settings, and deployment branding becomes editable.

## Offline licenses

If the server can’t reach the official support service, give your vendor the deployment ID, obtain a license code issued for that ID, and paste it under **Offline license**. With an existing license, use **Replace license code** to paste a newly issued code (its issue time must be later than the current license).

## License refresh

The license page shows the customer name, license ID, and expiry. Near expiry, the page and dashboard remind you to renew. Renewal means your vendor issues a new license code for the same deployment ID; paste it with **Replace license code**. The new code’s issue time must be later than the current license. Activating the same activation code again while the license is still valid returns the same license code and does not extend expiry.

## Server migration

Migrate the database and data directory together so the deployment ID and activated license remain. Replacing only the program or image while keeping the database and data directory does not require reactivation. Re-initializing or using an empty database creates a new deployment ID and requires a new license.
