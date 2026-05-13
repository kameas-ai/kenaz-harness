package agentgraph

import (
	"errors"
	"testing"
)

func TestEnforceManifestDriftPolicy_WarnMode(t *testing.T) {
	t.Parallel()
	warnings := []ManifestDriftWarning{{
		GraphID: "g1", NodeID: "n1", Kind: "model",
		AuthoredFingerprint: "old", LiveFingerprint: "new",
	}}
	// Both "" and "warn" should be no-ops.
	for _, mode := range []string{"", ManifestDriftModeWarn} {
		if err := EnforceManifestDriftPolicy(mode, warnings); err != nil {
			t.Errorf("mode=%q: expected nil error, got %v", mode, err)
		}
	}
}

func TestEnforceManifestDriftPolicy_BlockModeNoWarnings(t *testing.T) {
	t.Parallel()
	if err := EnforceManifestDriftPolicy(ManifestDriftModeBlock, nil); err != nil {
		t.Errorf("block with no warnings: expected nil, got %v", err)
	}
}

func TestEnforceManifestDriftPolicy_BlockModeWithWarnings(t *testing.T) {
	t.Parallel()
	warnings := []ManifestDriftWarning{{
		GraphID: "g1", NodeID: "n1", Kind: "model",
		ManifestVersionAtAuthor: "1.0.0", LiveManifestVersion: "1.1.0",
		AuthoredFingerprint: "old", LiveFingerprint: "new",
	}}
	err := EnforceManifestDriftPolicy(ManifestDriftModeBlock, warnings)
	if err == nil {
		t.Fatal("expected error in block mode with warnings")
	}
	if !errors.Is(err, ErrManifestDrift) {
		t.Errorf("error should wrap ErrManifestDrift; got: %v", err)
	}
}

func TestEnforceManifestDriftPolicy_UnknownMode(t *testing.T) {
	t.Parallel()
	err := EnforceManifestDriftPolicy("strict_and_loud", nil)
	if err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestEnforceManifestDriftPolicy_BlockModeManyWarnings(t *testing.T) {
	t.Parallel()
	// Build 7 warnings — more than the 5-summary cap.
	warnings := make([]ManifestDriftWarning, 7)
	for i := range warnings {
		warnings[i] = ManifestDriftWarning{
			GraphID: "g1", NodeID: "n" + string(rune('0'+i)), Kind: "model",
			AuthoredFingerprint: "old", LiveFingerprint: "new",
		}
	}
	err := EnforceManifestDriftPolicy(ManifestDriftModeBlock, warnings)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrManifestDrift) {
		t.Errorf("not ErrManifestDrift: %v", err)
	}
}
