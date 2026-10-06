package mlsidecar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
)

// setupVerifiedVersion writes a fake, already-verified engine install
// (mirroring what install.go's Install would have produced) under l:
// versions/<version>/kameas-ml/kameas-ml with the given content, current
// pointed at it, and a matching install.json record with Verified=true.
func setupVerifiedVersion(t *testing.T, l Layout, version string, content []byte) (exePath, sha string) {
	t.Helper()
	dir := filepath.Join(l.VersionDir(version), "kameas-ml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exePath = filepath.Join(dir, EngineExecutableName(""))
	if err := os.WriteFile(exePath, content, 0o755); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	f, err := os.Open(exePath)
	if err != nil {
		t.Fatalf("open exe: %v", err)
	}
	sha, _, err = integrity.HashSHA256(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("hash exe: %v", err)
	}
	if err := l.SetCurrent(version); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	tree, err := TreeDigest(l.OnedirPath(version))
	if err != nil {
		t.Fatalf("tree digest: %v", err)
	}
	if err := WriteInstallJSON(l, InstallRecord{Version: version, EngineSHA256: sha, Verified: true, Source: "test",
		TreeSHA256: tree, Provenance: ProvenanceChannelManifest, InstalledBy: "harness"}); err != nil {
		t.Fatalf("WriteInstallJSON: %v", err)
	}
	return exePath, sha
}

// setupSeededVersion mirrors setupVerifiedVersion but writes the record
// the OTHER client (Kenaz) writes: Verified=false, the given provenance
// — the A5(4) cross-client shape. Acceptance must ride provenance +
// tree match, never the Verified bit.
func setupSeededVersion(t *testing.T, l Layout, version string, content []byte, provenance string) (exePath, sha string) {
	t.Helper()
	dir := filepath.Join(l.VersionDir(version), "kameas-ml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exePath = filepath.Join(dir, EngineExecutableName(""))
	if err := os.WriteFile(exePath, content, 0o755); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	f, err := os.Open(exePath)
	if err != nil {
		t.Fatalf("open exe: %v", err)
	}
	sha, _, err = integrity.HashSHA256(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("hash exe: %v", err)
	}
	if err := l.SetCurrent(version); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	tree, err := TreeDigest(l.OnedirPath(version))
	if err != nil {
		t.Fatalf("tree digest: %v", err)
	}
	if err := WriteInstallJSON(l, InstallRecord{Version: version, EngineSHA256: sha, Verified: false, Source: "kenaz-seed",
		TreeSHA256: tree, Provenance: provenance, InstalledBy: "kenaz"}); err != nil {
		t.Fatalf("WriteInstallJSON: %v", err)
	}
	return exePath, sha
}

// TestEvaluateAdoption_AdoptVerified is the "adopt-verified" proof: a
// process whose exe_path resolves under `current`, whose on-disk bytes
// hash to the client's own verified install record, and whose
// self-report matches that record exactly, is adopted.
func TestEvaluateAdoption_AdoptVerified(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))

	health := HealthPayload{
		Product:           "kameas-ml",
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		EngineSHA256:      sha,
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptAccept {
		t.Fatalf("Action = %q, want %q (%s)", decision.Action, AdoptAccept, decision.Detail)
	}
}

// TestEvaluateAdoption_RefusesOnDigestMismatch is design F2's named
// planted-violation proof: a process that self-reports a DIFFERENT
// engine_sha256 than this client's own verified install record is
// refused, never adopted — "reports are cross-checks, never trust
// roots".
func TestEvaluateAdoption_RefusesOnDigestMismatch(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))
	_ = sha

	health := HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		EngineSHA256:      "sha256:" + "deadbeef00000000000000000000000000000000000000000000000000",
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptRefuseUnverified {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptRefuseUnverified)
	}
	if decision.Reason != ReasonDigestMismatch {
		t.Errorf("Reason = %q, want %q", decision.Reason, ReasonDigestMismatch)
	}
}

// TestEvaluateAdoption_TamperedOnDiskArtifact_NeverAdopted is the
// tampered-artifact-never-executes proof from the adoption side: the
// install.json record says one digest, but the file ON DISK has since
// been changed. Even with a self-report that matches the (now stale)
// record, EvaluateAdoption re-hashes the REAL bytes and refuses.
func TestEvaluateAdoption_TamperedOnDiskArtifact_NeverAdopted(t *testing.T) {
	l := NewLayout(t.TempDir())
	exePath, sha := setupVerifiedVersion(t, l, "1.0.0", []byte("original engine bytes"))

	// Tamper with the on-disk artifact after install; the self-report
	// still (naively) claims the ORIGINAL digest, as a compromised or
	// buggy process might.
	if err := os.WriteFile(exePath, []byte("REPLACED MALICIOUS BYTES"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	health := HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		EngineSHA256:      sha, // stale/lying self-report
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptRefuseUnverified {
		t.Fatalf("Action = %q, want %q — a tampered on-disk artifact must never be adopted", decision.Action, AdoptRefuseUnverified)
	}
}

// TestEvaluateAdoption_PortConflict_NoCurrentInstall covers the case
// where something answers on the engine port but this client has never installed
// anything at all — never adopted, never treated as "ours".
func TestEvaluateAdoption_PortConflict_NoCurrentInstall(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		SidecarVersion:    "9.9.9",
		ExePath:           "/some/foreign/path/kameas-ml",
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptPortConflict {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptPortConflict)
	}
}

// TestEvaluateAdoption_PortConflict_ExePathOutsideCurrent covers a
// process that answers health but runs from somewhere other than this
// client's own verified `current` — a foreign kenaz-ml, a dev build, or
// another user's install.
func TestEvaluateAdoption_PortConflict_ExePathOutsideCurrent(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))

	health := HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           "/completely/different/place/kameas-ml",
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptPortConflict {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptPortConflict)
	}
}

// TestEvaluateAdoption_RefusesWhenNeverVerified covers a `current` that
// resolves to a real directory, but this client has no positively
// verified install.json record for it at all (e.g. record missing, or
// Verified=false).
func TestEvaluateAdoption_RefusesWhenNeverVerified(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := l.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	dir := filepath.Join(l.VersionDir("1.0.0"), "kameas-ml")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	exePath := filepath.Join(dir, EngineExecutableName(""))
	if err := os.WriteFile(exePath, []byte("bytes"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := l.SetCurrent("1.0.0"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	// No install.json written at all.

	health := HealthPayload{
		SidecarVersion:    "1.0.0",
		ExePath:           exePath,
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptRefuseUnverified {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptRefuseUnverified)
	}
}

// TestEvaluateAdoption_SkewWindow_LegacyEngine covers design F5/§3.7 R4
// AS AMENDED by the 2026-09-29 security-review ruling: a pre-lease
// engine (LifecycleProtocol==0) is ALWAYS AdoptLegacyUnverified —
// unverifiable against this client's install record, hence never usable
// for recommendations — even with an otherwise-compatible contract-major
// and even with NO on-disk install for this client at all. The original
// F5 text ("use if contract-compatible") contradicted design R2's
// universal-verification rule; R2 wins (see StateLegacyUnverified's doc
// comment for the full rationale and the exploit this closes).
func TestEvaluateAdoption_SkewWindow_LegacyEngine(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		SidecarVersion:    "0.9.0",
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 0,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptLegacyUnverified {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptLegacyUnverified)
	}
	if decision.Reason != ReasonLegacyEngine {
		t.Errorf("Reason = %q, want %q", decision.Reason, ReasonLegacyEngine)
	}
	if !strings.Contains(decision.Detail, "update Kenaz to share the ML engine") {
		t.Errorf("Detail = %q, want it to surface the 'update Kenaz' message unconditionally", decision.Detail)
	}
}

// TestEvaluateAdoption_LegacyEngine_UnverifiedRegardlessOfContract proves
// the amendment's "regardless of contract-major" half directly: a legacy
// engine advertising a contract this client CANNOT speak at all still
// resolves to the exact same AdoptLegacyUnverified verdict as a
// contract-compatible one — contract compatibility is irrelevant once an
// engine is unconditionally unverifiable. (Superseded test name:
// TestEvaluateAdoption_SkewWindow_LegacyEngineIncompatible no longer
// applies — AdoptContractUnsupported is not reachable from the legacy
// branch post-amendment.)
func TestEvaluateAdoption_LegacyEngine_UnverifiedRegardlessOfContract(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: 0,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptLegacyUnverified {
		t.Fatalf("Action = %q, want %q (contract-major must not change a legacy engine's verdict)", decision.Action, AdoptLegacyUnverified)
	}
}

// TestEvaluateAdoption_BareEmptyHealth_NeverAdoptedAsHealthy is the
// security-review's planted-style regression pin: a process answering
// /health with a bare `{}` (every field at its JSON zero value —
// LifecycleProtocol==0, no "api" entry in ContractVersions, empty
// ExePath) must NEVER resolve to AdoptAccept. Before the 2026-09-29
// amendment, contractCompatible's "no api entry => compatible" leniency
// combined with the legacy branch's "use if contract-compatible" rule to
// adopt exactly this shape as a healthy sidecar.
func TestEvaluateAdoption_BareEmptyHealth_NeverAdoptedAsHealthy(t *testing.T) {
	l := NewLayout(t.TempDir())
	// A layout with a real, verified install present makes this the
	// STRONGEST version of the pin: even when this client DOES have
	// something it could legitimately adopt, a bare-{} report must still
	// never be treated as that verified install (LifecycleProtocol==0
	// short-circuits before ExePath/current is even consulted).
	setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))

	decision, err := EvaluateAdoption(l, HealthPayload{}, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action == AdoptAccept {
		t.Fatal("a bare {} /health response must never be adopted as a verified, healthy install")
	}
	if decision.Action != AdoptLegacyUnverified {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptLegacyUnverified)
	}
}

// TestEvaluateAdoption_ContractUnsupported_LeaseAwareEngine covers
// design §3.7 R7: even a fully lease-aware engine strands this client if
// its contract-major is genuinely unsupported.
func TestEvaluateAdoption_ContractUnsupported_LeaseAwareEngine(t *testing.T) {
	l := NewLayout(t.TempDir())
	setupVerifiedVersion(t, l, "1.0.0", []byte("engine binary bytes"))
	health := HealthPayload{
		SidecarVersion:    "1.0.0",
		ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
		LifecycleProtocol: SupportedContractMajor + 50,
	}
	decision, err := EvaluateAdoption(l, health, nil)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptContractUnsupported {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptContractUnsupported)
	}
}

// TestContractCompatible_NMinus1Supported pins the "N and N-1" transition
// window design §6.4 describes, applied to the lifecycle-protocol major
// (the only protocol number the engine publishes on /health).
func TestContractCompatible_NMinus1Supported(t *testing.T) {
	cases := []struct {
		protocol int
		want     bool
	}{
		{SupportedContractMajor, true},
		{SupportedContractMajor + 1, false},
		{SupportedContractMajor + 50, false},
		// 0 is the legacy / cloud marker, never "N-1": EvaluateAdoption's
		// legacy branch owns it, and contractCompatible must not bless it.
		{0, false},
		{-1, false},
	}
	for _, tc := range cases {
		got := contractCompatible(HealthPayload{LifecycleProtocol: tc.protocol}, SupportedContractMajor)
		if got != tc.want {
			t.Errorf("contractCompatible(protocol=%d) = %v, want %v", tc.protocol, got, tc.want)
		}
	}
}
