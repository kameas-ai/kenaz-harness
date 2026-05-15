// Package gemini is the Google Gemini streaming adapter, supporting both
// Google AI Studio (API-key authentication) and Google Cloud Vertex AI
// (service-account and application-default-credential authentication).
//
// Wire shape
//
// Gemini uses its own REST/SSE protocol (distinct from OpenAI's Chat
// Completions wire shape). This adapter implements the wire translation
// directly rather than composing against openaiwire.Base.
//
// Endpoint kinds
//
//   - AI Studio: https://generativelanguage.googleapis.com/v1beta/models/
//     <model>:streamGenerateContent?alt=sse
//   - Vertex AI: https://<region>-aiplatform.googleapis.com/v1/projects/
//     <project>/locations/<region>/publishers/google/models/<model>:
//     streamGenerateContent
//
// Lossy translation list
//
// The following capabilities are not representable in Gemini's wire
// protocol and are silently dropped or approximated:
//
//   - tool_call.id — Gemini does not assign stable IDs to function calls;
//     the adapter synthesises positional IDs ("call_0", "call_1", …).
//   - CachingSpec.Breakpoints — Gemini Context Caching requires a named
//     cached-content resource created out-of-band; the CachingSpec
//     breakpoint model is not mapped.
//   - ContentBlock type "document" (PDF) — Gemini accepts files via the
//     File API only; inline PDF bytes are rejected with ErrInvalidRequest.
//   - MediaSource.Kind=="uri" — the Gemini File API URI shape is out of
//     scope for this mission; URI sources return ErrInvalidRequest.
//   - image/gif — Gemini does not accept GIF images; returns ErrInvalidRequest.
package gemini
