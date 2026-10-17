---
title: Workspace settings
order: 1
---

Configure **Customer service** and **Employee service** separately in workspace settings. Customer service settings apply to external customer channels. Employee service settings apply to direct chats with service AI employees and employee channels such as WeCom bots.

Each audience has its own business hours, timeouts, categories, and summary models. Members with **Customer service management** or **Employee service management** permission manage the corresponding settings. Administrators manage both.

## Business hours

Set the time zone, weekly hours, and date overrides for each audience. Human first-response time uses the corresponding business hours, as do channel messages about availability after a handoff.

## Assignment and reminders

Configure unanswered-request reminders, reassignment timeouts, queue reminders, AI follow-up, and automatic session closure separately. Both audiences support automatic follow-up. Employee service timeouts also control follow-up and closure in employee direct chats with AI.

Timeout tasks read the current configuration when they execute. A human handler's capacity covers all active service requests.

## Service categories

Maintain categories and handoff teams for each audience. AI category selection, summaries, and manual summary edits use the categories for that service audience. Both audiences can have categories with the same name.

## Customer profiles and identity verification

Under **Customer service → Customer profiles**, use **Profile fields** and **Customer tags** to manage customer information. Customer identity verification keys are also managed in customer service settings.

## Summaries and translation

Each audience selects its decision model, summary model, and summary language. Handoff summaries, closing summaries, reviews, and knowledge-gap drafts use the corresponding service settings.

Translation is configured in Customer service settings for customer channel messages and replies to customers.

## AI evaluation

Select an evaluation decision model in **AI evaluation** settings. This workspace configuration supports offline AI employee evaluations and requires **AI employee management** permission.

## Media understanding

Under **Media understanding**, choose chat models for OCR and speech transcription. When the primary model cannot accept images or audio natively, the runtime uses these models to turn media into text for the primary model. The OCR model must accept image input; the transcription model must accept audio. Without a model configured, that fallback is unavailable. Members with **workspace management** permission can change these settings.
