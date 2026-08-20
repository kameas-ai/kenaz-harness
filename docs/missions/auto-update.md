# Auto-Update — Rollout Guide

Mission: `auto-update` (v0.4.0)

## Background

Kenaz Harness ships fast — users now upgrade multiple times per week as
the kernel-graph refactor, cedar policy gates, and OpenRouter resilience
work all land on staggered cadences. The pre-v0.4.0 story was "download
a fresh DMG / tarball / installer from docs.kameas.ai and re-launch by
hand"; that cost a context switch every time and made the friction
proportional to release velocity. The auto-update mission pulls that
loop inside the binary: a tiny dot indicator in the titlebar fades in
when a newer version is published, the staged binary pre-downloads in
the background, and a single click swaps + restarts. The flow is
unobtrusive (popover, not modal), pre-emptive (no waiting on click),
and reversible (skip per-version, choose channel).

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01+WP02 | `core/update` service + Windows boot-swap shim (`core/update/bootswap`) | Merged (PR #37) |
| WP03 | `core/rpc/views/update` RPC view + Wails bindings | Merged (PR #40) |
| WP04 | `UpdateIndicator` + `UpdateMenu` + `UpdateToast` Vue components | Merged (PR #38) |
| WP05 | `UpdatesPanel.vue` Settings tab + Settings RPC fields | Merged (PR #39) |
| WP06 | Audit emission + end-to-end smoke + this mission doc (capstone) | **This branch** |

## Architecture

| Layer | Package / file | Behavior |
|---|---|---|
| **Service** | `core/update/service.go` | Owns the lifecycle: `Check` (manifest fetch + parse), `Download` (streaming + sha256), `ApplyAndRestart` (Swapper dispatch), `SkipVersion`, `BackgroundPoll`. Stateless beyond the on-disk skip set; no shared mutex contention. |
| **Manifest** | `core/update/manifest.go` | Decodes the JSON shape served at `https://docs.kameas.ai/downloads/manifest.json`. Forward-compatible (unknown fields ignored). 1 MiB cap. |
| **Swap (Unix)** | `core/update/swap_unix.go` | Atomic `os.Rename` of the staged path over the running binary, then fork-exec + `os.Exit(0)`. Works while the binary is executing on macOS and Linux. |
| **Swap (Windows)** | `core/update/swap_windows.go` | Cannot rename a running `.exe`. Default path: spawn the bundled `kenaz-updater.exe` helper, exit. Fallback path (helper missing or spawn fails): write `<DataDir>/update/pending.json` for the bootswap shim. |
| **Helper updater (Windows)** | `cmd/kenaz-updater/main.go` | Stand-alone `.exe` shipped next to the harness in the Windows zip. Waits for the parent PID via `WaitForSingleObject` (30s), re-verifies the staged sha256, renames it over the target, and fork-execs the new binary. Logs every step to `<DataDir>/update/updater.log`. |
| **Boot-swap shim** | `core/update/bootswap/` | Safety net for the deferred-pending path. Runs at next launch, re-verifies the staged sha256, renames into place, deletes the marker, and re-execs the new binary. |
| **RPC view** | `core/rpc/views/update/` | Wails-bound surface: `Check`, `Download`, `ApplyAndRestart`, `SkipVersion`, `UnskipVersion`, `ListSkipped`. Adapts `core/update.Service` for the frontend. |
| **Indicator + menu** | `frontend/src/components/UpdateIndicator.vue`, `UpdateMenu.vue`, `UpdateToast.vue` | Titlebar dot + popover + one-shot toast. Subscribes to the broker `update:available` topic. |
| **Settings panel** | `frontend/src/views/settings/UpdatesPanel.vue` | Auto-check toggle, channel picker, interval selector, skipped-versions list (with "Restore" affordance via `UnskipVersion`). |
| **Audit** | `core/update/audit.go` (helpers) + `core/context/audit/audit.go` (kinds + payloads) | Six lifecycle kinds; payloads carry only versions, sizes, durations, platform tuples, booleans, and typed error class labels. |

## Manifest contract

The manifest is the single source of truth for "what's the latest". It
lives at:

- Stable: `https://docs.kameas.ai/downloads/manifest.json`
- Prerelease: `https://docs.kameas.ai/downloads/manifest-prerelease.json`
  (today the prerelease URL 404s and the Service falls back to stable
  with a warn log; the fallback is removed once the prerelease channel
  ships its own JSON.)

Shape:

```json
{
  "version": "v0.4.0",
  "notes": "Kernel-graph migration + auto-update pre-flight.",
  "assets": [
    {"platform": "darwin/arm64", "url": "https://...", "sha256": "<hex>"},
    {"platform": "darwin/amd64", "url": "https://...", "sha256": "<hex>"},
    {"platform": "linux/amd64",  "url": "https://...", "sha256": "<hex>"},
    {"platform": "windows/amd64","url": "https://...", "sha256": "<hex>"}
  ]
}
```

The `sha256` field is mandatory and verified inline during `Download`
(streaming, single-pass — the file is hashed during the network read,
not after a second `os.Open`). A mismatch trips `errSha256Mismatch`,
deletes the staged file, and emits `update.failed` with
`error_class="sha_mismatch"`. The bootswap shim re-verifies on Windows
before the rename, so a corrupted staging file never reaches the
running binary path.

## Per-platform install

| Platform | Strategy |
|---|---|
| **macOS** | Atomic `.app` bundle rename + fork-exec. Works while the binary is running because the rename is on the bundle directory, not the running Mach-O image. The OS closes file descriptors against the renamed path; the new binary starts with a fresh image. Tarball install assumed for non-bundle distributions. |
| **Linux** | Same atomic rename + fork-exec pattern. Tarball / AppImage install assumed; desktop-entry remains stable across versions. |
| **Windows (Install & Restart)** | The Service spawns `kenaz-updater.exe` (bundled next to the main binary in **both** Windows distribution channels as of `entry-points-and-crash-reporting-01PMZD13` UNIT-2, 2026-08-19 — the release zip via release.yml's build step, and the NSIS installer via `build/windows/installer/project.nsi`'s `File` line) with `--parent-pid`, `--staged`, `--target`, `--sha256`, plus repeated `--launch-args` carrying the original argv. The harness then `os.Exit(0)`s. The helper waits up to 30s for the parent PID via `WaitForSingleObject`, re-verifies the staged sha256, atomically renames it over the running .exe path (which is now unlocked), and `exec.Cmd.Start`s the new binary. Total UX: identical to Mac/Linux — user clicks "Install & Restart", app exits, new version comes back up — for both channels as of this date. Before UNIT-2, the NSIS-installed channel silently lacked the helper and always fell to the row below, so "Install & Restart" quit without restarting on that channel; the update was not lost (the fallback still applied it at the next manual launch). |
| **Windows (deferred-swap fallback)** | If the helper is missing (hand-curated or third-party repackaged install stripped it) or `cmd.Start` fails (AV quarantine, disk error), the Service falls back to writing `<DataDir>/update/pending.json` (struct: `target_path`, `staged_path`, `sha256`, `target_version`, `platform`) and exiting. The bootswap shim picks it up on next launch, re-verifies the sha256, renames into place, deletes the marker, and re-execs. The user has to relaunch by hand in this fallback; the helper-spawn path is the production default for both channels. |

## UX

- **Tiny dot indicator** in the titlebar (Chrome-style). Fades in only
  when an actionable update is available (`Available && !SkippedByUser`).
  Pulses while downloading, solid when staged, red when the pre-fetch
  failed (e.g. sha mismatch). Click opens the popover.
- **Compact popover, not a modal.** Shows: current version, available
  version, "what's new" snippet (truncated, with "Read more" linking
  to the docs release page), single primary action ("Install &
  restart"), secondary actions ("Skip this version", "Remind me later").
  Keyboard-dismissable.
- **Pre-download in background.** Once `BackgroundPoll` detects a newer
  version, the indicator fades in and `Download` is kicked off
  optimistically. By the time the user clicks "Install", the staged
  file is already verified. Apply is instant.
- **One toast per detected version per session.** Surfacing a banner
  every Check would be noisy; the toast fires once when
  `Info.Available` transitions false→true (same trigger that fires the
  `update.available` audit event). Subsequent ticks are silent until
  the user either skips or installs.

## Settings

- **Auto-check toggle** — opt out entirely (off keeps the indicator
  hidden).
- **Channel** — `stable` or `prerelease`.
- **Interval** — 1h / 6h / 24h. Default 24h on first install; users
  who want to track prerelease typically tighten to 1h or 6h.
- **Skipped versions** — list view of versions the user dismissed,
  with a per-row "Restore" affordance (`UnskipVersion`) so a too-eager
  skip is reversible.

## Audit kinds + payloads

All six kinds are declared in `core/context/audit/audit.go`. Payload
fields carry only versions / sizes / durations / platform tuples /
booleans / typed error class labels — never URLs (which can carry
signed query tokens), never the manifest body, never release-notes
content.

| Kind | Trigger | Payload (`audit.*Attrs`) |
|---|---|---|
| `update.checked` | Every successful `Check` (manual or background poll tick) | `Channel`, `ResultVersion`, `Took` (ms) |
| `update.available` | `Info.Available` transitions false→true during `BackgroundPoll` | `CurrentVersion`, `AvailableVersion`, `Channel` |
| `update.downloaded` | `Download` success (after streaming sha256 verify) | `Version`, `Bytes`, `Sha256Match` (always true on success) |
| `update.applied` | Fired *immediately before* the platform `Swap` call in `ApplyAndRestart` so the event lands even if the subsequent fork-exec kills the process | `FromVersion`, `ToVersion`, `Platform` |
| `update.skipped` | `SkipVersion` success | `Version`, `Reason` (label) |
| `update.failed` | Any classified failure across Check / Download / Apply | `Action` ∈ {`check`, `download`, `apply`}, `ErrorClass` ∈ {`network`, `sha_mismatch`, `swap_failed`, `manifest_invalid`, `other`} |

Error classification is conservative — unknown errors collapse to
`other` rather than leaking a new error string into the log. Network
errors are detected via `errors.As(err, &netErr net.Error)` so wrapped
DNS / dial / read errors all classify uniformly.

## Acceptance Criteria

1. **Lifecycle audit coverage** — every successful Service call emits
   exactly one event of the corresponding kind; every classified
   failure emits exactly one `update.failed` event with an actionable
   `ErrorClass`. The integration smoke
   (`core/update/integration_test.go`) asserts the four happy-path
   kinds fire in order on a NewService → Check → Download → Apply
   round-trip.
2. **Privacy invariant** — no audit payload contains a URL, the
   manifest body, or release-notes content. The integration smoke
   walks the recorded payloads and asserts `http://` and `https://`
   never appear.
3. **Sha256 mismatch is loud** — a Swapper error wrapping
   `errSha256Mismatch` produces `update.failed` with
   `error_class="sha_mismatch"`. `update.applied` still fires (so the
   audit log records the attempt) and precedes `update.failed`.
4. **Skip is reversible** — `SkipVersion` fires `update.skipped`,
   `Check` continues to return `Available=true` with
   `SkippedByUser=true`, and `UnskipVersion` removes the entry so the
   banner re-appears on the next Check.
5. **Background poll is idempotent on `update.available`** — the
   transition false→true fires exactly once; subsequent ticks where
   `Available` stays true do NOT re-emit.
6. **Channel fallback works** — a 404 on the prerelease manifest URL
   transparently falls back to the stable manifest with a warn log
   (`update.manifest.prerelease_missing`). `Check` returns the stable
   `Info` to the caller.
7. **No emitter, no leak** — `Audit: nil` in the Config disables every
   audit emission silently. The Service keeps running; existing test
   suites remain green.

## Open follow-ups

1. **Windows helper — elevated install paths** — `kenaz-updater.exe`
   today assumes the running binary lives in a user-writable location
   (`%LOCALAPPDATA%\Programs\Kenaz\` or a portable folder). An install
   under `Program Files` is read-only without UAC elevation; the
   rename will fail with `ERROR_ACCESS_DENIED` and the helper exits 1
   so the user falls through to a manual reinstall. Future v0.5.x: ship
   a manifest entry that triggers a UAC prompt + ShellExecute(verb=runas)
   re-spawn of the helper, or a per-machine service path. Out of scope
   for v0.4.0 because the published installer puts Kenaz under LocalAppData.
2. **Windows helper — AV / SmartScreen first-run flagging** — the
   first build of `kenaz-updater.exe` is signed by Trusted Signing
   alongside the main binary, but Microsoft Defender SmartScreen still
   builds reputation per filename. A small fraction of users may see
   a "Windows protected your PC" dialog the first time the helper
   runs. Mitigation: Trusted Signing's reputation aggregates across
   the Kameas publisher identity, so subsequent releases inherit the
   reputation built by the harness exe. Track via release-day
   telemetry in v0.4.x.
3. **Richer changelog UI** — the popover today shows a truncated
   `notes` blurb. A future iteration would render a real markdown view
   with diff-summary bullets sourced from the manifest's
   `notes_markdown` field. Requires a manifest schema bump.
4. **Signed binary verification post-download** — sha256 is sufficient
   to detect corruption / MITM. Cosign-style signature verification
   (the manifest carries an additional `signature` field; the public
   key is shipped with the binary) would defend against a manifest-
   server compromise. Tracked separately; requires a key-management
   plan (KMS rotation, embedded public-key staleness).
5. **Process-wide `audit.Emitter` wiring in the rpc layer** — today
   the chassis runs with `Audit: nil` because the rpc layer hasn't
   materialized a process-wide emitter yet (see
   `core/rpc/api.go:1654` `TODO(audit)`). Once the event-log mission
   lands the `core/event.NewEmitter` + `redact.Pipeline`, the update
   Service config gains `Audit: a.eventEmitter` and the privacy-CI
   guard switches from "no emitter, no leak" to "emitter on, payloads
   redacted".

## Logging / Observability

Every Service call logs at info level via `core/logging`:

- `update.check` — `channel`, `current`, `available`, `actionable`.
- `update.download.done` — `version`, `bytes`, `sha256`, `platform`.
- `update.apply.begin` / `update.apply.swapped` — `version`, `platform`,
  `sha256`.
- `update.apply.failed` — wraps the Swapper error; classified separately
  through the audit emitter.
- `update.poll.failed` / `update.poll.announced` — per-tick signalling.
- `update.skip` / `update.unskip` — the Settings round-trip.
- `update.manifest.prerelease_missing` — the fallback log line; gone
  once the prerelease manifest ships.

## Out of scope

- Auto-rollback on a bad release. The user-flow is "skip this version"
  — there's no telemetry pipeline yet to detect a bad release
  automatically.
- Delta updates. Every download is a full binary fetch. The CDN object
  is small enough (~30 MB) that a delta-update story isn't worth the
  schema complexity.
- Cross-channel migration (stable → prerelease and back). Channel is
  a read-only choice today; a future iteration would warn on
  downgrade-by-channel and let the user opt in explicitly.
