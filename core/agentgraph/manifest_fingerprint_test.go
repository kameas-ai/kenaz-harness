package agentgraph

import (
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
)

// TestComputeManifestFingerprint verifies basic properties of the
// fingerprint helper:
//
//  1. Non-empty hex string for a real manifest.
//  2. Stable (same result on second call — idempotency).
//  3. Insensitive to display-only field changes (description, display_name).
//  4. Sensitive to attr-type changes.
//  5. Sensitive to port additions.
//  6. Sensitive to budget changes.
//
// We drive the test against the shipped catalog so it exercises the
// real manifests.
func TestComputeManifestFingerprint(t *testing.T) {
	t.Parallel()

	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("model")
	if err != nil {
		t.Fatalf("Get(model): %v", err)
	}

	fp, err := ComputeManifestFingerprint(rm)
	if err != nil {
		t.Fatalf("ComputeManifestFingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("fingerprint must not be empty")
	}
	// SHA-256 hex is 64 chars.
	if len(fp) != 64 {
		t.Errorf("expected 64-char hex, got len=%d: %q", len(fp), fp)
	}
	// Must be lowercase hex.
	for _, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in fingerprint", c)
		}
	}

	// Stability: second call returns same value.
	fp2, err := ComputeManifestFingerprint(rm)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if fp != fp2 {
		t.Errorf("fingerprint not stable: %q vs %q", fp, fp2)
	}
}

// TestFingerprintInsensitiveToDisplayFields verifies that changing only
// description / display_name does not change the fingerprint.
func TestFingerprintInsensitiveToDisplayFields(t *testing.T) {
	t.Parallel()

	rm := makeTestRM("testk", "compute", nil, nil, "desc A", "DisplayA")
	fp1, err := ComputeManifestFingerprint(rm)
	if err != nil {
		t.Fatalf("fp1: %v", err)
	}

	rm2 := makeTestRM("testk", "compute", nil, nil, "desc B — changed", "DisplayB")
	fp2, err := ComputeManifestFingerprint(rm2)
	if err != nil {
		t.Fatalf("fp2: %v", err)
	}

	if fp1 != fp2 {
		t.Errorf("fingerprint should NOT change when only display fields change: %q vs %q", fp1, fp2)
	}
}

// TestFingerprintSensitiveToAttrType verifies that adding/changing an
// attr type changes the fingerprint.
func TestFingerprintSensitiveToAttrType(t *testing.T) {
	t.Parallel()

	attrs1 := map[string]nodes.AttrSpec{
		"temp": {Type: nodes.AttrTypeFloat},
	}
	attrs2 := map[string]nodes.AttrSpec{
		"temp": {Type: nodes.AttrTypeInt},
	}

	fp1, err := ComputeManifestFingerprint(makeTestRM("k", "none", attrs1, nil, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	fp2, err := ComputeManifestFingerprint(makeTestRM("k", "none", attrs2, nil, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if fp1 == fp2 {
		t.Error("fingerprint must differ when attr type changes")
	}
}

// TestFingerprintSensitiveToPorts verifies that adding a port changes the fingerprint.
func TestFingerprintSensitiveToPorts(t *testing.T) {
	t.Parallel()

	ports1 := []nodes.PortSpec{{Name: "in", Type: "any", Required: true}}
	ports2 := []nodes.PortSpec{{Name: "in", Type: "any", Required: true}, {Name: "ctx", Type: "string"}}

	fp1, err := ComputeManifestFingerprint(makeTestRM("k", "none", nil, ports1, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	fp2, err := ComputeManifestFingerprint(makeTestRM("k", "none", nil, ports2, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if fp1 == fp2 {
		t.Error("fingerprint must differ when input ports change")
	}
}

// TestFingerprintSensitiveToBudget verifies budget changes affect the fingerprint.
func TestFingerprintSensitiveToBudget(t *testing.T) {
	t.Parallel()

	fp1, err := ComputeManifestFingerprint(makeTestRM("k", "llm", nil, nil, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	fp2, err := ComputeManifestFingerprint(makeTestRM("k", "tool", nil, nil, "", ""))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if fp1 == fp2 {
		t.Error("fingerprint must differ when budget changes")
	}
}

// TestFingerprintNilInput verifies that nil input returns an error.
func TestFingerprintNilInput(t *testing.T) {
	t.Parallel()
	_, err := ComputeManifestFingerprint(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}

// TestFingerprintAllShippedKinds runs the helper against every callable
// kind in the shipped catalog and verifies all fingerprints are unique
// (no two kinds collide) and well-formed.
func TestFingerprintAllShippedKinds(t *testing.T) {
	t.Parallel()

	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	seen := map[string]string{} // fingerprint → kind ID (collision check)
	for _, rm := range cat.Kinds() {
		fp, err := ComputeManifestFingerprint(rm)
		if err != nil {
			t.Errorf("kind %q: %v", rm.Manifest.ID, err)
			continue
		}
		if len(fp) != 64 {
			t.Errorf("kind %q: bad fp length %d", rm.Manifest.ID, len(fp))
		}
		if !strings.ContainsAny(fp, "0123456789abcdef") {
			t.Errorf("kind %q: non-hex fp %q", rm.Manifest.ID, fp)
		}
		if prev, collision := seen[fp]; collision {
			t.Errorf("fingerprint collision: kind %q and %q share fp %q", rm.Manifest.ID, prev, fp)
		}
		seen[fp] = rm.Manifest.ID
	}
}

// makeTestRM constructs a minimal ResolvedManifest for table-driven tests.
func makeTestRM(
	id string,
	budget string,
	attrs map[string]nodes.AttrSpec,
	inputPorts []nodes.PortSpec,
	description string,
	displayName string,
) *nodes.ResolvedManifest {
	t := true
	return &nodes.ResolvedManifest{
		Manifest: nodes.Manifest{
			ID:          id,
			Callable:    &t,
			Budget:      nodes.BudgetSignature(budget),
			Attrs:       attrs,
			Ports:       nodes.PortSet{Inputs: inputPorts},
			Description: description,
			DisplayName: displayName,
		},
		Chain: []string{id},
	}
}
