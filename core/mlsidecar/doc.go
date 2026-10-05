// Package mlsidecar implements the harness's half of the kenaz-ml
// sidecar's install + lifecycle protocol (laya-advisors-01LAYA001 WP12,
// per kitty-specs/laya-advisors-01LAYA001/research/kenaz-ml-integration-
// design.md — "THE build contract").
//
// The real kenaz-ml sidecar does not exist yet: it does not serve
// /v1/recommend, /v1/systemone, /v1/contracts, or /v1/clients/lease
// today (spec OQ-4; design §9 Phase 1). Every type and function in this
// package is built and tested against a STUB HTTP server
// (sidecar_stub_test.go) speaking the wire shapes the design specifies —
// nothing here spawns Python, and nothing here reaches a real network
// (httptest + local files only, per the WP12 brief's hard constraint).
//
// # Scope
//
// This package owns the CLIENT side of the protocol described in design
// §3.4/§3.5/§3.7:
//
//   - Layout (paths.go): the shared install root
//     ($XDG_DATA_HOME/kameas/ml or ~/Library/Application Support/kameas/ml)
//     — versions/<semver>/, checkpoints/<sha-prefix>/, current (a
//     symlink), lease/, install.json. Named once, here, per design §3.4's
//     "new frozen interface" ruling.
//   - install.json bookkeeping (installjson.go): who installed what
//     artifact digest, when, from which source.
//   - VERIFIED adoption (adopt.go, design F2 / §3.7 R2): a client NEVER
//     trusts a running process's self-report. It verifies the on-disk
//     artifact the process claims to run from (client-side, reusing
//     core/bundle/integrity + core/trust — "no second verifier"), then
//     cross-checks the process's reported identity against that
//     verified artifact. Mismatch refuses adoption.
//   - The file-based lease protocol (lease.go, design §3.5/§3.7 R4/R5):
//     mtime-heartbeat lease files under lease/, a spawn O_EXCL lock, a
//     local shutdown-authorization token. The sidecar's own
//     self-termination and stale-lease sweep are the SIDECAR's job
//     (Python, a different mission) — this package only writes,
//     refreshes, and releases the harness's own lease, and implements
//     the pid-liveness check a lease-holder needs when BREAKING a stale
//     spawn lock left by a crashed process.
//   - Download + install (install.go, design §6.2): A-1 channel fetch
//     (core/bundle/channels), verify-before-run, darwin-only
//     quarantine clearing AFTER verification (design F3/§3.7 R3),
//     rename-swap into versions/, never exec-in-place.
//   - Update = flip-and-respawn (update.go, design §3.5/§3.7 R6): stage
//     and verify the new version, atomically swap `current`, request a
//     token-authorized graceful shutdown, and let the next health poll
//     (from either client) respawn under the spawn lock.
//   - Skew-window semantics (adopt.go, design F5/§3.7 R4): a pre-lease
//     engine (no lifecycle_protocol on /health, no /v1/clients/lease) is
//     adopt-only — never terminated, never double-spawned.
//   - The health/state machine (manager.go): six distinct, honestly
//     surfaced states (not_installed / installing / healthy /
//     installed_unhealthy / unverified / contract_unsupported), refined
//     by a Reason for the failure-table cases design §7 enumerates
//     (port_conflict, digest_mismatch, update_pending, ...). Health
//     polling piggybacks the lease heartbeat (one 30s cadence, not two).
//     Manager.Ensure implements "lazy start on first advisor demand, not
//     app boot" — nothing in this package spawns anything until a
//     caller asks.
//   - probe.go adapts Manager into core/advice.SidecarProbe — the WP12
//     amendment that replaces ResolveAdvisorModel's placeholder
//     ollama-profile scan with a real sidecar-health probe.
//
// # What this package does NOT do
//
// It does not implement the sidecar itself (Python, kenaz-ml repo). It
// does not implement checkpoint-pack distribution or fine-tuning (design
// Phase 3). It never writes outside a caller-supplied root.
//
// # WP13 additions (laya-advisors-01LAYA001)
//
// The Settings surface (core/rpc/views/sidecar + the Recommendations
// panel) and the production wiring (core/rpc/sidecar_wiring.go) now sit
// on top of this package. What WP13 added here: .dmg artifact handling
// (dmg.go — verify the DMG BYTES, THEN mount/copy/detach), the
// production ProcessSpawner (spawn.go — detached, KENAZ_ML_INSTALL_ROOT,
// lease/ created first), startup health polling, the read-only Observe
// (observe.go), the demand-driven DemandProbe (demand.go — lazy start on
// first advisor demand, zero cost when never enabled), the engine pin
// (release.go) and the real engine's lease/shutdown.token name.
//
// # Engine publication (engine-publication-01ENPUB01)
//
// The pin is build-time: pinned_release_gen.go, written by
// `go run ./cmd/kenaz-ml-sign pin-gen` (pin.go validates + renders it);
// its zero value keeps PinnedEngineRelease honestly unavailable. The
// signature it names is made by cmd/kenaz-ml-sign over EngineManifest's
// SigningPayload, and verified against the trust store, which boot
// seeds with the compiled-in release key (release_key.go,
// release_signing_key.pub — a placeholder until the owner swaps in the
// real public key) without ever overriding operator/fleet anchors.
package mlsidecar
