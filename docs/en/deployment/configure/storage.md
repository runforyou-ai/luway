---
title: File storage
order: 5
---

{{product}} stores files in S3-compatible object storage. After installation, a platform administrator must configure **Platform admin → System settings → File storage** before uploading attachments, avatars, or knowledge documents. All servers share this configuration and pick up changes within 10 seconds.

## Upload and access addresses

| Field | Description |
| --- | --- |
| Upload endpoint | Full S3 API address, reachable from both servers and client devices; a container-only address is insufficient |
| File access address | Stable file URL base, including a CDN or custom domain such as `https://cdn.example.com/assets` |
| Region, bucket | S3 region and bucket name |
| Access key ID, secret access key | Server credentials with object read, write, delete and multipart create, list, upload, complete, and abort permissions |
| Path-style access | Access the S3 API as “endpoint/bucket”; commonly required by self-managed S3 services |

File URLs are “file access address/object key”. The bucket name is not added automatically. For example, `https://cdn.example.com/assets` and `workspaces/…/files/….png` produce `https://cdn.example.com/assets/workspaces/…/files/….png`. A direct path-style bucket URL must already include the bucket name.

Clients upload directly using short-lived signed requests. For files larger than 100 MiB, Uppy uploads parts and requests completion directly from S3. The server verifies the actual size and file type before confirming the upload. Ordinary files have stable URLs without expiration, so chat history and AI references survive the expiration of upload signatures. Download clicks may request a short-lived URL that preserves the original filename; this temporary URL is not saved in chat history.

## Save validation and access

Before saving, the server creates a random test object and verifies authenticated writes, reads, deletion, and anonymous retrieval of the exact content through the file access address. Any failed step prevents saving.

Use a private bucket with a CDN authorized to read from it and anonymously serve ordinary files. If the test object is also anonymously readable from the S3 origin, saving is allowed with a warning recommending a CDN and restricted origin access. Binding a CDN domain does not itself make the origin private. An inconclusive origin probe also produces a warning; this probe does not certify permissions for the entire bucket.

Ordinary files live under `workspaces/*/files/*` and knowledge document originals under `workspaces/*/knowledge-documents/*`; anyone holding a link can read them. Chat history, AI references and knowledge previews all use the stable “file access address/object key” form and do not expire with upload signatures. Do not make the entire bucket publicly readable or allow anonymous writes.

## CORS

Browsers and desktop clients contact S3 directly. Allow application origins to use `GET`, `PUT`, `POST`, `DELETE`, and `HEAD`; allow required `Content-Type`, `Cache-Control`, and `x-amz-*` request headers; expose `ETag` to the browser. Include the desktop WebView origins in the allowed origins. The file access domain must also allow cross-origin reads from the application.

The connection test runs on the server and cannot verify browser CORS. After saving, upload a file from the web application and each client you use. Use a file larger than 100 MiB to verify multipart uploads.

## Temporary file cleanup

Selected files are uploaded as temporary objects and become active when business data is saved. Unused files expire after 24 hours; background cleanup deletes them and aborts unfinished multipart uploads. Also configure a bucket lifecycle rule to abort old incomplete multipart uploads left orphaned by network failures.
