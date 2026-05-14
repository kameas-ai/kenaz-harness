# Multimodal Output — Operator & User Guide

**Mission**: `multimodal-io-extended-01KQ8TD2`
**Shipped in**: v0.13.0

This document covers the model-generated image output pipeline: what it does, how to configure it, and how to disable it.

---

## Overview

When a model produces an image (e.g. via DALL-E 3, gpt-image-1, or Amazon Titan Image Generator), the harness automatically:

1. Fetches the image (if delivered as a URL) within a 30-second / byte-cap bounded window.
2. Captures it as a binary artifact in the artifact CAS (`source = "model_output"`).
3. Appends a `generated_image` content block to the assistant turn.
4. Renders an inline thumbnail in the chat bubble (click to full-screen lightbox).
5. Makes the artifact available in the Artifacts tab for promotion, export, or deletion.

---

## Capability matrix

| Provider | Model family | Image output | Notes |
|---|---|---|---|
| OpenAI | `dall-e-3`, `gpt-image-1` | Yes | URL + revised-prompt fields populated |
| AWS Bedrock | Titan Image G1/G2 | Yes | S3 pre-signed URL fetched by harness |
| Anthropic | Claude 3.x+ | No | No native image generation |
| Others | Any | No | Requires `CapImageOutput` in capability YAML |

---

## Settings

### Settings UI (Settings → Generated Image Capture)

The section is visible only when `HARNESS_MULTIMODAL_OUT` is `on` (the default).

| Setting | Default | Description |
|---|---|---|
| Auto-capture model-generated images | ON | When on, generated images are saved to the artifact store. When off, they appear inline in the bubble but are not persisted. |
| Per-image byte cap (MiB) | 20 MiB | Images larger than this cap are dropped with a placeholder block. Range: 1–100 MiB. |

### Environment variables

| Variable | Values | Default | Effect |
|---|---|---|---|
| `HARNESS_MULTIMODAL_OUT` | `on` / `off` | `on` | System-level kill switch. When `off`, generated image events are dropped with a warn log and the Settings UI section is hidden. Takes effect at process startup — restart the harness after changing. |

---

## Kill switch

To disable the entire multimodal output pipeline:

```bash
HARNESS_MULTIMODAL_OUT=off wails dev
# or
HARNESS_MULTIMODAL_OUT=off ./kenaz-harness
```

When `off`:

- Adapters do not emit `StreamGeneratedImage` events — image bytes are never fetched.
- The "Generated Image Capture" section in Settings is hidden.
- Inline `GeneratedImageBlock` thumbnails are hidden from existing messages.
- The `multimodal-out` entry in Settings → Feature Flags shows as disabled.

---

## Manual acceptance smoke

Preconditions: OpenAI API key configured, provider profile set to `gpt-image-1` or `dall-e-3`.

1. **Basic capture**
   - Send: `Generate a 512x512 image of a red cube on a white background.`
   - Expected: thumbnail appears inline in the assistant bubble within ~10 s; artifact appears in the Artifacts tab with `source = model_output` and a title like `2026-MM-DD-openai-dall-e-3-0.png`.

2. **Revised prompt**
   - Send a prompt that triggers DALL-E 3's safety rewrite (e.g. include a famous person's name).
   - Expected: hover over the thumbnail — tooltip shows the revised prompt text.

3. **Auto-capture off**
   - In Settings → Generated Image Capture, uncheck "Auto-capture model-generated images".
   - Send another image generation request.
   - Expected: thumbnail still appears inline but no new artifact row is created in the Artifacts tab.

4. **Byte cap**
   - Set the per-image byte cap to 1 MiB.
   - Request a high-resolution image that will exceed 1 MiB.
   - Expected: placeholder block appears in the bubble with text `[generated image dropped — image exceeds byte cap]`; no artifact row created.

5. **Kill switch**
   - Restart with `HARNESS_MULTIMODAL_OUT=off`.
   - Verify: Settings → Generated Image Capture section is absent; Settings → Feature Flags shows `multimodal-out` as disabled.
   - Send an image generation request.
   - Expected: assistant returns text (or tool result), no image thumbnail rendered.

6. **Lightbox**
   - Click any generated image thumbnail.
   - Expected: full-screen lightbox opens with the image; Escape or click-outside closes it.

---

## Artifact provenance fields

Artifacts captured from model output carry extended source metadata in `ArtifactSourceRef`:

| Field | Description |
|---|---|
| `imageIndex` | Index of this image within the generation request (0-based, for multi-image requests). |
| `revisedPrompt` | The rewritten prompt used by the provider, if any (DALL-E 3). |

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| No thumbnail in bubble | `HARNESS_MULTIMODAL_OUT=off` | Restart with `HARNESS_MULTIMODAL_OUT=on` (or unset). |
| Thumbnail appears but no artifact in tab | Auto-capture is off | Enable in Settings → Generated Image Capture. |
| Placeholder "[generated image dropped]" | Image exceeded byte cap | Raise the byte cap in Settings (up to 100 MiB). |
| Artifact fetch fails (404) | DALL-E temporary URL expired | URL validity is ~60 s; retry the generation. |
