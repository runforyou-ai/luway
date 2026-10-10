---
title: Docker
order: 1
---

Deploy the server with the container image.

## Prepare dependencies

The server needs PostgreSQL. Have its connection details ready before you run it. When several servers run, they also need NATS with JetStream enabled. See [Multiple servers](/docs/en/deployment/operate/multi-server/).

The image includes the release manifest and client files of the same version, so the download page offers desktop installers and executors directly. See [Client distribution](/docs/en/deployment/operate/client-distribution/).

## Run the container

The configuration directory inside the container is `/etc/{{slug}}-server`, and the configuration file is `server.yaml` in it. On the host, create a `config` directory and prepare `server.yaml` in it first. For all fields, see [Configuration reference](/docs/en/deployment/configure/configuration/):

```yaml
database:
  host: <database address>
  port: 5432
  user: {{slug}}
  password: <password>
  name: {{slug}}
  sslMode: disable
```

Then start the container with the configuration directory and a data volume mounted. Mount the whole directory so that `config set` in the container can rewrite the configuration file:

```bash
docker run -d --name {{slug}}-server --restart unless-stopped \
  -v ./config:/etc/{{slug}}-server \
  -v {{slug}}-data:/var/lib/{{slug}}-server \
  -p 8080:8080 \
  <image>:<version>
```

By default the server listens on `0.0.0.0:8080` inside the container. Complete the first-time setup soon after it starts. See [First-time setup](/docs/en/deployment/configure/first-install/).

To run a management command in the container, append it to the image's program `/server`, for example:

```bash
docker exec {{slug}}-server /server status
```

## Persistent data

The data directory inside the container is `/var/lib/{{slug}}-server`. When the deployment doesn't use object storage, local files are saved in `files` under it. Mount this directory as a volume so the files survive when the container is recreated. With object storage, files are stored in the bucket. See [File storage](/docs/en/deployment/configure/storage/).

## Ports and HTTPS

When a reverse proxy in front provides HTTPS, map only `server.port`. To serve HTTPS from the container directly, set `server.port` to `80` and `server.httpsPort` to `443` in the configuration file, and map host ports 80 and 443. See [HTTPS and reverse proxies](/docs/en/deployment/configure/https/).
