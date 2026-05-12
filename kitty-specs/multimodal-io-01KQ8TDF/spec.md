# Spec: Multimodal Input — capability gate + adapter audit (v0.8.5)

**Mission ID**: `multimodal-io-01KQ8TDF`
**Status**: active
**Owner**: alecfeeman
**Release slot**: v0.8.5 (UX maturity — "paste an image into chat" goal)
**Predecessor**: `_archive/multimodal-io-01KQ5GW4` — shipped the foundational
`ContentBlock` / `MediaSource` shape, CAS-backed `MediaStore`, and basic
frontend tray. This mission enriches that foundation with per-provider
capability gates, richer metadata, and cross-provider coverage.

---

## 1. Why

The predecessor mission (`multimodal-io-01KQ5GW4`) wired the core plumbing:
`Message.Content []ContentBlock`, inline base64 image/document blocks, and the
CAS-backed `MediaStore`. Four things remain before the feature is genuinely
ship-ready:

1. **No byte-level capability gate.** Every adapter trusts the caller to send
   only what the provider accepts. A mismatch surfaces as a cryptic wire error
   instead of a clear pre-flight rejection.
2. **No per-provider MIME / size / page-count caps.** The frontend uses
   hard-coded 20 MiB / 30 MiB caps regardless of the active provider.
3. **OpenRouter has no image serialization at all.** OpenAI silently drops
   PDFs with a warn log rather than failing pre-flight.
4. **`MediaSource` carries no dimensions or byte size.** The gate cannot
   enforce pixel caps or byte caps without re-decoding the base64 in every
   adapter.

This mission closes those gaps with a descriptor-driven pre-flight gate,
per-adapter audits, image-dimension + PDF-page-count extraction, and a feature
flag that lets operators or users disable multimodal input completely.

---

## 2. Goals

- **G1** Any image or document content block is pre-flight validated against
  the active (provider, model) descriptor before any network call. Violations
  surface as typed errors with enough context to render a user-friendly
  message.
- **G2** `MediaSource` carries `SizeBytes` and optional `ImageDimensions` so
  the gate can enforce per-provider caps without per-adapter re-decode.
- **G3** All four adapters (Anthropic, OpenAI, OpenRouter, Bedrock) consistently
  handle image blocks; PDFs are rejected pre-flight on providers that do not
  support them.
- **G4** The frontend derives attachment caps from the active model's capability
  descriptor rather than hard-coded constants.
- **G5** A `HARNESS_MULTIMODAL_IN` env flag (default `on`) and a
  `Settings.MultimodalInputEnabled` dial give operators and users a single
  off-switch.
- **G6** PDF page count and image pixel dimensions are extracted on ingest and
  stored on the `MediaArtifact` / `ContentBlock` so downstream code and the
  tray can surface them without re-parsing.

---

## 3. Functional requirements

### 3.1 Extended MediaSource + typed errors

| ID | Requirement |
|---|---|
| FR-001 | `llm.MediaSource` gains `SizeBytes int64` and `ImageDimensions *ImageDimensions` (struct `{Width, Height int}`). Additive — no existing field renamed or removed. |
| FR-002 | `llm` package gains six typed attachment errors: `ErrAttachmentTooLarge`, `ErrAttachmentMimeUnsupported`, `ErrAttachmentCountExceeded`, `ErrAttachmentDimensionExceeded`, `ErrAttachmentEncrypted`, `ErrAttachmentAudioUnsupported`. Each carries enough context (provider, MIME, given vs. cap) for a localised friendly message. |
| FR-003 | `frontend/src/lib/types.ts` mirrors the new `SizeBytes` and `ImageDimensions` fields on the TS `MediaSource` shape. |
| FR-004 | `GenerationRequest.Attachments` is marked `// Deprecated: use Message.Content blocks` in godoc. No removal — existing callers remain valid. |

### 3.2 Capability YAML extension

| ID | Requirement |
|---|---|
| FR-005 | `capabilities.Descriptor` (loader + YAML schema) gains: `ImageInput bool`, `DocumentInput bool`, `MaxImageBytes int64`, `MaxDocumentBytes int64`, `MaxImageCountPerMessage int`, `MaxImagePixels int64`, `MaxDocumentPages int`, `ImageInputMimeTypes []string`, `DocumentInputMimeTypes []string`. |
| FR-006 | `capabilities/data/{anthropic,openai,openrouter,bedrock,ollama}.yaml` populated per §7 of plan.md. Missing fields default to `false` / `0` (safe, restrictive). |
| FR-007 | New `capabilities.Catalog` accessor methods expose the new fields: `AttachmentLimits(provider, model) AttachmentDescriptor`. |

### 3.3 Pre-flight capability gate

| ID | Requirement |
|---|---|
| FR-008 | `gate.CheckAttachments(req llm.GenerationRequest, prof llm.ProviderProfile) error` checks every image/document `ContentBlock` in `req.Messages` against the resolved descriptor. Returns the **first** violation as a typed error. Called before the adapter wire call. |
| FR-009 | Audio MIME types (`audio/*`) are unconditionally rejected with `ErrAttachmentAudioUnsupported` regardless of provider. |
| FR-010 | Each adapter (`anthropic.go`, `openai.go`, `openrouter.go`, `bedrock.go`) calls `gate.CheckAttachments` in `Stream` before any serialization. |

### 3.4 Per-adapter audit

| ID | Requirement |
|---|---|
| FR-011 | **Anthropic**: existing image+document serialization confirmed. `CheckAttachments` hooked in. |
| FR-012 | **OpenAI**: `document_input: false` in YAML; the silent warn-log drop of document blocks is removed — the gate rejects pre-flight with `ErrAttachmentMimeUnsupported{Provider:"openai", Mime:"application/pdf"}`. |
| FR-013 | **OpenRouter**: image serialization added (mirror OpenAI `image_url` base64 shape); `document_input: false` default. `CheckAttachments` hooked in. |
| FR-014 | **Bedrock**: existing image+document serialization confirmed. Claude-on-Bedrock YAML rows set `document_input: true`. `CheckAttachments` hooked in. |

### 3.5 Metadata extraction

| ID | Requirement |
|---|---|
| FR-015 | `core/attachments/media.go` `Put` extracts image dimensions via `image.DecodeConfig` (JPEG, PNG, GIF) and `golang.org/x/image/webp.DecodeConfig` and stamps `ImageDimensions` on the returned `MediaArtifact`. |
| FR-016 | `core/attachments/media.go` `Put` extracts PDF page count via `github.com/ledongthuc/pdf`. Input read capped at 50 MiB. Encrypted PDF → `ErrAttachmentEncrypted`. |
| FR-017 | `MediaArtifact` gains `ImageWidth int`, `ImageHeight int`, `PageCount int` fields. SQL schema extended (migration). |

### 3.6 Frontend enrichment

| ID | Requirement |
|---|---|
| FR-018 | `ChatInput.vue` fetches `client.llm.attachmentLimits(profileId, modelId)` on mount and replaces the hard-coded 20 MiB / 30 MiB constants with the descriptor-driven values. |
| FR-019 | `DocumentChip.vue` (wherever inline chip renders) shows page count when available. |
| FR-020 | `ImageBlock.vue` shows `WxH px` tooltip on hover when dimensions are available. |
| FR-021 | `frontend/src/lib/errors.ts` adds `friendly()` mappings for all six new typed errors (FR-002). |

### 3.7 Feature flag

| ID | Requirement |
|---|---|
| FR-022 | `HARNESS_MULTIMODAL_IN` env var (default `on`). When `off`, the capability loader forces `ImageInput=false`, `DocumentInput=false` on every descriptor. |
| FR-023 | `Settings.MultimodalInputEnabled bool` (default `true`, JSON `multimodalInputEnabled`). Settings UI exposes a toggle under Feature Toggles. |
| FR-024 | When the combined flag resolves to `off`, `ChatInput.vue` hides the paperclip button, the drop overlay, and the paste handler is a no-op for image/PDF clipboard items. |

---

## 4. Non-functional requirements

| ID | Requirement |
|---|---|
| NFR-001 | `go test -race -count=1 -short ./core/llm/... ./core/attachments/... ./core/rpc/...` clean. |
| NFR-002 | Frontend vitest clean; `vue-tsc` clean on new/modified files. |
| NFR-003 | Schema additions to YAML are purely additive; missing fields parse as zero/false → gate defaults to restrictive (safe). |
| NFR-004 | PDF parse is bounded: `Put` reads at most 50 MiB before returning `ErrOversize`. |
| NFR-005 | `go.mod` additions: `github.com/ledongthuc/pdf` (PDF page count), `golang.org/x/image` (WebP decode). Both permissively licensed. |

---

## 5. Out of scope

- Audio or video input (locked: `ErrAttachmentAudioUnsupported` is the hard no).
- HEIC → JPEG client-side re-encoding (deferred; v1 rejects HEIC pre-flight).
- OpenAI Responses API PDF support (Chat Completions only for now; tracked as follow-up).
- AWS Bedrock S3-URI fast path (force base64 inline; tracked as follow-up).
- Artifact-pipeline auto-capture of user-attached media (no; `MediaStore` gives
  dedup + persistence; artifact panel is for model-emitted items).
- Switching session-message storage from inline base64 to `media://` URIs
  (follow-up; tracked in `docs/multimodal-input.md`).

---

## 6. Success criteria

- **A1** Drag a 1 MB PNG into the composer against an Anthropic Sonnet profile →
  renders in tray → send → user bubble shows thumbnail → model echoes a
  description of the image.
- **A2** Drag a 4-page PDF against the same profile → tray chip shows "4 pages"
  → send → user bubble shows DocumentChip → model cites the PDF contents.
- **A3** Drag the same PDF against an OpenAI profile → pre-flight error banner:
  "OpenAI Chat Completions does not accept PDFs. Switch to Anthropic, Gemini, or
  Bedrock-Claude, or convert pages to images."
- **A4** Paste a screenshot from clipboard against Bedrock-Claude → tray entry →
  send → round-trip works.
- **A5** Toggle Settings → `MultimodalInputEnabled=false` → reload composer →
  paperclip gone, drop overlay no-ops, pasting an image inserts nothing.
- **A6** `HARNESS_MULTIMODAL_IN=off go run .` boots; descriptor dump shows
  every provider's `ImageInput=false`.
- **A7** Image > `MaxImageBytes` for the active profile → inline tray error with
  the byte cap from the descriptor (not the hard-coded constant).
- **A8** Encrypted PDF → `ErrAttachmentEncrypted` → tray shows "PDF is
  password-protected; remove the password and re-attach."
- **A9** `go test -race -count=1 -short ./core/llm/... ./core/attachments/... ./core/rpc/...` green.
- **A10** `pnpm typecheck` + `vitest run` green.

---

## 7. Open questions

| # | Question | Decision |
|---|---|---|
| Q1 | Should `ImageDimensions` be stamped on the `ContentBlock.Source` at send time, or only on the `MediaArtifact` row? | Both: the `MediaArtifact` carries it authoritatively; the `ContentBlock.Source` carries it for in-flight gate checks so the gate can validate without a DB lookup. |
| Q2 | Does OpenRouter support per-model YAML overrides for vision? | Yes — add well-known Claude-via-OR and GPT-4o-via-OR rows with `image_input: true` and respective limits. |
| Q3 | What happens when the YAML lists a `max_image_pixels` cap but the `ImageDimensions` field is absent from the `ContentBlock`? | The gate skips the pixel check (cannot validate without data). A future hardening pass can require dimensions for providers where they matter. |
| Q4 | Should `MaxDocumentPages` gate be a hard block or a soft warning? | Hard block for Anthropic (max 100 pages documented). Soft warning (log, no reject) for others where the cap is less documented. |
