---
title: Platform model service
order: 5
---

Platform admins can set up models for the whole platform. Every workspace can select them directly without connecting its own model providers. Find these pages under **Settings → Platform**: **Platform models**, **Platform providers**, and **Model calls**.

## Providers

Platform providers are the model providers that actually serve platform models, such as OpenRouter, DeepSeek, or Alibaba Cloud Model Studio.

1. Open **Platform providers**, click the add button in the top-right corner, and choose a brand.
2. Enter a name, API key, and API URL. You can click **Test connection** first to confirm the configuration works.
3. After saving, the provider can be used as a source of platform models.

Only platform admins can see a platform provider's key and URL. Workspace members can't. A provider that is still a source of a platform model can't be deleted. Remove that source from the platform model first.

## Model catalog

A platform model is the model workspaces see, such as "DeepSeek V4.1 Flash". A platform model can have several sources. Workspaces only see the model name, not which providers serve it.

1. Open **Platform models** and click the add button in the top-right corner.
2. Add a source: select a platform provider and enter that provider's model identifier, or click the list button next to the source to choose from the models the provider offers. If the model name is empty, the selected model's type, input types, and token limits are filled in too.
3. Adjust the model name, type, input types, context window, and maximum output tokens as needed.

Calls start with the first enabled source and try the next one when a source fails. Use the up and down arrows to change the order, and clear **Enabled** to pause a source. When every source is paused, workspaces can't use the model for the time being.

> [!IMPORTANT]
> All sources of a platform model must serve the same model. This matters most for embedding models: knowledge base indexes aren't rebuilt when sources change, so switching to a different model breaks retrieval.

A saved platform model is available to every workspace right away. A platform model that is used by AI employees, knowledge bases, or customer service settings in any workspace can't be deleted, and its type or input types can't be changed to ones those uses don't support.

## How workspaces use them

Wherever workspaces choose a model (AI employees, knowledge bases, conversation summaries, translation, and so on), they see a **Platform models** group above the models they configured themselves. For workspace model providers, see [Model services](/docs/en/guide/workspace/models/).

## Call records and errors

**Model calls** lists platform model calls from all workspaces. You can filter by model, status, and workspace. Open a record to see:

- the workspace, usage, and who started the call;
- duration and token usage;
- the provider, model identifier, status, and failure reason of each upstream attempt.

When a call tried several sources, the earlier sources failed and a later one completed the call or it failed in the end. Failures that keep happening on the same source usually mean you should check that provider's key, balance, or model identifier.

Calls to models a workspace configured itself belong to that workspace and don't appear here.
