---
title: Email
order: 6
---

Platform administrators can enable email under Platform admin → System settings → Email. Select a provider, enter the sender address and provider settings, then save. The configuration applies to the whole deployment, and all servers pick up changes within 10 seconds.

## Providers

| Provider | Settings |
| --- | --- |
| SMTP | Host, port, encryption; username and password when authentication is required |
| Amazon SES | AWS region, Access Key ID, Secret Access Key |
| Mailgun | Sending domain, US or EU region, API Key |
| Postmark | Server Token; an empty message stream uses outbound |
| Resend | API Key |

SMTP defaults to port 587 with STARTTLS. SSL/TLS and unencrypted connections are also available. Leaving the username blank disables authentication and clears the saved password.

Before using a provider API, verify your sending domain or sender address with the provider and ensure your account has permission to send email. Amazon SES uses the SES v2 API. The region must match the verified identity; sandbox accounts are also subject to recipient verification restrictions. API providers use their official endpoints.

Leave the sender name blank to use the name supplied by each invitation or notification. Enter a name to use it for all messages. Each delivery attempt has a 30-second timeout.

## Saving and testing

Reading the configuration never returns credentials in plain text. When a credential is marked as already set, leave the input blank to keep it or enter a new value to replace it. Changing the SMTP host or username, or the SES Access Key ID, requires entering the corresponding credential again.

Switching providers saves only the selected provider's settings and removes the previous provider's settings and credentials. Disabling email preserves the current configuration. Required fields must be complete before enabling it again.

After saving, select Send test email, enter your address in Test recipient in the dialog, and send. Save any pending changes first. Success means the mail service accepted the message; check your inbox and spam folder. Authentication failures, sending limits, rejected messages and timeouts are reported separately.

Credentials are stored in the deployment database. Protect database and backup access as you would other service credentials. Diagnostics and delivery errors do not include credentials or message content.

> [!NOTE]
> After upgrading from the SMTP-only configuration, configure and enable email again on this page. Update the server and clients together.

## Features that use email

- Member invitations are sent to the invited email address. When email is disabled, only an invitation link is provided for manual sharing.
- Website visitors can leave an email address after requesting a human agent. When an agent replies after the visitor leaves, the server sends a combined notification with a link back to the conversation. When email is disabled, the handoff prompt does not request an email address, and queued notifications wait until email is enabled again.
