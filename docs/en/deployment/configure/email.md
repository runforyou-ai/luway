---
title: Email
order: 6
---

Configure SMTP to send invitations and notifications.

## SMTP settings

Email delivery is shared by the whole deployment. Platform administrators enter the SMTP host, port, encryption (STARTTLS, SSL/TLS, or none), and from address under **Settings → Platform → Deployment → Email**, plus a username and password when the server requires authentication. Every server uses the new settings within 10 seconds of saving.

Leave the SMTP host blank to turn email delivery off.

## Features that use email

- When you invite a member, the server emails the invitation. Without email delivery, you get only the invitation link to forward yourself.
- After a website visitor is handed off to a human, they can leave an email address. If they leave and an agent replies, the server sends a combined email notification with a link back to the conversation. Without email delivery, the handoff message doesn't ask for an email, and notifications already queued are kept until email delivery is configured.
