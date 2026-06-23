package agentgraph

import (
	"testing"
)

// TestCheckManifestDrift_NoProvenance verifies that a graph with no
// provenance rows produces no warnings.
func TestCheckManifestDrift_NoProvenance(t *testing.T) {
	t.Parallel()
	g := Graph{ID: "g1", Nodes: []Node{{ID: "n1", Kind: NodeKindModel}}}
	warnings := CheckManifestDrift(g, nil)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings with no provenance, got %d", len(warnings))
	}
}

// TestCheckManifestDrift_NoMismatch verifies that matching fingerprints
// produce no warnings.
func TestCheckManifestDrift_NoMismatch(t *testing.T) {
	t.Parallel()

	// Get the live fingerprint for "model".
	rm, ok := ResolvedManifests[NodeKindModel]
	if !ok {
		t.Skip("model manifest not in ResolvedManifests")
	}
	liveFP, err := ComputeManifestFingerprint(rm)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}

	g := Graph{ID: "g1", Nodes: []Node{{ID: "n1", Kind: NodeKindModel}}}
	prov := []NodeProvenance{{
		GraphID:                 "g1",
		NodeID:                  "n1",
		Kind:                    "model",
		ManifestVersionAtAuthor: "1.0.0",
		FingerprintAtAuthor:     liveFP,
	}}

	warnings := CheckManifestDrift(g, prov)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings when fingerprint matches, got %d: %+v", len(warnings), warnings)
	}
}

// TestCheckManifestDrift_Mismatch verifies that a stale fingerprint
// produces exactly one warning with the right fields.
func TestCheckManifestDrift_Mismatch(t *testing.T) {
	t.Parallel()

	g := Graph{ID: "g1", Nodes: []Node{{ID: "n1", Kind: NodeKindModel}}}
	prov := []NodeProvenance{{
		GraphID:                 "g1",
		NodeID:                  "n1",
		Kind:                    "model",
		ManifestVersionAtAuthor: "0.9.0",
		FingerprintAtAuthor:     "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}}

	warnings := CheckManifestDrift(g, prov)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	w := warnings[0]
	if w.NodeID != "n1" {
		t.Errorf("NodeID = %q, want n1", w.NodeID)
	}
	if w.Kind != "model" {
		t.Errorf("Kind = %q, want model", w.Kind)
	}
	if w.AuthoredFingerprint != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("AuthoredFingerprint unexpected: %q", w.AuthoredFingerprint)
	}
	if w.LiveFingerprint == "" {
		t.Error("LiveFingerprint must not be empty")
	}
	if w.LiveFingerprint == w.AuthoredFingerprint {
		t.Error("LiveFingerprint should differ from AuthoredFingerprint")
	}
}

// TestCheckManifestDrift_EmptyFingerprintSkipped verifies that a
// provenance row with an empty fingerprint (pre-migration node) is skipped.
func TestCheckManifestDrift_EmptyFingerprintSkipped(t *testing.T) {
	t.Parallel()

	g := Graph{ID: "g1", Nodes: []Node{{ID: "n1", Kind: NodeKindModel}}}
	prov := []NodeProvenance{{
		GraphID:             "g1",
		NodeID:              "n1",
		Kind:                "model",
		FingerprintAtAuthor: "", // empty = unknown provenance
	}}

	warnings := CheckManifestDrift(g, prov)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for empty fingerprint, got %d", len(warnings))
	}
}

// TestCheckManifestDrift_UnknownNodeSkipped verifies that nodes with no
// provenance row are silently skipped.
func TestCheckManifestDrift_UnknownNodeSkipped(t *testing.T) {
	t.Parallel()

	g := Graph{ID: "g1", Nodes: []Node{
		{ID: "n1", Kind: NodeKindModel},
		{ID: "n2", Kind: NodeKindDecision},
	}}
	// Only n2 has provenance with a stale fp.
	prov := []NodeProvenance{{
		GraphID:                 "g1",
		NodeID:                  "n2",
		Kind:                    "decision",
		ManifestVersionAtAuthor: "0.0.1",
		FingerprintAtAuthor:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}

	warnings := CheckManifestDrift(g, prov)
	// n1 has no row → skipped. n2 has stale fp → warning.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].NodeID != "n2" {
		t.Errorf("NodeID = %q, want n2", warnings[0].NodeID)
	}
}

// TestCheckManifestDrift_GrandfatheredRename verifies that a node authored
// with the pre-rename (kaneaz->kenaz) Sleep fingerprint is NOT reported as
// drift — the tool-name string change was behavior-neutral — while a
// genuinely different stale fingerprint for the same kind still warns.
func TestCheckManifestDrift_GrandfatheredRename(t *testing.T) {
	t.Parallel()

	const oldSleepFP = "5d12fa131e6f79f24f0c94aab65f5b63b333c0d71850194c4bd4d0052248cd0b"

	g := Graph{ID: "g1", Nodes: []Node{{ID: "n1", Kind: NodeKindSleep}}}

	// Authored with the grandfathered pre-rename fingerprint → no drift.
	provGrandfathered := []NodeProvenance{{
		GraphID:                 "g1",
		NodeID:                  "n1",
		Kind:                    string(NodeKindSleep),
		ManifestVersionAtAuthor: "1.0.0",
		FingerprintAtAuthor:     oldSleepFP,
	}}
	if w := CheckManifestDrift(g, provGrandfathered); len(w) != 0 {
		t.Errorf("expected 0 warnings for grandfathered pre-rename fingerprint, got %d: %+v", len(w), w)
	}

	// A different stale fingerprint for the same kind must still warn —
	// the grandfather is narrow, not a blanket suppression.
	provStale := []NodeProvenance{{
		GraphID:                 "g1",
		NodeID:                  "n1",
		Kind:                    string(NodeKindSleep),
		ManifestVersionAtAuthor: "1.0.0",
		FingerprintAtAuthor:     "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
	}}
	if w := CheckManifestDrift(g, provStale); len(w) != 1 {
		t.Fatalf("expected 1 warning for a non-grandfathered stale fingerprint, got %d", len(w))
	}
}

// TestAllGeneratedManifestVersions checks that allGeneratedManifestVersions
// is populated with all callable kinds and non-empty version strings.
func TestAllGeneratedManifestVersions(t *testing.T) {
	t.Parallel()

	m := getLiveManifestVersionMap()
	kinds := AllNodeKinds()
	if len(m) != len(kinds) {
		t.Errorf("allGeneratedManifestVersions len=%d, want %d (AllNodeKinds)", len(m), len(kinds))
	}
	for _, k := range kinds {
		v, ok := m[string(k)]
		if !ok {
			t.Errorf("kind %q missing from allGeneratedManifestVersions", k)
			continue
		}
		if v == "" {
			t.Errorf("kind %q has empty version in allGeneratedManifestVersions", k)
		}
	}
}
