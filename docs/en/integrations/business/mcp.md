---
title: MCP connection
order: 2
---

Business systems can provide tools through MCP.

## Connection types

Streamable HTTP and SSE are supported. The service URL must be reachable from the server. See [Business systems](/docs/en/integrations/business/connectors/) for the headers each authentication method sends. The MCP server is connected when the AI employee first calls one of its tools, and the connection is reused for the rest of the run.

## Tool list and properties

The MCP server's tool list is loaded when you save the business system. Declared `readOnlyHint` and `destructiveHint` values become the default properties: tools not declared read-only are treated as writes, and writes without `destructiveHint` set to `false` are treated as irreversible. See [Business systems](/docs/en/integrations/business/connectors/) for properties and levels.

## Identity headers

Identity headers stay the same for a whole run. Connection tests and tool updates send only the authentication header, not identity headers, and the MCP server must still return its tool list. The business system must return data for the identity in the headers and verify that orders and other resources belong to it.

## Test and update tools

**Test connection** on the list page checks a saved connection, and **Update tools** reloads the tool lists of all business systems. Tools that remain keep their settings; changing the service URL clears all tool settings.
