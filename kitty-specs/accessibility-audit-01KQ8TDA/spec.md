# Spec: Accessibility audit + remediation

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The token system (`tokens.css`) supports semantic colours and the Vue components mostly use semantic classes. We have not run a contrast or screen-reader pass. WCAG 2.2 AA conformance is the floor for any credible developer tool, and many of our flows (long-running streams, streaming text, tool dispatches) have non-obvious accessibility implications.

## 2. Goals

- Run an automated WCAG 2.2 AA audit (axe-core or Pa11y) against every top-level view.
- Manual screen-reader test (VoiceOver on macOS) of the chat surface end-to-end.
- Keyboard-only walkthrough: send a message, switch model, browse artifacts, navigate to settings.
- Remediate every identified gap.
- Add a CI step that fails on new violations.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | All interactive elements have accessible names (aria-label or visible label). | proposed |
| FR-002 | Color contrast ≥ 4.5:1 for body text, 3:1 for UI affordances. | proposed |
| FR-003 | Streaming text is announced via `aria-live="polite"` with chunked updates (no flooding). | proposed |
| FR-004 | Slash autocomplete is keyboard-navigable (arrow keys + Enter + Esc). | proposed |
| FR-005 | All modals trap focus and restore on close. | proposed |
| FR-006 | All custom controls have visible focus rings (no `:focus { outline: none }`). | proposed |
| FR-007 | Toast notifications announce via `role="status"` or `role="alert"` as appropriate. | proposed |
| FR-008 | CI step runs axe-core against the built app and fails on new AA violations. | proposed |

## 4. Success criteria

- Zero AA violations in the automated audit.
- A screen-reader user can complete the chat → artifact → settings flow without sighted help.
