---
title: Business systems
order: 1
---

A business system is an external system connected to your workspace, such as an order, ticketing, or membership system. AI employees use its tools as authorized to look up or change business data.

## Add a business system

Add business systems under **AI employees → Business systems** with a name, transport, connection details, and authentication. Saving connects to the business system and loads its tools; nothing is saved if the connection fails. The transport can't be changed after creation:

- **MCP server**: The business system provides an MCP server. See [MCP connection](/docs/en/integrations/business/mcp/).
- **HTTP API (OpenAPI)**: Operations are called as described by the business system's OpenAPI document. See [HTTP API connection](/docs/en/integrations/business/openapi/).

Authentication applies to both transports, and the credentials are shared by the workspace:

| Authentication | Header sent |
| --- | --- |
| None | None |
| Bearer token | `Authorization: Bearer <token>` |
| Custom header | The header name and key you enter, such as `X-Api-Key` |
| Username and password | `Authorization: Basic …` |

AI employees use the most recently loaded and saved tool list. After the business system adds or changes tools, select **Update tools** on the list page for the changes to take effect.

## Tools and operation levels

Each tool has three properties: whether it is read-only, whether its written results can be undone, and whether it sends messages to third parties. The operation level is derived from these properties and identity bindings:

| Level | Condition | Examples |
| --- | --- | --- |
| L0 Public information | Read-only, no identity bound | Product lookup, public policies |
| L1 Own data | Read-only, bound to a customer or member identity | My orders, shipment tracking |
| L2 Reversible write | Written results can be undone | Profile updates, ticket creation |
| L3 Irreversible action | Written results can't be undone, or involve payments | Refunds, balance adjustments |

Default properties come from what the business system declares: MCP tools use their annotations and HTTP operations use their request method. Undeclared tools are treated as irreversible writes. Open a tool on the **Tools** tab of a business system to correct its properties or disable it.

## Identity bindings

Identity bindings let the business system return data for the real identity in the conversation. AI employees can't see or change these values:

- **Identity headers**: Added in the connection settings and sent as request headers. They can't reuse the authentication header name.
- **Parameter bindings**: Bind a tool parameter to an identity in the tool settings. The AI employee no longer fills in that parameter.

You can bind the verified customer's ID and email, the served member's ID and email, and the conversation ID. Customer identity is provided only after the customer is verified. Member identity is provided only when the AI employee serves a single member, not in group chats. Tools whose bound identity is missing aren't offered to the AI employee, and the AI employee asks unverified customers to sign in.

## Authorize AI employees

Select business systems in an AI employee's run configuration and set the highest level for each one. The AI employee can use only tools at or below that level. L2 operations can require the initiator's confirmation first, and L3 operations and operations that message third parties run after the AI employee's responsible person approves them. See [Operation levels and approvals](/docs/en/guide/ai-employees/operation-levels/).

## Call records

Every call records the business system, tool, operation level, arguments, and result. The side panel of a customer conversation shows the business lookups in the current service cycle.
