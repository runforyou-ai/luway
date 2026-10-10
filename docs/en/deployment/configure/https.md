---
title: HTTPS and reverse proxies
order: 4
---

Enable HTTPS for a deployment, either with servers that serve HTTPS directly and manage the certificate, or with a reverse proxy or load balancer in front of the servers.

## The deployment address decides whether HTTPS is used

When the deployment address starts with `https://`, every app reaches the deployment over HTTPS and website visitor cookies are marked `Secure`. When it starts with `http://`, HTTP is used. Change the deployment address under **Settings → Platform → Deployment → Address & HTTPS**; see [Deployment address and name](/docs/en/deployment/configure/address/).

Which component provides HTTPS depends on each server's startup configuration:

- A server with `server.httpsPort` set serves HTTPS on that port directly, using the certificate in the deployment settings. See [Serve HTTPS directly](#serve-https-directly).
- A server without it listens only on `server.port`, and a reverse proxy or load balancer in front of it terminates HTTPS and forwards requests. See [Reverse proxy requirements](#reverse-proxy-requirements).

## Serve HTTPS directly

When a server faces the internet directly, have `server.port` listen on port 80, set the HTTPS port, then restart:

```bash
{{slug}}-server config set server.port 80
{{slug}}-server config set server.httpsPort 443
```

On Linux, regular users can't listen on ports below 1024; grant the program the `CAP_NET_BIND_SERVICE` capability, or have your process manager grant it. For containers, set both fields in the mounted configuration file and map host ports 80 and 443 to them. See [Configuration reference](/docs/en/deployment/configure/configuration/#server).

Once the HTTPS port is set:

- The HTTPS port uses the deployment address's certificate and serves the same routes as `server.port`.
- When the deployment address uses HTTPS, HTTP requests for the deployment address's domain redirect to the same path on the deployment address. Requests by IP address aren't redirected.
- `server.port` answers certificate validation requests, which the ACME service sends to public port 80. If `server.port` isn't 80, forward public port 80 to it at the host or firewall.

Several servers can serve HTTPS directly at the same time behind DNS round robin or a TCP-only load balancer. They share one certificate; see [Running multiple servers](/docs/en/deployment/operate/multi-server/).

## Certificates

When a server that serves HTTPS directly is online and the deployment address uses HTTPS, the **Address & HTTPS** tab shows **Certificate source** and the current certificate's domains and expiry.

### Automatic

With **Automatic** selected, saving the deployment address first issues a Let's Encrypt certificate for its domain, and the address is saved only after issuance succeeds. Entering an HTTPS deployment address during first-time setup also issues a certificate first. Issuance requires that:

- the deployment address uses a domain name, not an IP address;
- the domain points to the servers that serve HTTPS directly;
- `server.port` on those servers is reachable from the internet on port 80.

If issuance fails, nothing is saved and the page shows the reason Let's Encrypt returned. Certificates renew automatically 30 days before they expire; the deployment checks every 6 hours. If renewal fails, **Current certificate** shows when and why, and the next check retries.

### Upload

For internal deployments that Let's Encrypt can't reach, or when you use a company CA or a wildcard certificate, choose **Upload** and paste the PEM certificate chain and private key. Put the server certificate first, followed by intermediates. The certificate must match the private key, must not be expired, and must cover the deployment address's domain. Uploaded certificates don't renew automatically; upload a new one before it expires.

Every server uses the new certificate within 10 seconds, without a restart.

## Reverse proxy requirements

When a reverse proxy or load balancer sits in front of the servers:

- leave `server.httpsPort` unset and forward requests to each server's `server.port`;
- keep the request's `Host` header;
- set the deployment address to the proxy's public HTTPS address;
- realtime uses long-lived connections; support `/nats` WebSocket Upgrade and set the idle timeout above two minutes, preferably 600 seconds;
- overwrite a header with the visitor's address (for example, `X-Real-IP` set by Nginx, or Cloudflare's own `CF-Connecting-IP`) and enter it in [`server.clientIPHeader`](/docs/en/deployment/configure/configuration/).

The server rate limits sign-in, sign-up, website visitor messages and uploads, and help center search by the visitor's IP. Requests over the limit get "Too many attempts. Try again later." The proxy must overwrite any header of the same name sent by the visitor, or visitors can forge an IP to get around the limits. When `server.clientIPHeader` is empty, or a request lacks the header or carries an invalid address in it, the server uses the connection's peer address, so every request through the proxy shares the proxy's IP and quickly hits the limits. A server that serves HTTPS directly needs no setup: the connection's peer is the visitor.

## Proxy configuration examples

Nginx:

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    server_name support.example.com;
    ssl_certificate     /etc/nginx/certs/support.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/support.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

Caddy:

```text
support.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

## Change the deployment address when the admin console is unreachable

If a wrong deployment address or certificate setting keeps you out of the admin console, run this on any server:

```bash
{{slug}}-server public-url http://support.example.com
```

The command keeps the current certificate source and, for automatic certificates, issues one first for an HTTPS address. It can't replace an uploaded certificate: for a new HTTPS domain, switch to an HTTP deployment address first, then upload the certificate in the admin console. Servers in the deployment use the new address within 10 seconds.

Member, website visitor and computer realtime connections use `/nats` on the deployment address. The Go server includes a proxy for self-hosting. Nginx or a load balancer may route that same path directly to the external NATS WebSocket listener, preserving Upgrade, Connection and Host. Sticky sessions are unnecessary. Set the idle timeout above the NATS two-minute ping interval, preferably 600 seconds.
