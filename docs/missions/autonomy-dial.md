# Autonomy Dial — Rollout Guide

Mission: `autonomy-dial-01KR3M2A`

Target release: **v0.3.0 beta**.

## Status

| WP | Title | Status |
|---|---|---|
| WP01 | `core/autonomy/` package + preset table | Merged |
| WP02 | Migration 0316 + settings/project/session schema | Merged |
| WP03 | RPC view methods (settings/projects/sessions) | **This branch** |
| WP04 | Toolloop integration | **Shipped** (v0.4.1) |
| WP05 | Cedar prompt registry posture wiring | **Shipped** (v0.4.1) |
| WP06 | Settings global panel | **This branch** |
| WP07 | Project + session panels + chat header chip | **This branch** |
| WP08 | Acceptance smoke + docs | **This branch** |

## What ships in v0.3.0

A user-visible **Autonomy** dial that persists across the
`global → project → session` override chain and renders on the chat
header as a clickable chip. Five tiers (`strict` / `cautious` /
`default` / `bold` / `autonomous`) plus seven independently-tunable
knobs (`maxIterations`, `askOnAmbiguity`, `autoApproveFamilies`,
`tokenCeilingPerTurn`, `recapStyle`, `continueOnError`,
`destructiveActionPosture`).

The resolver (`core/autonomy.Resolve`) is fully wired. Persistence
round-trips through `settings.json`, `projects.autonomy_*` columns,
and `sessions.autonomy_*` columns. The chip in the chat header reads
`Sessions_ResolveAutonomy` so the effective tier label refreshes after
any panel save without a page reload.

## Tier preset table

The preset table lives in `core/autonomy/presets.go`. Stable shape:

| Tier | maxIterations | askOnAmbiguity | autoApproveFamilies | tokenCeilingPerTurn | recapStyle | continueOnError | destructive |
|---|---|---|---|---|---|---|---|
| strict | 5 | always | (none) | low | full | stop | confirm |
| cautious | 10 | hard | read | low-mid | brief | retry-once | confirm |
| default | 25 | major | read,write | (unbounded) | brief | retry-once | confirm |
| bold | 50 | proceed | read,write,shell-safe | mid | brief | adapt | confirm |
| autonomous | 0 (unbounded) | never | read,write,shell-safe,network | high | none | adapt | cedar-only |

Cedar deny remains the floor regardless of tier.

## How to extend

- **New knob**: add the constant in `core/autonomy/knobs.go`, append it
  to `allKnobs` in `core/autonomy/resolve.go`, fill the preset cell for
  every tier in `core/autonomy/presets.go`, add the assign branch in
  `assignKnob` and the parse branch in `decodeKnobValue`. Wire the new
  field into `core/rpc/views/sessions.AutonomyKnobValues` + the
  `toAutonomyKnobValues` projector. Frontend: extend the
  `AutonomyKnob` enum, the `AUTONOMY_KNOB_LABELS` map, and the
  `AUTONOMY_KNOB_ORDER` array in `frontend/src/lib/types.ts`.
- **New tier**: add the constant in `core/autonomy/tier.go`, the case
  in `String()` + `ParseTier`, the row in `presetTable`, and the
  `AUTONOMY_TIER_LABELS` / `AUTONOMY_TIER_DESCRIPTIONS` entries on the
  frontend.
- **New layer** (e.g. organisation-wide): extend `Resolve(global,
  project, session, ...)` and add a fourth column to the panel grid.

## Gaps closed in v0.4.1

**WP04 — Toolloop integration** (shipped v0.4.1): `kernelToolAdapter`
now reads `AutoApproveFamilies` via an `AutonomyKnobsProvider` before
each tool call. When the resolved knobs include `FamilyShellSafe` or
`FamilyNetwork` (bold / autonomous tier), the adapter bypasses the
interactive-prompt branch while still consulting the resolver for
explicit Cedar denials (cedar deny remains the floor). The provider is
wired into `Config.AutonomyKnobs` on `ChatRunner`; nil disables the
behaviour (v0.3.0 baseline). Tests: four cases covering autonomous
bypass, strict fall-through, deny floor enforcement, and nil-provider
baseline.

**WP05 — Cedar prompt registry posture wiring** (shipped v0.4.1):
`core/policy/cedar.Registry` gains a `PromptPosture` type with three
values: `PostureDefault` (v0.3.0 baseline), `PostureAutoAllow`
(autonomous/bold — `RequestInteractive` returns Allow immediately
without dispatching), and `PostureAlwaysPrompt` (strict/cautious —
transient-grants cache is skipped so every call surfaces to the UI).
Configured via `WithPosture` at construction time or changed live via
`SetPosture`. Tests confirm auto-resolve, forced-dispatch, and dynamic
update behaviours.

## Acceptance smoke (manual)

1. Open the harness, navigate to `Settings → General`. Find the new
   **Autonomy** section under "Sessions" and above "Tool execution".
2. Click **Default** in the tier selector. Verify the description
   below the slider updates.
3. Open **Advanced** and set `maxIterations` to `99`. The Custom
   badge should appear next to the section title.
4. Click **Reset** on the same row. The badge disappears.
5. Open a project page, scroll to its Autonomy panel, set tier =
   **Bold**. The chat header chip in any session inside that project
   should now show "Bold".
6. Open a session in that project. Click the chip — the popover opens
   with the session panel. Set the session-level tier to **Strict**.
   The chip label updates to "Strict" on close.
7. Check the **Source** column in the advanced grid: every knob's
   source should read `session` (since we set a session-level tier).
8. Reset the session via "Reset session to project default". The chip
   reverts to "Bold" (the project tier).

## Tests

```bash
# backend
go test ./core/autonomy/... ./core/projects/... ./core/session/... \
        ./core/rpc/views/settings/... ./core/rpc/views/sessions/... \
        ./core/rpc/views/projects/... -race -count=1 -short

# frontend
cd frontend && npm test -- --run
```

The acceptance test
(`core/rpc/views/sessions/autonomy_resolve_test.go::TestResolveAutonomy_InheritanceFlow`)
walks the 6-step inheritance scenario above against the in-memory
session store + injected context provider.

## Follow-ups

- Autonomy-aware compaction: have the compaction tier read the
  session's autonomy.Tier so `autonomous` sessions get more aggressive
  compaction.
- Wall-clock ceiling: a per-turn `tokenCeilingPerTurn` cousin that
  caps elapsed-seconds, not just token spend.
- Drag-to-bump on the chat-header chip (plan §WP07 stretch goal).
