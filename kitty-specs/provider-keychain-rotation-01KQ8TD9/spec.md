# Spec: Provider keychain rotation flow

**Status**: draft · **Owner**: alecfeeman

## 1. Why

API keys expire / get rotated. Today the harness only learns about it when a stream errors mid-session with `ErrAuth`. The user has no proactive surface to rotate keys before they expire, no test-on-add affordance after the original install, and no way to trigger a re-prompt.

## 2. Goals

- Per-provider "Rotate key" affordance in the Providers view.
- Test-on-rotate: re-runs the existing prober, surfaces success/failure inline.
- Mid-stream auth failure → toast offering "Rotate key" with one click.
- Optional: provider-side expiry hint (when known) → proactive notification 7 days before.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Providers view per-row action: "Rotate key" — opens the existing AddProvider form pre-filled with the row's id and a fresh key input. | proposed |
| FR-002 | On rotate, the prober runs against the new key before the old key is replaced. | proposed |
| FR-003 | Mid-stream `ErrAuth` triggers a toast in the chat surface with a "Rotate key" CTA. | proposed |
| FR-004 | Optional: providers that surface key expiry (Anthropic console returns it) record the date and notify N days before. | proposed |
| FR-005 | Audit log entry on rotation success. | proposed |
| FR-006 | Old key is overwritten in the keychain — no soft-delete. | proposed |

## 4. Success criteria

- A user whose key expired mid-conversation can rotate without leaving the chat surface.
- 100% of rotation failures (bad new key) are caught before the old key is invalidated.
