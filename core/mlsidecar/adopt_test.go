package mlsidecar

import (
	"os"
	"path/filepath"
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
	if err := WriteInstallJSON(l, InstallRecord{Version: version, EngineSHA256: sha, Verified: true, Source: "test"}); err != nil {
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptRefuseUnverified {
		t.Fatalf("Action = %q, want %q — a tampered on-disk artifact must never be adopted", decision.Action, AdoptRefuseUnverified)
	}
}

// TestEvaluateAdoption_PortConflict_NoCurrentInstall covers the case
// where something answers :7774 but this client has never installed
// anything at all — never adopted, never treated as "ours".
func TestEvaluateAdoption_PortConflict_NoCurrentInstall(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		SidecarVersion:    "9.9.9",
		ExePath:           "/some/foreign/path/kameas-ml",
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptRefuseUnverified {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptRefuseUnverified)
	}
}

// TestEvaluateAdoption_SkewWindow_LegacyEngine covers design F5/§3.7 R4:
// a pre-lease engine (LifecycleProtocol==0) with a compatible contract
// is adopt-only.
func TestEvaluateAdoption_SkewWindow_LegacyEngine(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		SidecarVersion:    "0.9.0",
		ContractVersions:  map[string]int{"api": SupportedContractMajor},
		LifecycleProtocol: 0,
	}
	decision, err := EvaluateAdoption(l, health)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptLegacy {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptLegacy)
	}
	if decision.Reason != ReasonLegacyEngine {
		t.Errorf("Reason = %q, want %q", decision.Reason, ReasonLegacyEngine)
	}
}

// TestEvaluateAdoption_SkewWindow_LegacyEngineIncompatible covers the
// "else surface 'update Kenaz to share the ML engine'" half of F5: a
// pre-lease engine whose contract this client cannot speak at all.
func TestEvaluateAdoption_SkewWindow_LegacyEngineIncompatible(t *testing.T) {
	l := NewLayout(t.TempDir())
	health := HealthPayload{
		ContractVersions:  map[string]int{"api": SupportedContractMajor + 50},
		LifecycleProtocol: 0,
	}
	decision, err := EvaluateAdoption(l, health)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptContractUnsupported {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptContractUnsupported)
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
		ContractVersions:  map[string]int{"api": SupportedContractMajor + 50},
		LifecycleProtocol: 1,
	}
	decision, err := EvaluateAdoption(l, health)
	if err != nil {
		t.Fatalf("EvaluateAdoption: %v", err)
	}
	if decision.Action != AdoptContractUnsupported {
		t.Fatalf("Action = %q, want %q", decision.Action, AdoptContractUnsupported)
	}
}

// TestContractCompatible_NMinus1Supported pins the "N and N-1" transition
// window design §6.4 describes.
func TestContractCompatible_NMinus1Supported(t *testing.T) {
	cases := []struct {
		major int
		want  bool
	}{
		{SupportedContractMajor, true},
		{SupportedContractMajor - 1, true},
		{SupportedContractMajor + 1, false},
		{SupportedContractMajor - 2, false},
	}
	for _, tc := range cases {
		got := contractCompatible(HealthPayload{ContractVersions: map[string]int{"api": tc.major}}, SupportedContractMajor)
		if got != tc.want {
			t.Errorf("contractCompatible(major=%d) = %v, want %v", tc.major, got, tc.want)
		}
	}
	// No "api" entry at all: treated as compatible (see contractCompatible's doc comment).
	if !contractCompatible(HealthPayload{}, SupportedContractMajor) {
		t.Error("contractCompatible with no api entry should default to true")
	}
}
