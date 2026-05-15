# Sentry Error Monitoring — Operator & User Guide

Mission: `sentry-error-monitoring-01KX5R8G`

## Background

When the harness crashes or surfaces an unhandled error, operators have no
visibility unless the user manually reports it. This mission adds opt-in crash
reporting via Sentry (or any Sentry-compatible ingest), with a mandatory
multi-layer redaction pass so that conversation content, API keys, and personal
data are never transmitted.

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | core/sentry package — tier model, redactor, breadcrumb buffer, client interface | Shipped |
| WP02 | Panic handlers + main.go wire-up + Settings fields + KindPanicRecovered | Shipped |
| WP03 | SlogHandler + RPC binding boundary recovery + P0 redaction integration test | Shipped |
| WP04 | Frontend: @sentry/vue lazy load + JS redactor + Vue error handler | Shipped |
| WP05 | Settings panel + onboarding modal + SentryClient wiring + LegendBar pill | Shipped |
| WP06 | Local report test + operator docs | Shipped |

---

## Architecture

### Tiers

| Tier | What is sent | Identity |
|---|---|---|
| **Off** (default) | Nothing | — |
| **Anonymous** | Redacted stack traces + error types | No user identity |
| **Identified** | Same as Anonymous + fleet identity | Requires fleet sign-in; auto-downgrades on logout |

The tier is stored in `settings.crashReportingTier` (string: `"off"` / `"anonymous"` /
`"identified"`). The package-level `core/sentry.Init()` function resolves the
tier at startup and creates a `liveClient` (backed by `github.com/getsentry/sentry-go`)
or a nop client.

### Kill switch

Set `HARNESS_SENTRY_DISABLED=1` (or `true` / `yes` / `on`) to force the nop
client regardless of settings. Useful for CI environments that should never
reach an external Sentry endpoint.

```sh
export HARNESS_SENTRY_DISABLED=1
```

### DSN configuration

Self-hosters supply their own Sentry project DSN in Settings → Privacy → Crash
Reporting. The DSN is stored in `settings.sentryDsn`. When the field is empty
and the tier is not Off, the harness defaults to the Kameas-hosted project DSN
(baked in at build time via `-ldflags`).

---

## Four wire-up points

| Point | Package/file | What it covers |
|---|---|---|
| 1. Main recovery | `main.go` → `RecoverMain()` | Top-level panics; flushes 5 s before re-panic |
| 2. Slog handler | `core/sentry.SlogHandler` wraps the existing structured logger | ERROR-level slog records → redacted breadcrumbs |
| 3. Binding boundary | `core/rpc/bindings.go` → `RecoverBinding()` deferred in each binding | Panics inside Wails-bound methods |
| 4. Goroutine recovery | `core/sentry.RecoverGoroutine()` for long-lived goroutines | Swallows, audits `process.panic_recovered`, logs |

---

## Redaction

All data passes through `core/sentry.redactEvent()` in the `beforeSend` hook
(backend) and `redactSentryEvent()` in the frontend's `beforeSend` callback.

### 11 secret pattern classes

| Class | Pattern |
|---|---|
| `@secret:` refs | `@secret:[^\s]+` |
| `sk-ant-` API keys | `sk-ant-[A-Za-z0-9_-]{10,}` |
| `sk-proj-` API keys | `sk-proj-[A-Za-z0-9_-]{10,}` |
| Generic `sk-` keys | `sk-[A-Za-z0-9]{20,}` |
| Bearer tokens | `Bearer [A-Za-z0-9._~+/-]+=*` |
| Bare JWTs | `eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*` |
| AWS key IDs | `AKIA[0-9A-Z]{16}` |
| AWS secret keys | `[0-9a-zA-Z/+]{40}` (32+ chars after known prefix) |
| Email addresses | RFC 5322 local-part @ domain |
| Phone numbers | E.164-style 10–15 digit sequences |
| Home-dir paths | `$HOME` prefix replacement |

Additional rules:
- Structured-log attributes with keys prefixed `private.` are dropped entirely.
- String values longer than 200 characters are truncated to 50-char head + `…[N chars truncated]…` + 20-char tail.

### What we never send

- Conversation messages or prompt text
- API keys, tokens, or credentials
- File contents or filesystem paths outside the harness data dir
- Email addresses or phone numbers
- Attributes marked `private.` in structured logs
- Long binding-arg strings (>200 chars, truncated)

---

## User-facing surfaces

### First-launch modal

On first run, `CrashReportingOnboardingModal.vue` is shown once. The user may
choose **Send anonymous reports** (tier = anonymous) or **Skip / No thanks**
(tier = off). The choice is persisted immediately and the modal never appears
again (`hasSeenCrashReportingOnboarding = true`).

### Settings → Privacy → Crash Reporting

The `CrashReportingPanel.vue` exposes:

- **Tier picker** — Off / Anonymous / Identified radio group
- **Sentry DSN** — text field with a **Test** button (issues a HEAD to the
  ingestion URL; accepts any 2xx or 4xx as "reachable")
- **Recent events** — last 5 captured events from the on-disk cache
  (`<dataDir>/sentry-cache.json`)
- **Generate local crash report** — writes a redacted JSON file to
  `<dataDir>/crash-reports/YYYY-MM-DD-HHMMSS.json` (mode 0600) for
  offline sharing with support. No network call is made.

### LegendBar indicator

When crash reporting is active (tier != off), a discreet `reporting: <tier>`
pill appears in the bottom status bar as a visible reminder.

---

## Local crash reports

The **Generate local crash report** action writes a JSON file containing:

- `generated_at`, `harness_version`, `os`, `go_version`, `arch`
- `breadcrumbs` — last ≤50 slog ERROR-level records (redacted)
- `last_five` — last ≤5 Sentry-captured events from the cache

The file undergoes two redaction passes:
1. Structured per-field redaction (breadcrumbs + last_five)
2. Full `RedactString()` pass over the final JSON bytes

File permissions are `0600`; only the process owner can read it.

---

## Operator deployment

### Self-hosted Sentry

1. Create a project at your Sentry instance and copy the DSN.
2. Open Settings → Privacy → Crash Reporting.
3. Set tier to **Anonymous** (or **Identified** if fleet sign-in is configured).
4. Paste the DSN and click **Test** to verify connectivity.
5. Click **Confirm** (or the tier radio saves automatically on change).

### Disabling in CI / unattended mode

```sh
export HARNESS_SENTRY_DISABLED=1
# or pass at build time:
go build -ldflags "-X main.SentryDSN="
```

### On-disk layout

```
<dataDir>/
  sentry-cache.json        # last ≤5 events (JSON array, ≤50 KB)
  crash-reports/
    2026-05-15-174500.json # per-invocation local reports (mode 0600)
```

---

## Privacy invariant (P0 gate)

`core/sentry.TestIntegration_RedactionGate` asserts all 11 secret pattern
classes are stripped from both the string redactor and the `beforeSend`
pipeline. This test is required to pass after every subsequent WP. Run it:

```sh
go test -race -run TestIntegration_RedactionGate ./core/sentry/...
```

`core/sentry.TestGenerateLocalReport_SecretRedaction` verifies planted secrets
do not appear verbatim in the local crash report bytes:

```sh
go test -race -run TestGenerateLocalReport_SecretRedaction ./core/sentry/...
```
