# Onboarding Handoff + Progress-Mirror Contract

Mission: harness-onboarding-01NHON01 WP08  
Status: **SHIPPED** (WP01–WP06 wired; WP04/WP05/WP07 are graceful seams pending Fleet deployment)

---

## Overview

This document specifies the wire shapes and integration contracts for two
onboarding subsystems:

1. **Fleet handoff intake** (WP04) — accepts a non-authenticating hint from
   the Fleet welcome page so the account step can pre-fill the user's email.
2. **Progress mirror** (WP07) — records named onboarding milestones locally
   and (when Fleet ships) mirrors them to a Fleet shared checklist.

Both systems are **seams** — they accept calls now and record/store the data
locally. The Fleet-side endpoints are not yet deployed. When they ship, the
wiring adapters in `core/rpc/onboarding_wiring.go` are the integration points.

---

## 1. Fleet Handoff Intake (WP04)

### Purpose

The Fleet welcome page (`01NWEL01`) will detect that a user does not yet have
the harness installed and present an "Install Kenaz" CTA. After install, it
will fire a `kameas://onboarding?email=...&source=fleet_welcome` deep-link (or
pass `--onboarding-token email=...` on the CLI) to pre-fill the account step.

**INVARIANT**: The hint is pre-fill only — it carries no auth grant. The actual
sign-in is always the owned-login flow. A missing or malformed hint never blocks
onboarding; the account step starts empty.

### Go interface

```go
// core/rpc/views/onboarding/api.go
type HandoffHint struct {
    EmailHint string `json:"emailHint,omitempty"` // pre-fill; not auth
    Source    string `json:"source,omitempty"`    // "deep_link" | "cli_arg"
}

type OnboardingAPI interface {
    AcceptHandoffHint(ctx context.Context, hint HandoffHint) error
    GetHandoffHint(ctx context.Context) (HandoffHint, error)
    // ...
}
```

### Wails bindings

```ts
// frontend/wailsjs/go/rpc/Bindings.d.ts
export function Onboarding_AcceptHandoffHint(arg1: onboarding.HandoffHint): Promise<void>;
export function Onboarding_GetHandoffHint(): Promise<onboarding.HandoffHint>;
```

### Call sequence

```
Fleet welcome page
    ↓ (deep-link or CLI arg on harness startup)
main.go / app.go startup hook
    ↓ Onboarding_AcceptHandoffHint({emailHint: "user@example.com", source: "deep_link"})
core/rpc/views/onboarding/impl.go API.AcceptHandoffHint
    ↓ stores in a.handoffHint (in-memory; single-instance lifetime)

...later, when account step renders...
frontend OnboardingDialog
    ↓ Onboarding_GetHandoffHint()
    ← {emailHint: "user@example.com", source: "deep_link"}
frontend pre-fills the email input field (display-only)
```

### Fleet integration point

`core/rpc/onboarding_wiring.go` — `onboardingAccountStepAdapter` is the
integration point. When the Fleet sign-in surface ships, add a real
`AccountSigner` implementation here that calls the fleet auth flow.
The `core/onboarding/fsm.go` `AccountSigner` interface is the FSM seam.

---

## 2. Progress Mirror (WP07)

### Purpose

The Fleet platform maintains a shared onboarding checklist per user. When the
harness completes a named onboarding milestone, it should mirror the progress
to Fleet so the web dashboard can display "Kenaz: provider configured ✓" etc.

The mirror is **best-effort and non-blocking**. Errors are logged but never
returned to the caller. The local recording always succeeds.

### Named milestones

| ProgressStep constant | When it fires |
|---|---|
| `provider_configured` | FSM enters `account_step` (provider key verified) |
| `account_connected` | FSM enters `guided_action` with `SignedIn == true` |
| `bootstrap_run` | DEFERRED (WP05 context bootstrap) |
| `guided_action_shown` | FSM enters `done` |

### Go interface

```go
// core/rpc/views/onboarding/api.go
type ProgressStep string

const (
    ProgressStepProviderConfigured ProgressStep = "provider_configured"
    ProgressStepAccountConnected   ProgressStep = "account_connected"
    ProgressStepBootstrapRun       ProgressStep = "bootstrap_run"
    ProgressStepGuidedActionShown  ProgressStep = "guided_action_shown"
)

type OnboardingAPI interface {
    RecordProgress(ctx context.Context, step ProgressStep) error
    // ...
}
```

### Wails binding

```ts
export function Onboarding_RecordProgress(arg1: onboarding.ProgressStep): Promise<void>;
```

### Fleet integration point

`core/rpc/views/onboarding/impl.go` `API.RecordProgress` — marked with a
`DEFERRED FLEET INTEGRATION (WP07)` comment. The integration must be:

- **Non-blocking** (goroutine)
- **Best-effort** (errors logged, not returned)
- **Gated** on `a.fsmCtx.SignedIn` (only mirror when the user is signed in)
- **OSS-first** (no `core/fleet/` import in `core/rpc/views/onboarding/`) —
  the fleet call belongs in the host adapter

The cleanest approach is a `ProgressSyncer` interface in `impl.go` (similar to
`AccountSigner`) wired from `core/rpc/onboarding_wiring.go`.

---

## 3. FSM State Machine (complete, post-WP06)

```
welcome
  → (next) pick_provider_kind
      → (back) welcome
      → (anthropic|openai|openrouter) enter_api_key
          → (back) pick_provider_kind
          → (submit_key fail) enter_api_key  (with error card)
          → (submit_key ok) account_step     ← NEW WP03
              → (skip_account) guided_action ← OSS-standalone invariant
              → (sign_in ok) guided_action
              → (sign_in fail) guided_action (with error card, still advances)
              → (back) pick_provider_kind
          → test_connection (async callers only)
              → (test_ok) account_step
              → (test_fail) enter_api_key
              → (back) enter_api_key
      → guided_action                        ← NEW WP06
          → (start_new_chat | open_settings | finish) done
done  (terminal; EventFinish is a no-op loop)
```

---

## 4. Completion persistence

`Settings.FirstRunOnboardingCompleted` (added WP01) is flipped to `true` by:

1. `Dismiss()` — user explicitly closes the dialog without completing.
2. `Step()` on transition to `StateDone` — via `API.cfg.Completion.MarkOnboardingCompleted`.

The adapter is `onboardingCompletionAdapter` in `core/rpc/onboarding_wiring.go`.
It delegates to `settings.SettingsStore.SaveFirstRunOnboardingCompleted(true)`.

---

## 5. OSS-first invariants (enforced by `check-no-fleet-imports.sh`)

- `core/onboarding/` MUST NOT import `core/fleet/`.
- `core/rpc/views/onboarding/` MUST NOT import `core/fleet/`.
- Fleet seams are interfaces (`AccountSigner`, `AccountStepAvailableChecker`)
  defined in those packages, implemented in `core/rpc/onboarding_wiring.go`.
- The account step is **always skippable**. `EventSkipAccount` is always
  accepted in `StateAccountStep` regardless of fleet availability.
- No provider/tool API key ever leaves the local OS keychain.
