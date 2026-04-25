package snapshot

import (
	"testing"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
	"github.com/sigil-tech/kaneaz-harness/core/context/merge"
	"github.com/sigil-tech/kaneaz-harness/core/context/verify"
)

func mkResult() *merge.Result {
	ref := pack.PackRef{Name: "p", Version: "1", Layer: pack.LayerOrg, ContentHash: "sha256:p"}
	e := pack.ContextEntry{
		Name: "term", Kind: pack.KindGlossary, Body: []byte("body"),
		ContentHash: "sha256:e", SourceLayer: pack.LayerOrg, SourcePack: ref,
	}
	return &merge.Result{
		Entries: []merge.ResolvedEntry{{ContextEntry: e, Winner: pack.LayerOrg}},
		Layers:  []merge.LayerActivation{{Layer: pack.LayerOrg, Pack: ref, Entries: 1}},
	}
}

func TestBuild_DeterministicID(t *testing.T) {
	r := mkResult()
	prov := []verify.ProvenanceRecord{{
		Pack:     r.Entries[0].SourcePack,
		AnchorID: "anchor", Algorithm: "sigstore-bundle",
	}}
	t1 := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	s1, err := Build(r, prov, ModeFresh, "wf", "agent", t1)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	s2, err := Build(r, prov, ModeFresh, "wf", "agent", t2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// IDs must be equal regardless of generated_at.
	if s1.ID != s2.ID {
		t.Errorf("snapshot ID is not generated_at-stable: %s vs %s", s1.ID, s2.ID)
	}
	// Mode change must change the ID.
	s3, _ := Build(r, prov, ModeCacheOnly, "wf", "agent", t1)
	if s3.ID == s1.ID {
		t.Errorf("Mode must affect ID")
	}
}

func TestBuild_AttachesProvenance(t *testing.T) {
	r := mkResult()
	prov := []verify.ProvenanceRecord{{
		Pack:     r.Entries[0].SourcePack,
		AnchorID: "anchor", Algorithm: "sigstore-bundle",
	}}
	s, err := Build(r, prov, ModeFresh, "", "", time.Now())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(s.Layers) != 1 || s.Layers[0].Provenance == nil {
		t.Fatalf("provenance not attached")
	}
	if s.Layers[0].Provenance.AnchorID != "anchor" {
		t.Errorf("provenance lost; got %q", s.Layers[0].Provenance.AnchorID)
	}
}

func TestBuild_NilResult(t *testing.T) {
	if _, err := Build(nil, nil, ModeFresh, "", "", time.Now()); err == nil {
		t.Errorf("expected error for nil result")
	}
}
