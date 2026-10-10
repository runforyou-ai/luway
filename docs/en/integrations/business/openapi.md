---
title: HTTP API connection
order: 3
---

Business systems can provide HTTP APIs described by an OpenAPI document. Each operation becomes a tool.

## OpenAPI document

OpenAPI 3.x documents in JSON or YAML are supported; Swagger 2.0 isn't. Enter a document URL or paste the document:

- **Document URL**: The document is read from this URL when you save or update tools, without credentials. If the document requires authentication, paste it instead.
- **Paste document**: The document is stored with the business system. Paste it again after the API changes.

Only internal `#/...` references are resolved. Request bodies must use a JSON media type (`application/json` or a type ending in `+json`) and are sent with the declared type; operations with other request body types don't become tools.

## API base URL

Tools call the API base URL followed by the operation path. When the base URL is empty, the first entry in the document's `servers` is used, with relative URLs resolved against the document URL. If the document has no usable server, enter the base URL.

## Tools and parameters

The tool name is the operation's `operationId`, or the method and path such as `GET /orders/{id}` when none is set or the name is already taken, with a number appended if it is still taken. The tool description comes from the operation's `summary` and `description`.

Tool parameters combine the operation's path, query, and header parameters with the request body. When the body is an object, each writable field becomes a parameter. When the body isn't an object or a field shares a name with another parameter, the whole body becomes a `body` parameter. Parameter bindings can bind path, query, and header parameters or body fields to an identity. See [Business systems](/docs/en/integrations/business/connectors/).

Query parameters are serialized according to the declared `style` and `explode`, and arrays in paths and headers are joined with commas. Body fields set to `null` are sent as is, which clears the field.

## Default properties

`GET` and `HEAD` operations are read-only by default. Operations with other methods are irreversible writes, or L3, by default. Correct them in the tool settings, for example by marking a `POST` that creates a ticket as reversible.

## Call results

Response bodies can't exceed 8 MB. For 2xx responses, the response body is passed to the AI employee as is, or only the status code when the body is empty. Other status codes are treated as failures, and the status code and response body are passed to the AI employee as the reason. Redirects returned by the API aren't followed.
