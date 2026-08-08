# ADR 0005 — bluemonday HTML sanitizer carve-out from third-party-SDK posture

**Status**: Accepted
**Date**: 2026-08-08
**Spec**: `specs/092-kenaz-documents` (Task 1.H-SAN, FR-005, SC-002); ruling R-1
**Companion**: workspace `.specify/decisions/ADR-document-substrate.md` records
the same decision at the programme level with `security-privacy` co-signature.

## Context

Spec 092 makes HTML the document type. FR-005 requires that every byte of
HTML entering the store pass a **single, shared, idempotent** sanitization
pipeline implemented **in Go**, so that one rule set covers the desktop
entry point, the served entry point, and the host app that imports
`core/docs` as a library. SC-002 turns that into a merge gate: 100% of
stored bodies must satisfy `sanitize ∘ sanitize = sanitize` with no active
content surviving.

The 092 preflight (2026-08-08) found **no Go HTML sanitizer anywhere in this
repository**. `go.mod` has no bluemonday or equivalent; every sanitization
path shipping today is `dompurify` in the Vue frontend — client-side,
render-time, and therefore incapable of being a store invariant.

Two options:

- **(a)** `github.com/microcosm-cc/bluemonday` — a new direct dependency
  under `core/`.
- **(b)** Hand-roll an allowlist policy over `golang.org/x/net/html`, which
  is already a direct dependency.

Charter **C-001** ("no third-party SDKs in `core/`; stdlib +
already-vendored deps only") admits a strict reading that blocks (a), the
same reading ADR 0004 confronted for `cedar-go`. Constitution **§VIII**
(unchanged in shape at constitution 3.0.0 — still one sentence: *"New
dependencies in any component require explicit written justification"*)
requires justification, not abstention.

## Decision

Adopt **bluemonday** under `core/docs/`, behind this package's own API,
as exempt from a strict third-party-SDK prohibition.

### Why not hand-roll (the §VIII justification)

Writing an HTML sanitizer is the canonical security own-goal, and the
reasons are specific rather than general:

1. **The bug class is silent.** A sanitizer that is 99% correct looks
   identical in production to one that is 100% correct, right up until it
   doesn't. There is no failing test, no error log, and no user report —
   just a working XSS. Every other dependency decision in this repo trades
   convenience against surface area; this one trades surface area against a
   class of vulnerability we cannot detect ourselves.

2. **The hard part is not the allowlist, it is the disagreements.** A
   hand-rolled policy over `x/net/html` gets the element and attribute
   filtering right in an afternoon. What takes years is mutation XSS: the
   cases where the sanitizer's parse and the browser's parse of the same
   bytes differ. `<noscript>` is markup with scripting disabled and raw text
   with it enabled. `<svg>` and `<math>` switch the tokenizer into foreign
   content, changing how the following bytes are read. `<template>` children
   land in an inert fragment. `<xmp>` and `<plaintext>` are raw-text
   containers a naive filter re-emits live. `<!-->` closes a comment in some
   parsers and not others. bluemonday has absorbed a decade of these,
   including a documented security policy and CVE history; we would be
   rediscovering them one incident at a time.

3. **This code is a two-repo merge gate.** SC-002 makes the sanitizer an
   invariant that `kenaz` also depends on (R-3: `kenaz` imports `core/docs`
   as a library, precedent `core/mcp/oauth`). A hand-rolled sanitizer would
   get one review at authoring time and then rot in a package almost nobody
   opens — the worst possible maintenance profile for security-critical
   parsing code.

4. **The dependency budget is better spent here than on the editor.** 092
   proposes exactly two new dependencies (R-1 sanitizer, R-2 TipTap). If
   only one is affordable, it should be the one where a mistake is invisible
   and remotely exploitable, not the one where a mistake is a worse editing
   experience.

### Why bluemonday specifically

1. **It is a text transformer, not a service client** — the ADR-0004
   distinction, and the reason C-001's *intent* is not violated. bluemonday
   opens no sockets, reads no environment, holds no credentials, emits no
   telemetry, and has no cloud surface. Its entire API is `[]byte` in,
   `[]byte` out, CPU-bound.

2. **Local-first by construction.** Nothing in `Sanitize` can network. The
   §I posture is preserved without needing a fence around it.

3. **Pure Go, no CGo, small graph.** Three modules enter (`bluemonday`,
   plus `aymerick/douceur` and `gorilla/css` for CSS-declaration parsing —
   both already listed in `NOTICES`). `golang.org/x/net` was already a
   direct dependency. No web framework, no ORM, no reflection-heavy
   machinery. Licences: BSD-3-Clause (bluemonday, gorilla/css) and MIT
   (douceur).

4. **The seam is replaceable.** No bluemonday type appears in any exported
   signature of `core/docs` — callers see `docs.Sanitize(string) (string,
   error)`. If a future audit rejects the dependency, the policy can be
   reimplemented behind the same function without touching a single caller,
   and the property tests in `sanitize_property_test.go` transfer unchanged.

5. **Precedent.** `modernc.org/sqlite` interprets our own database files;
   `cedar-go` interprets our own policy files (ADR 0004); bluemonday
   interprets our own document bodies. Same shape of carve-out, same
   reasoning.

### Where we are stricter than the library

The carve-out is not blind adoption. `core/docs` overrides bluemonday's
defaults in four places, each with a test pinning it:

- **`AllowDataURIImages()` is not used.** Its accepted-mime pattern in
  v1.0.27 includes `image/svg+xml` even though its own doc comment says it
  does not — and SVG is a scripting host. `dataImageURL` in `sanitize.go`
  is a narrower replacement admitting only `gif`, `jpeg`, `png`, `webp`.
  If the library ever widens further, our test fails rather than our
  posture.
- **`AllowStandardURLs()` is not used.** It permits `http` and relative
  URLs. We allow `https` and `mailto` only, with `AllowRelativeURLs(false)`,
  and require https URLs to be hierarchical with a real host and no
  embedded credentials.
- **`RewriteSrc` enforces "https links only, never resources."**
  bluemonday's URL policies are global — they see a URL, not the element it
  came from — so an `https` `<img src>` would otherwise become a tracking
  beacon. The rewriter is the element-aware hook that closes it.
- **`AllowElementsContent("frame")` repairs an upstream content-loss bug.**
  bluemonday ships void `<frame>` on its skip-content list, and that flag
  clears only on an end tag that a void element never has — so one `<frame>`
  anywhere silently truncates the rest of the document. Fail-closed, but a
  fidelity bug we can simply not have.

## Consequences

- **Pros**: FR-005 is single-sourced in Go and enforced at the store
  boundary; the mutation-XSS class is handled by code with a decade of
  incident history behind it; the seam stays replaceable; upstream security
  fixes arrive as version bumps.
- **Cons**: three new modules in the graph, one of them under `core/`. Also
  an ongoing obligation — bluemonday releases must be tracked, and the four
  divergences above must be re-verified on each bump. The tests do the
  re-verification; the tracking is a human duty.
- **Bounded**: bluemonday is imported only from `core/docs/`. Nothing else
  in the tree may reference it, for the same leaf-package reason ADR 0004
  gave for cedar-go.
- **Not a licence to skip the other defenses.** Sanitization is one of
  three independent layers. Rendering surfaces still frame document HTML in
  a sandboxed iframe with neither `allow-same-origin` nor `allow-scripts`
  (FR-006, corrected by task 1.H-FR6), and exporters still strip scripts
  unconditionally. dompurify stays in the Vue app as render-time defense in
  depth. If this ADR is ever read as "the sanitizer handles it", it has been
  misread.

## References

- `core/docs/sanitize.go` — the policy, and the package doc stating which
  layer is authoritative for what.
- `core/docs/sanitize_property_test.go` — the SC-002 idempotence property
  over a generated corpus, plus closure / inertness / no-external-reference
  properties and a fuzz target.
- `core/docs/sanitize_test.go` — the adversarial payload table; every row
  names the class it guards.
- `docs/adr/0004-cedar-policy-engine-carveout.md` — the carve-out this one
  is modelled on.
- `specs/092-kenaz-documents/plan.md` §"R-1 — Go HTML sanitizer".
- Constitution §VIII (3.0.0), §XI Security by Default, §XIII Explicit
  Publication.
