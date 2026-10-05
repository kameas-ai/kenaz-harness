package fleet

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// TestPulledNodeToUnit_SkipsArtifactKind pins the ingress half of the
// artifact local-only rule (adversarial-review F3): a pulled team node
// claiming kind=artifact is skipped, never landed into the local units
// store (where it would reach the Library and media refcounts).
func TestPulledNodeToUnit_SkipsArtifactKind(t *testing.T) {
	m := NewUnitMapper("team-1")
	n := ContextPulledNode{
		ID: "evil-artifact", Kind: string(units.KindArtifact),
		Scope: "global", Classification: ClassTeamShared,
		Version: 1, Title: "not yours", Metadata: []byte(`{}`),
	}
	u, ok, err := m.PulledNodeToUnit(n)
	if err != nil {
		t.Fatalf("PulledNodeToUnit: %v", err)
	}
	if ok {
		t.Fatalf("pulled artifact-kind node was accepted: %+v", u)
	}
	// A doc node still maps (the skip is kind-scoped, not a regression).
	n.Kind = string(units.KindDoc)
	n.ID = "fine-doc"
	if _, ok, err := m.PulledNodeToUnit(n); err != nil || !ok {
		t.Fatalf("doc node no longer maps (ok=%v err=%v)", ok, err)
	}
}
