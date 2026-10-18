package mlsidecar

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// SupportedContractMajor is the lifecycle-protocol major this build of
// the harness speaks — compared against /health's `lifecycle_protocol`
// (design §6.4's "contract-major on /health"). The kenaz-ml engine
// publishes no API-wide contract number on /health (its
// `contract_versions` are per-kind feature-contract hashes, negotiated
// per call through /v1/contracts), so the integer lease protocol is the
// only protocol version there is to gate adoption on (interop ruling
// 2026-09-30). A single package constant rather than a per-call
// parameter, matching how the rest of the harness pins its own protocol
// version once per build.
const SupportedContractMajor = 1

// AdoptAction is EvaluateAdoption's verdict.
type AdoptAction string

const (
	// AdoptAccept: the running process is a client-verified install;
	// adopt it, spawn nothing.
	AdoptAccept AdoptAction = "adopt"
	// AdoptRefuseUnverified: a process is listening, but this client
	// could not verify it runs from a client-verified shared-root
	// install (design F2/§3.7 R2). Never adopted; never killed.
	AdoptRefuseUnverified AdoptAction = "refuse_unverified"
	// AdoptPortConflict: a process is listening on the engine port that does not
	// resolve to any install this client recognizes at all (foreign
	// process, or no `current` exists yet). Never killed.
	AdoptPortConflict AdoptAction = "port_conflict"
	// AdoptLegacyUnverified: a pre-lease engine (design F5/§3.7 R4,
	// AMENDED by the 2026-09-29 security-review ruling — see
	// StateLegacyUnverified's doc comment). Never terminated, never
	// double-spawned, but NEVER usable for recommendations: it was not
	// installed by this client, so there is no install.json record to
	// verify it against, regardless of contract-major.
	AdoptLegacyUnverified AdoptAction = "legacy_unverified"
	// AdoptContractUnsupported: the running engine's contract-major is
	// outside what this client build understands (design §3.7 R7). This
	// client alone degrades to a read-only stranded state; the shared
	// instance is left running for other, compatible clients.
	AdoptContractUnsupported AdoptAction = "contract_unsupported"
)

// AdoptDecision is EvaluateAdoption's full result.
type AdoptDecision struct {
	Action AdoptAction
	Reason Reason
	Health HealthPayload
	Detail string
}

// EvaluateAdoption implements design F2 ("Adopt-first, verified"): a
// client NEVER trusts a running process's self-report. It:
//
//  1. Checks lifecycle_protocol for the skew-window case (design F5,
//     AMENDED 2026-09-29 — see StateLegacyUnverified's doc comment): a
//     pre-lease engine is ALWAYS unverifiable and therefore never usable
//     for recommendations, regardless of contract-major. R2's universal-
//     verification rule ("a process not running from a client-verified
//     shared-dir install is a port-conflict state, never an adoptee")
//     applies here without exception — a legacy engine was, by
//     definition, not installed by this client, so there is no
//     install.json record to check it against.
//  2. Checks this client's own contract-major compatibility (design
//     §3.7 R7) for a LEASE-AWARE engine — an incompatible engine strands
//     this client regardless of how well-verified the binary is.
//  3. Resolves health.ExePath: it must point inside THIS client's own
//     `current` version directory. A process running from anywhere
//     else is a port conflict, never an adoptee.
//  4. Re-verifies the ON-DISK install at that version against
//     install.json via VerifyInstalled — the launcher AND the whole
//     onedir tree, under the cross-client trust rule (design Amendment
//     A5(3)+(4): a record written by EITHER client, harness
//     "kameas-channel-manifest" or Kenaz "kenaz-bundle-digest", is
//     accepted when the bytes match it). No network call, no trusting the
//     remote process.
//  5. Cross-checks the process's self-reported digest against that
//     independently-verified record. "Reports are cross-checks, never
//     trust roots" (WP12 brief): a mismatch here is exactly the F2
//     planted-violation proof (adopt-refused-on-digest-mismatch). A
//     sidecar_version mismatch is only noted (A5(4)).
//
// health is the already-fetched /health payload; EvaluateAdoption makes
// no network call itself so it is trivially unit-testable against
// hand-built HealthPayload fixtures, independent of the stub server
// (adopt_test.go) — the stub-backed integration path is covered by
// manager_test.go instead.
//
// tv is the per-process whole-tree verify cache (the Manager's, design
// A5(3)); a nil tv re-hashes the whole tree on every call.
func EvaluateAdoption(layout Layout, health HealthPayload, tv *TreeVerifier) (AdoptDecision, error) {
	if health.LifecycleProtocol == 0 {
		// AMENDED (2026-09-29 security-review ruling, supersedes the
		// original F5 text): a pre-lease engine is UNCONDITIONALLY
		// unverifiable — it was not installed by this client, so no
		// install.json record exists to check it against, and
		// contract-major compatibility cannot substitute for that. The
		// pre-amendment code branched on contractCompatible here and
		// returned AdoptLegacy (mapped to StateHealthy) for a
		// "compatible" legacy engine; combined with contractCompatible's
		// documented "no api entry => compatible" leniency, a process
		// answering /health with a bare `{}` was adopted as healthy.
		// This branch no longer consults contractCompatible at all: EVERY
		// pre-lease engine is AdoptLegacyUnverified, full stop.
		return AdoptDecision{
			Action: AdoptLegacyUnverified,
			Reason: ReasonLegacyEngine,
			Health: health,
			Detail: "pre-lease engine (no lifecycle_protocol): unverifiable against this client's install record — update Kenaz to share the ML engine. Never terminated, never double-spawned, never used for recommendations.",
		}, nil
	}

	if !contractCompatible(health, SupportedContractMajor) {
		return AdoptDecision{
			Action: AdoptContractUnsupported,
			Health: health,
			Detail: "running engine's contract-major is unsupported by this client build",
		}, nil
	}

	currentDir, err := layout.CurrentVersionDir()
	if err != nil {
		// No `current` this client recognizes: whatever is on the port is
		// not an install this client can vouch for.
		return AdoptDecision{Action: AdoptPortConflict, Health: health, Detail: "no verified `current` install to compare against: " + err.Error()}, nil
	}
	if !pathIsWithin(health.ExePath, currentDir) {
		return AdoptDecision{Action: AdoptPortConflict, Health: health, Detail: "exe_path does not resolve inside this client's `current` install"}, nil
	}

	version := filepath.Base(currentDir)
	rec, verr := VerifyInstalled(layout, version, tv)
	if verr != nil {
		if errors.Is(verr, errInstallRecordUnreadable) {
			return AdoptDecision{}, verr
		}
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "install not verifiable: " + verr.Error()}, nil
	}

	// The file the process actually runs from must itself carry the
	// verified launcher bytes (the tree check above covers it too; this
	// pins exe_path to the launcher specifically).
	if err := VerifyFileSHA256(health.ExePath, rec.EngineSHA256); err != nil {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "the process's executable is not the verified engine launcher: " + err.Error()}, nil
	}

	// Cross-check the process's self-report against the independently
	// verified record — a mismatch here means the running process is
	// lying about what it is, or is a different build entirely that
	// happens to share the port. Reports are cross-checks, never roots.
	if health.EngineSHA256 != "" && !digestsEqual(health.EngineSHA256, rec.EngineSHA256) {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "reported engine_sha256 does not match the verified install record"}, nil
	}

	// sidecar_version is NOT a gate (A5(4) relaxed the exact match to
	// record-vs-directory-label consistency, enforced in VerifyInstalled):
	// engine builds report 0.1.0 everywhere today and Kenaz labels are
	// "<semver>+<sha12>", so a mismatch is noted, never refused.
	detail := "verified install (provenance " + rec.Provenance + "), verified process identity"
	if health.SidecarVersion != "" && !versionLabelConsistent(version, health.SidecarVersion) {
		detail += fmt.Sprintf("; note: engine reports %q, version label is %q", health.SidecarVersion, version)
	}
	return AdoptDecision{Action: AdoptAccept, Health: health, Detail: detail}, nil
}

// errInstallRecordUnreadable marks an install.json that exists but could
// not be read or parsed — an I/O fault, not a verification verdict.
var errInstallRecordUnreadable = errors.New("mlsidecar: install.json unreadable")

// errNoInstallRecord marks the absence of install.json.
var errNoInstallRecord = errors.New("mlsidecar: no install.json record")

// VerifyInstalled re-verifies versions/<label> against install.json under
// the cross-client trust rule (design Amendment A5(3)+(4)), mirroring
// Kenaz's internal/ml VerifyInstalled so either client adopts (or spawns)
// the other's install on exactly the same evidence:
//
//  1. the record names exactly the version directory `current` points to
//     (record-vs-directory-label consistency);
//  2. its provenance is one of the two known values — the Verified bit is
//     neither required nor sufficient;
//  3. the launcher re-hashes to engine_sha256;
//  4. the WHOLE onedir re-hashes to tree_sha256 (a record that predates A5
//     and carries none is not verifiable). tv caches by stat fingerprint,
//     per process; nil always re-hashes.
//
// Pure on-disk bytes + records: nothing is trusted from a running process.
func VerifyInstalled(l Layout, label string, tv *TreeVerifier) (InstallRecord, error) {
	rec, ok, err := ReadInstallJSON(l)
	if err != nil {
		return InstallRecord{}, fmt.Errorf("%w: %v", errInstallRecordUnreadable, err)
	}
	if !ok {
		return InstallRecord{}, errNoInstallRecord
	}
	if rec.Version != label {
		return rec, fmt.Errorf("install.json describes %q, current is %q", rec.Version, label)
	}
	if !l.acceptsProvenance(rec.Provenance) {
		return rec, fmt.Errorf("install.json provenance %q is not a known installer (verified=%v)", rec.Provenance, rec.Verified)
	}
	if err := VerifyFileSHA256(pathUnderVersionsDir(l, label), rec.EngineSHA256); err != nil {
		return rec, err
	}
	if rec.TreeSHA256 == "" {
		return rec, fmt.Errorf("install record for %q has no tree digest (predates design A5): not verifiable", label)
	}
	if err := tv.Verify(l.OnedirPath(label), rec.TreeSHA256); err != nil {
		return rec, err
	}
	return rec, nil
}

// contractCompatible reports whether health's advertised lifecycle
// protocol is one this client build can speak to. Design §6.4: "the
// engine maintains N and N−1 ... during transitions" — applied here to
// the protocol major that gates adoption at all; per-kind feature-
// contract negotiation (the 16-hex hashes in /health contract_versions
// and /v1/contracts) belongs to the dispatch layer (design §3.2) and to
// SidecarAdvisor's per-call routing, not to adoption.
//
// SECURITY NOTE (2026-09-29 review): this function is NEVER consulted
// for a pre-lease (LifecycleProtocol==0) engine — that branch in
// EvaluateAdoption returns AdoptLegacyUnverified unconditionally, before
// this function would ever run — and every engine that passes it still
// faces the unconditional on-disk digest re-verification a few lines
// below in EvaluateAdoption. (The pre-2026-09-30 version read an "api"
// entry out of contract_versions and treated its absence as compatible;
// the engine never published one, and the field is per-kind now.)
func contractCompatible(health HealthPayload, supportedMajor int) bool {
	p := health.LifecycleProtocol
	return p == supportedMajor || (p == supportedMajor-1 && p > 0)
}

// normalizeDigest lowercases a digest and strips an optional "sha256:"
// prefix. The engine reports /health engine_sha256 as BARE hex, while
// install.json (and core/bundle/integrity) carry the prefixed form — a
// raw != between the two mismatched on every real engine, refusing every
// adoption (design Amendment A5; found by Kenaz's second-client
// implementation, which normalizes the same way).
func normalizeDigest(d string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "sha256:")
}

// digestsEqual compares two digests after normalization; empty never
// equals anything.
func digestsEqual(a, b string) bool {
	na, nb := normalizeDigest(a), normalizeDigest(b)
	return na != "" && na == nb
}

// versionLabelConsistent reports whether an engine's self-reported
// sidecar_version is consistent with the install's version-directory
// label (design Amendment A5(4): the exact-match requirement is relaxed
// to record-vs-directory-label consistency). A label is either the bare
// version ("0.2.0", harness A-1 installs) or the version plus a build
// suffix ("0.2.0+1a2b3c4d5e6f", Kenaz seeds). The self-report stays a
// cross-check, never a trust root — the on-disk digest re-verification
// above is what adoption rests on.
func versionLabelConsistent(label, reported string) bool {
	return label == reported || strings.HasPrefix(label, reported+"+")
}

// pathIsWithin reports whether target is equal to or nested under root,
// after cleaning both. Mirrors the containment check
// Layout.CurrentVersionDir already applies to the `current` symlink
// itself, now applied to a REPORTED path this client must not trust
// blindly.
func pathIsWithin(target, root string) bool {
	if target == "" || root == "" {
		return false
	}
	cleanTarget := filepath.Clean(target)
	cleanRoot := filepath.Clean(root)
	if cleanTarget == cleanRoot {
		return true
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
