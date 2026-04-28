---
work_package_id: "WP09"
title: "Privacy-guarantees panel + Model·Tier·Build status footer + live-rate inline indicator"
dependencies:
  - "WP06"
  - "WP02"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 9 - Status surfaces"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – Privacy-guarantees panel + status footer + live-rate inline indicator

## Goal

Land three Kenaz status primitives:

1. `PrivacyGuarantees.vue` — the right-rail panel with small-uppercase status (e.g., `APPLIED`) and checkmarked guarantees (e.g., "Credentials never persisted", "Event-log redaction applied", "Local-first: zero outbound traffic").
2. `StatusPill.vue` (real implementation) — bottom-left footer triple `Active provider · Trust tier · Harness build` parallel to Kenaz's `Model · Tier · Build`.
3. `LiveRateIndicator.vue` — small inline rate indicator (e.g., `0.4 e/s`) consumed by event streams and any continuously-emitting toggle.

## Spec references

- FR-001e (privacy-guarantees panel pattern)
- FR-001f (Model · Tier · Build status footer parallel for the harness)
- FR-001g (live-rate inline indicators)
- FR-020 (no credential values in UI — privacy panel reinforces)
- C-004 (no credential values in UI)
- C-009 (VM-host visual coherence)
- C-010 (enterprise-first defaults)

## Plan references

- §2.1 (`PrivacyGuarantees.vue` under `components/ui/`; `StatusPill.vue` under `shell/`)
- §3.1 (`ShellStatus { ActiveProvider, TrustTier, HarnessBuild, Connection, EventRate, PolicyApplied, RedactionOn, LocalFirstOn }`)
- §7 v1.0 item 15 (Privacy-guarantees panel primitive; Model · Tier · Build status footer; live-rate inline indicators)

## Subtasks

- T001 — Implement `frontend/src/components/ui/PrivacyGuarantees.vue` with typed props `{ status: 'APPLIED' | 'PARTIAL' | 'OFF'; guarantees: ReadonlyArray<{ label: string; on: boolean }> }`. Render small-uppercase status, list with `--ok` checkmarks for `on=true`, `--ink-dim` for `on=false`. No credential values ever rendered (defence-in-depth assertion in a TS type guard at the component boundary).
- T002 — Replace `StatusPill.vue` placeholder from WP06 with a real implementation rendering the triple `Active provider | <name>`, `Trust tier | <tier>`, `Harness build | <build>` driven by the `ShellStatus` returned from `HarnessAPI.ShellStatus()` (consumed via composable from WP12). Use monospace for values, sans for labels.
- T003 — Implement `frontend/src/components/ui/LiveRateIndicator.vue` with typed props `{ rate: number; unit: string; precision?: number }`. Render `0.4 e/s`-style label in `--ink-subtle` monospace. Wire it into `LegendBar.vue` next to the event-stream pause/resume toggle (from WP08).
- T004 — Add Vitest + axe-core tests for each primitive. Add a unit test asserting `PrivacyGuarantees.vue`'s prop type does not accept any field named `value`, `secret`, or `password` (compile-time guard via TS conditional types).

## Acceptance criteria

- The Shell renders the privacy panel slot, status footer, and rate indicator in their Kenaz positions.
- `ShellStatus { PolicyApplied, RedactionOn, LocalFirstOn }` flags drive the privacy panel's three default guarantees.
- The status footer reflects `ActiveProvider`, `TrustTier`, `HarnessBuild` from a stub `ShellStatus` (real wiring via composable in WP12).
- The rate indicator updates live without thrash (rAF-throttled) when fed a synthetic rate stream.
- Axe-core scans pass for all three primitives.
- WP04 CSS-token check passes; WP05 strict CSP passes.

## Files to create/modify

- Create: `frontend/src/components/ui/PrivacyGuarantees.vue`, `LiveRateIndicator.vue`.
- Modify: `frontend/src/shell/StatusPill.vue` (real implementation).
- Modify: `frontend/src/shell/LegendBar.vue` to host `LiveRateIndicator`.
- Create: Vitest tests for each primitive.
- Update: `docs/design-primitives.md` listing all three primitives with prop signatures.

## Definition of done

- All acceptance criteria pass.
- Cross-mission note: the privacy panel's "Credentials never persisted" guarantee is read-only and reflects state surfaced by `secrets-keychain.Resolver` (referenced via reference-only metadata; no resolved values ever cross the boundary, per FR-020 / C-004).
- Cross-mission note: the privacy panel's "Event-log redaction applied" guarantee reflects a flag from the `event-log` mission's redaction pipeline.
