---
title: File storage
order: 5
---

Store files in a local directory or S3-compatible object storage.

File storage is shared by the whole deployment. Platform administrators change it under **Settings → Platform → Deployment → File storage**, and every server picks up the change within 10 seconds.

## Local storage

Without object storage, new files are saved in `files` under the server's data directory, which you can set with `data.directory`. See [Configuration reference](/docs/en/deployment/configure/configuration/#data-directory). Files are downloaded through the server, and the deployment can run only one server.

## Object storage

With object storage on, clients upload new files straight to the bucket using presigned requests issued by the server, and read files directly from the bucket. The server doesn't relay file contents. Fill in:

| Field | Description |
| --- | --- |
| Endpoint | The address the server uses to call the object storage API |
| Public address | The address members' and customers' devices use to read files. Can be the bucket address or a CDN |
| Region, Bucket | The bucket's region and name |
| Access key ID, Secret access key | Credentials that can read and write the bucket |
| Path-style access | Access the bucket as endpoint/bucket. Self-hosted services such as MinIO usually need this |

When you save, the server first accesses the bucket with the credentials you entered, and doesn't save if it can't. The whole deployment shares one bucket, with object keys separated by workspace.

## Bucket CORS

Browsers and the desktop app upload and read files directly from the bucket, so the bucket must allow cross-origin `PUT` and `GET` requests from the deployment address.

## Switching storage

Switching affects only new files. Existing files are read from wherever they were saved. After you turn object storage off, files uploaded earlier are still read from the bucket, so keep the bucket and its credentials.

While several servers run in the deployment, object storage can't be turned off. See [Multiple servers](/docs/en/deployment/operate/multi-server/).

## Temporary file cleanup

A file is uploaded as a temporary file as soon as it's chosen, and is put to use only when the business data is saved. Unused temporary files expire after 24 hours and are deleted by the server on a schedule.
