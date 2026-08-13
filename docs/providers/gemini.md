# Google Gemini Provider

This guide covers configuring the Google Gemini adapter in the kenaz-harness.
The adapter supports two endpoint kinds: **AI Studio** (API key, free tier
available) and **Vertex AI** (Google Cloud project, service-account or ADC auth).

## Overview

The `gemini` adapter speaks the Gemini REST/SSE wire protocol directly —
no GCP SDK is linked into the binary. Key characteristics:

- Streams via `streamGenerateContent?alt=sse` on both AI Studio and Vertex endpoints.
- Role mapping: `user→user`, `assistant→model`, `tool→user` (functionResponse part).
- Tool call IDs are synthesised positionally (`call_0`, `call_1`, …) because Gemini
  does not assign IDs on the wire.
- Reasoning is gated to `gemini-2.5-*` models via `thinkingConfig.thinkingBudget`.
- Vision input is supported for PNG, JPEG, WebP, HEIC, HEIF (not GIF or URI sources).

## Prerequisites

### AI Studio

1. A [Google AI Studio](https://aistudio.google.com/) account.
2. An **API key** generated at `aistudio.google.com/apikey`.

### Vertex AI

1. A Google Cloud project with the **Vertex AI API** enabled.
2. One of:
   - A **service account** JSON key file (roles: `roles/aiplatform.user`), or
   - **Application Default Credentials** (ADC) — e.g. `gcloud auth application-default login`.
3. The project ID and region (e.g. `us-central1`).

## Adding a Provider via the UI

### AI Studio

1. Open the harness **Providers** tab.
2. Select **Google Gemini** from the provider kind dropdown.
3. Choose **AI Studio** as the endpoint kind.
4. Paste your API key.
5. Click **Submit**.

The API key is written to your OS keychain and never stored in `providers.json`.

### Vertex AI (ADC)

1. On your machine, run `gcloud auth application-default login` so the
   metadata-server or `~/.config/gcloud/application_default_credentials.json`
   file is populated.
2. Open the harness **Providers** tab.
3. Select **Google Gemini** and choose **Vertex AI** as the endpoint kind.
4. Choose **Application Default Credentials** as the auth method.
5. Enter your **Project ID** and **Region** (e.g. `us-central1`).
6. Click **Submit** (no API key probe is performed for ADC — credentials are
   validated on first inference).

### Vertex AI (service account — paste JSON)

1. Download the service account JSON key from the Google Cloud Console.
2. Open **Providers**, select **Google Gemini → Vertex AI**.
3. Choose **Service Account (paste JSON)** and paste the full JSON content.
4. Enter your **Project ID** and **Region**.
5. Click **Submit**.

The JSON is written to your OS keychain. The harness mints short-lived
OAuth2 Bearer tokens (1-hour TTL) from the service account key at runtime.

### Vertex AI (service account — file path)

1. Place the service account JSON file in a stable path on disk
   (e.g. `/etc/harness/sa-vertex.json`).
2. Open **Providers**, select **Google Gemini → Vertex AI**.
3. Choose **Service Account (file path)** and enter the absolute path.
4. Enter your **Project ID** and **Region**.
5. Click **Submit**.

## Feature Flag

The Gemini adapter is enabled by default. To disable it:

```bash
export HARNESS_GOOGLE_GEMINI=0
```

When disabled, the adapter is not registered and **Google Gemini** does not
appear in the providers dropdown. The flag is also surfaced in the harness
**Settings → Feature Flags** panel.

## Supported Models

| Model | Reasoning | Vision | Context Window | Max Output |
|---|---|---|---|---|
| `gemini-2.5-pro` | Yes | Yes | 1 048 576 | 65 536 |
| `gemini-2.5-flash` | Yes | Yes | 1 048 576 | 65 536 |
| `gemini-2.0-flash` | No | Yes | 1 048 576 | 8 192 |
| `gemini-2.0-pro` | No | Yes | 1 048 576 | 8 192 |
| `gemini-1.5-pro` | No | Yes | 2 097 152 | 8 192 |
| `gemini-1.5-flash` | No | Yes | 1 048 576 | 8 192 |
| `gemini-1.0-pro` | No | No | 32 768 | 8 192 |

Glob matching is used — any model name prefixed by the above strings inherits
the same capabilities. For models not in the table, the wildcard fallback
applies (streaming, tool calling, vision, JSON mode enabled; reasoning off).

## Reasoning (gemini-2.5 only)

Set `ReasoningSpec.BudgetTokens` in a `GenerationRequest` to enable thinking
tokens. The harness maps the budget to `generationConfig.thinkingConfig.thinkingBudget`
on the wire. Budget 0 disables thinking (default for non-2.5 models).

If a `gemini-2.0-*` or older model is used with a non-zero budget the harness
silently drops the `thinkingConfig` field — the model does not support it.

## Lossy Translation Notes

The following information is dropped or approximated when translating to the
Gemini wire format:

- **Synthesised tool call IDs** — Gemini assigns no ID on the wire; the harness
  synthesises `call_0`, `call_1`, … positionally. Round-tripping tool call IDs
  across turns requires the caller to track these synthetic IDs.
- **Prompt caching** — no harness-level caching capability exists (the inert
  `CachingSpec` type was removed by per-family-message-shaping-01PMDL06 WP03).
- **Document content blocks** — Gemini's document part is not yet wired;
  document blocks are rejected with an error.
- **URI media sources** — inline base64 is the only supported image source;
  URI-sourced images are rejected with an error.
- **GIF images** — not accepted by Gemini's inline-data type; GIF MIME is
  rejected with an error.

## Cost Accounting

Usage is reported under `kind=gemini` in the per-session usage view and audit
trail. Pricing (per million tokens, as of May 2026):

| Model | Input | Output | Reasoning (cached) |
|---|---|---|---|
| `gemini-2.5-pro*` | $1.25 | $10.00 | $3.50 |
| `gemini-2.5-flash*` | $0.15 | $0.60 | — |
| `gemini-2.0-flash*` | $0.10 | $0.40 | — |
| `gemini-2.0-pro*` | $0.15 | $0.60 | — |
| `gemini-1.5-pro*` | $1.25 | $5.00 | — |
| `gemini-1.5-flash*` | $0.075 | $0.30 | — |

Prices are from the starter price table and can be overridden via the cost
reducer configuration.

## Manual Smoke Test Checklist

After configuring a Gemini provider, verify the following:

1. **TestKey passes** — open the provider card; the green tick should appear
   within 5 seconds. A red badge with "Unauthorized" means the API key or
   service account credentials are wrong.
2. **Text generation works** — start a new session with a Gemini model and
   send "Hello". Confirm the response streams token by token.
3. **Vision works** — attach a PNG image and ask "Describe this image."
   Confirm the model identifies the image content.
4. **Tool calling works** — enable a built-in tool and ask a question that
   triggers a tool call. Confirm the tool-call badge appears in the UI.
5. **Reasoning works (2.5 models only)** — select `gemini-2.5-flash`, enable
   the reasoning dial, and send a multi-step math problem. Confirm thinking
   tokens are shown in the usage breakdown.
