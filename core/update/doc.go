// Package update implements the auto-update subsystem for the Kenaz
// harness shipped in v0.4.0. The bar is "users upgrade multiple times
// per week — make this clean and unobtrusive."
//
// # Surface
//
//   - Service is the only public surface. It checks a remote manifest
//     for the configured release channel, downloads + verifies the
//     platform binary, applies the swap, and restarts.
//
//   - The download path is staged under <DataDir>/update/staging/. SHA-256
//     verification ALWAYS runs before the swap regardless of platform.
//
//   - Swap behavior is per-platform but hidden behind a Swapper
//     interface so tests can run end-to-end without invoking real
//     fork-exec / os.Exit. ApplyAndRestart writes the production swap
//     side effect on macOS/Linux and stages a pending.json marker on
//     Windows that the bootswap shim consumes on next launch.
//
//   - BackgroundPoll is a long-running goroutine; it publishes new
//     Info structs onto a Publisher under topic "update:available"
//     when an actionable update is found. The caller owns the goroutine
//     lifetime via ctx.
//
// # Manifest schema
//
// Stable channel:  https://docs.kameas.ai/downloads/manifest.json
// Prerelease:      https://docs.kameas.ai/downloads/manifest-prerelease.json
//
// JSON shape (only the fields used here):
//
//	{
//	  "version": "v0.4.0",
//	  "notes":   "release blurb or URL",
//	  "assets":  [
//	    {"platform":"darwin/arm64","url":"https://...","sha256":"hex"},
//	    {"platform":"linux/amd64", "url":"https://...","sha256":"hex"},
//	    {"platform":"windows/amd64","url":"https://...","sha256":"hex"}
//	  ]
//	}
//
// Forward-compat: unknown fields are ignored. If the prerelease
// manifest 404s the service falls back to the stable manifest with a
// log line so users on the prerelease channel still see something.
//
// # Privacy
//
// No message content, prompts, attachment names, session ids, or user
// data flow through this package. Logs and audit emit only:
//
//   - target version strings ("v0.4.0")
//   - platform tuples ("darwin/arm64")
//   - sha256 fingerprints
//   - event types ("update.check", "update.download.done", ...)
//
// The mission privacy invariant is enforced by code review; there is
// no plaintext payload that could leak.
package update
