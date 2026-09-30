package mlsidecar

import (
	"path/filepath"
	"strings"
)

// SupportedContractMajor is the `/v1` API contract-major this build of
// the harness understands (design §6.4: "Contracts — /v1 API
// contract-major on /health"). A single package constant rather than a
// per-call parameter, matching how the rest of the harness pins its own
// protocol version once per build.
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
	// AdoptPortConflict: a process is listening at :7774 that does not
	// resolve to any install this client recognizes at all (foreign
	// process, or no `current` exists yet). Never killed.
	AdoptPortConflict AdoptAction = "port_conflict"
	// AdoptLegacy: a pre-lease engine (design F5/§3.7 R4). Adopt-only:
	// never terminate it, never spawn a second instance alongside it.
	AdoptLegacy AdoptAction = "legacy_pre_lease"
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
//  1. Checks lifecycle_protocol for the skew-window case (design F5):
//     a pre-lease engine is adopt-only, contract-compatibility
//     permitting.
//  2. Checks this client's own contract-major compatibility (design
//     §3.7 R7) before anything else — an incompatible engine strands
//     this client regardless of how well-verified the binary is.
//  3. Resolves health.ExePath: it must point inside THIS client's own
//     `current` version directory. A process running from anywhere
//     else is a port conflict, never an adoptee.
//  4. Re-verifies the ON-DISK artifact at that version against the
//     install.json record THIS client wrote when it last verified that
//     version (client-side, reusing core/bundle/integrity + core/trust
//     — no network call, no trusting the remote process).
//  5. Cross-checks the process's self-reported digest/version against
//     that independently-verified record. "Reports are cross-checks,
//     never trust roots" (WP12 brief): a mismatch here is exactly the
//     F2 planted-violation proof (adopt-refused-on-digest-mismatch).
//
// health is the already-fetched /health payload; EvaluateAdoption makes
// no network call itself so it is trivially unit-testable against
// hand-built HealthPayload fixtures, independent of the stub server
// (adopt_test.go) — the stub-backed integration path is covered by
// manager_test.go instead.
func EvaluateAdoption(layout Layout, health HealthPayload) (AdoptDecision, error) {
	if health.LifecycleProtocol == 0 {
		// Design F5/§3.7 R4: "a pre-lease engine found running is
		// adopt-only (never terminate, never double-spawn; use if
		// contract-compatible else surface 'update Kenaz to share the ML
		// engine')".
		if !contractCompatible(health, SupportedContractMajor) {
			return AdoptDecision{
				Action: AdoptContractUnsupported,
				Health: health,
				Detail: "legacy engine's contract is unsupported by this client — update Kenaz to share the ML engine",
			}, nil
		}
		return AdoptDecision{
			Action: AdoptLegacy,
			Reason: ReasonLegacyEngine,
			Health: health,
			Detail: "pre-lease engine (no lifecycle_protocol): adopt-only, never terminated, never double-spawned",
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
		// No `current` this client recognizes: whatever is on :7774 is
		// not an install this client can vouch for.
		return AdoptDecision{Action: AdoptPortConflict, Health: health, Detail: "no verified `current` install to compare against: " + err.Error()}, nil
	}
	if !pathIsWithin(health.ExePath, currentDir) {
		return AdoptDecision{Action: AdoptPortConflict, Health: health, Detail: "exe_path does not resolve inside this client's `current` install"}, nil
	}

	version := filepath.Base(currentDir)
	rec, ok, err := ReadInstallJSON(layout)
	if err != nil {
		return AdoptDecision{}, err
	}
	if !ok || !rec.Verified || rec.Version != version {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "no positively-verified install.json record for the running version"}, nil
	}

	// Re-verify the ON-DISK bytes right now — install.json records what
	// was true at install time; a file replaced out from under `current`
	// since then must not be trusted just because the record once said
	// so.
	if err := VerifyFileSHA256(health.ExePath, rec.EngineSHA256); err != nil {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "on-disk artifact no longer matches its verified install record: " + err.Error()}, nil
	}

	// Cross-check the process's self-report against the independently
	// verified record — a mismatch here means the running process is
	// lying about what it is, or is a different build entirely that
	// happens to share the port.
	if health.EngineSHA256 != "" && health.EngineSHA256 != rec.EngineSHA256 {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "reported engine_sha256 does not match the verified install record"}, nil
	}
	if health.SidecarVersion != "" && health.SidecarVersion != rec.Version {
		return AdoptDecision{Action: AdoptRefuseUnverified, Reason: ReasonDigestMismatch, Health: health, Detail: "reported sidecar_version does not match the verified install record"}, nil
	}

	return AdoptDecision{Action: AdoptAccept, Health: health, Detail: "verified install, verified process identity"}, nil
}

// contractCompatible reports whether health's advertised API
// contract-major is one this client build can speak to. Design §6.4:
// "the engine maintains N and N−1 per-kind feature contracts during
// transitions" — applied here at the coarser API-contract-major level
// that gates adoption at all; per-kind N/N−1 handling belongs to the
// dispatch layer (design §3.2), not to adoption.
//
// A HealthPayload with no "api" entry in ContractVersions at all is
// treated as compatible: the absence of contract information is not
// itself proof of incompatibility, and refusing every uninstrumented
// process would make AdoptContractUnsupported swallow AdoptPortConflict
// / AdoptLegacy's more specific verdicts.
func contractCompatible(health HealthPayload, supportedMajor int) bool {
	major, ok := health.ContractVersions["api"]
	if !ok {
		return true
	}
	return major == supportedMajor || major == supportedMajor-1
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
