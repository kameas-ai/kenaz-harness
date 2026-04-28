---
work_package_id: "WP02"
title: "Vendor Kenaz tokens.css verbatim and map into tailwind.config.ts"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 2 - Tokens"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Vendor Kenaz tokens.css verbatim and map into Tailwind

## Goal

Establish the harness's single source of visual truth: vendor `/Users/alecfeeman/PycharmProjects/kenaz/frontend/src/styles/tokens.css` byte-identically into `frontend/src/styles/tokens.css` and project every token group (surfaces, ink, accent/brass, signal, modal, radii, type, motion) into `tailwind.config.ts` via `theme.extend.colors / spacing / borderRadius / fontFamily` so shadcn-vue primitives become token-driven by construction. No raw hex/rgba/oklch outside tokens.css.

## Spec references

- FR-001a (Kenaz token import)
- FR-005 (centralized theme tokens)
- C-008 (Kenaz tokens are the source of truth — MUST NOT redefine locally)
- C-009 (VM-host visual coherence)
- C-010 (enterprise-first defaults — dark theme default)
- SC-001 (≥ 4/5 "feels like a frontier AI tool")
- NFR-008 (theme contrast WCAG 2.2 AA)

## Plan references

- §1 ("Kenaz tokens vendored verbatim … No raw hex, OKLCH, or `rgba()` literal appears outside `tokens.css`")
- §4.1 ("`tokens.css` is the source of truth … vendored verbatim … Do not redefine. Do not re-derive.")
- §5.1 (token vocabulary table — surfaces 0–4, borders, ink default/muted/subtle/dim/trace, accent + variants, signal palette, modal, radii sm/md/lg, type ui/mono, breakpoint two-col, motion fast/base/slow)
- §7 v1.0 item 2 (vendored tokens.css; Tailwind theme config consumes it)
- §8 R-1 (Kenaz token drift — refresh-cadence procedure + CI hash check)

## Subtasks

- T001 — Copy `/Users/alecfeeman/PycharmProjects/kenaz/frontend/src/styles/tokens.css` byte-for-byte to `frontend/src/styles/tokens.css`. Add a top-of-file comment recording source path, source git sha, and refresh procedure. Add the SHA to a `frontend/src/styles/tokens.source.json` or similar manifest.
- T002 — Extend `tailwind.config.ts` `theme.extend` to map every token group into utilities: `colors.surface[0..4]`, `colors.ink[default|muted|subtle|dim|trace]`, `colors.accent[default|muted|dim|glow|hairline]`, `colors.signal[ok|warn|danger|info|violet|neutral|git]`, `colors.border[default|muted|strong]`, `colors.modal[overlay|shadow]`, `borderRadius[sm|md|lg]`, `fontFamily[ui|mono]`. Each value references a CSS variable (`var(--surface-2)` etc.).
- T003 — Add harness-local motion tokens (`--motion-fast` 120 ms, `--motion-base` 200 ms, `--motion-slow` 320 ms with `cubic-bezier(0.2,0,0,1)`) into `tokens.css` at a clearly fenced "harness-local until Kenaz publishes" block (per §5.1 footnote). Mirror them as Tailwind `transitionDuration` / `transitionTimingFunction`.
- T004 — Inline a 200-byte critical token block (surfaces + ink + accent only) into `index.html` `<style>` to prevent first-paint flash of unthemed content (R-9 mitigation).

## Acceptance criteria

- `frontend/src/styles/tokens.css` is byte-identical to the Kenaz source for the upstream-defined block; the source-sha manifest records the import sha.
- `tailwind.config.ts` exposes every token from §5.1 as a Tailwind utility; `tw-class-of('text-ink-muted')` resolves to `var(--ink-muted)`.
- A unit test verifies that every token CSS variable referenced in the Tailwind config exists in `tokens.css`.
- `index.html` contains the inline critical-token block; no FOUC on cold start.
- WP03's font @font-face declarations can reference `var(--font-ui)` and `var(--font-mono)`.

## Files to create/modify

- Create: `frontend/src/styles/tokens.css`, `frontend/src/styles/tokens.source.json`.
- Modify: `frontend/tailwind.config.ts`, `frontend/index.html`, `frontend/src/styles/global.css` (import `tokens.css`).
- Create: `docs/adr/0001-vendor-kenaz-tokens.md` recording vendor-vs-package decision (DIRECTIVE_003).
- Create: `frontend/src/styles/REFRESH.md` documenting the refresh procedure and CI alert path (R-1 mitigation).

## Definition of done

- All acceptance criteria pass.
- No file under `frontend/src/` outside `tokens.css` introduces a raw color literal (WP04 enforces this with CI; this WP must not violate the rule).
- ADR 0001 merged.
- Token-discipline test (placeholder) passes; WP04 will tighten it into the build-failing CI invariant.
