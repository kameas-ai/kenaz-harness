package merge

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
	"github.com/sigil-tech/kaneaz-harness/core/context/policy"
)

// fixturePack builds a tiny in-memory pack with the given entries. Real
// packs come from the parser; this helper exercises the merger in
// isolation with deterministic content hashes.
func fixturePack(name string, layer pack.Layer, entries ...pack.ContextEntry) *pack.ContextPack {
	ref := pack.PackRef{
		Name:        name,
		Version:     "1.0.0",
		Layer:       layer,
		ContentHash: "sha256:" + name,
	}
	out := make([]pack.ContextEntry, 0, len(entries))
	for _, e := range entries {
		e.SourceLayer = layer
		e.SourcePack = ref
		if e.ContentHash == "" {
			e.ContentHash = "sha256:" + name + "/" + e.Name
		}
		if e.SizeBytes == 0 {
			e.SizeBytes = int64(len(e.Body)) + 32 // simulate frontmatter overhead
		}
		out = append(out, e)
	}
	return &pack.ContextPack{Ref: ref, Entries: out}
}

func entry(name string, body string) pack.ContextEntry {
	return pack.ContextEntry{
		Name: name, Kind: pack.KindGlossary,
		Body: []byte(body),
	}
}

func TestMerge_OrgOnly(t *testing.T) {
	org := fixturePack("org", pack.LayerOrg,
		entry("entropy", "thermo"),
		entry("tco", "total cost of ownership"),
	)
	res, err := Merge(Request{
		Layers: []LayerInput{{Layer: pack.LayerOrg, Pack: org}},
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got := len(res.Entries); got != 2 {
		t.Fatalf("Entries len = %d, want 2", got)
	}
	if res.Entries[0].Name != "entropy" || res.Entries[1].Name != "tco" {
		t.Errorf("entries not name-sorted: %+v", res.Entries)
	}
	if len(res.Overrides) != 0 {
		t.Errorf("expected no overrides, got %v", res.Overrides)
	}
	if len(res.Layers) != 1 || res.Layers[0].Layer != pack.LayerOrg {
		t.Errorf("LayerActivation = %v", res.Layers)
	}
}

func TestMerge_TeamOverridesOrg(t *testing.T) {
	org := fixturePack("org", pack.LayerOrg,
		entry("entropy", "org-meaning"),
		entry("tco", "org-tco"),
	)
	team := fixturePack("team", pack.LayerTeam,
		entry("entropy", "team-meaning"),
	)
	res, err := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerTeam, Pack: team},
		},
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, ok := findEntry(res, "entropy")
	if !ok {
		t.Fatalf("entropy missing from result")
	}
	if got.Winner != pack.LayerTeam || string(got.Body) != "team-meaning" {
		t.Errorf("entropy winner=%q body=%q want team team-meaning", got.Winner, got.Body)
	}
	// US2-2: org-only entry still surfaces.
	if _, ok := findEntry(res, "tco"); !ok {
		t.Errorf("tco (org-only) missing from result")
	}
	if len(res.Overrides) != 1 || res.Overrides[0].EntryName != "entropy" {
		t.Fatalf("Overrides = %v", res.Overrides)
	}
	if res.Overrides[0].Winner != pack.LayerTeam || res.Overrides[0].Loser != pack.LayerOrg {
		t.Errorf("override direction wrong: %+v", res.Overrides[0])
	}
}

func TestMerge_PersonalBeatsTeamBeatsOrg(t *testing.T) {
	org := fixturePack("org", pack.LayerOrg, entry("term", "org"))
	team := fixturePack("team", pack.LayerTeam, entry("term", "team"))
	personal := fixturePack("personal", pack.LayerPersonal, entry("term", "personal"))
	res, err := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerTeam, Pack: team},
			{Layer: pack.LayerPersonal, Pack: personal},
		},
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, _ := findEntry(res, "term")
	if got.Winner != pack.LayerPersonal || string(got.Body) != "personal" {
		t.Errorf("got winner=%q body=%q, want personal personal", got.Winner, got.Body)
	}
	if len(res.Overrides) != 2 {
		t.Errorf("expected 2 override records (team and org both beaten); got %d", len(res.Overrides))
	}
}

func TestMerge_Deterministic(t *testing.T) {
	mk := func() *Result {
		org := fixturePack("org", pack.LayerOrg, entry("a", "x"), entry("b", "y"))
		team := fixturePack("team", pack.LayerTeam, entry("b", "y2"), entry("c", "z"))
		r, err := Merge(Request{
			Layers: []LayerInput{
				{Layer: pack.LayerOrg, Pack: org},
				{Layer: pack.LayerTeam, Pack: team},
			},
			Policy: policy.Default(),
		})
		if err != nil {
			t.Fatalf("Merge: %v", err)
		}
		return r
	}
	r1, r2 := mk(), mk()
	if !reflect.DeepEqual(r1, r2) {
		t.Fatalf("merge non-deterministic")
	}
}

func TestMerge_InputOrderInvariant(t *testing.T) {
	// Same packs, two different input orders must yield identical results.
	org := fixturePack("org", pack.LayerOrg, entry("term", "org"))
	team := fixturePack("team", pack.LayerTeam, entry("term", "team"))
	a, _ := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerTeam, Pack: team},
		},
		Policy: policy.Default(),
	})
	b, _ := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerTeam, Pack: team},
			{Layer: pack.LayerOrg, Pack: org},
		},
		Policy: policy.Default(),
	})
	if !reflect.DeepEqual(a.Entries, b.Entries) || !reflect.DeepEqual(a.Overrides, b.Overrides) {
		t.Fatalf("input-order leaked through; a=%+v b=%+v", a, b)
	}
}

func TestMerge_WorkflowScopeFiltersBeforeOverride(t *testing.T) {
	// Entry restricted to a different workflow must not apply, and so
	// must not override an org definition either.
	org := fixturePack("org", pack.LayerOrg, entry("term", "org-default"))
	scoped := pack.ContextEntry{
		Name: "term", Kind: pack.KindGlossary, Body: []byte("only-quarterly"),
		Scope: pack.Scope{Workflows: []string{"quarterly-rollup"}},
	}
	team := fixturePack("team", pack.LayerTeam, scoped)

	// Active workflow: "weekly". The team's scoped entry should NOT win.
	res, err := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerTeam, Pack: team},
		},
		Workflow: "weekly",
		Policy:   policy.Default(),
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, _ := findEntry(res, "term")
	if got.Winner != pack.LayerOrg {
		t.Errorf("scope filter not applied before override; winner=%q", got.Winner)
	}
	if len(res.Overrides) != 0 {
		t.Errorf("expected no override (scoped entry filtered out); got %v", res.Overrides)
	}
}

func TestMerge_FailOnConflictMode(t *testing.T) {
	org := fixturePack("org", pack.LayerOrg, entry("term", "org"))
	personal := fixturePack("personal", pack.LayerPersonal, entry("term", "personal"))
	pol := policy.Default()
	pol.Conflict = policy.ConflictFail

	_, err := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerPersonal, Pack: personal},
		},
		Policy: pol,
	})
	var ec *policy.ErrConflict
	if !errors.As(err, &ec) {
		t.Fatalf("expected *policy.ErrConflict, got %v", err)
	}
	if len(ec.Conflicts) != 1 || ec.Conflicts[0].EntryName != "term" {
		t.Errorf("conflict reports = %+v", ec.Conflicts)
	}
}

func TestMerge_OverrideByPrecedenceModeIsDefault(t *testing.T) {
	org := fixturePack("org", pack.LayerOrg, entry("term", "org"))
	personal := fixturePack("personal", pack.LayerPersonal, entry("term", "personal"))
	res, err := Merge(Request{
		Layers: []LayerInput{
			{Layer: pack.LayerOrg, Pack: org},
			{Layer: pack.LayerPersonal, Pack: personal},
		},
		Policy: policy.Default(),
	})
	if err != nil {
		t.Fatalf("Merge: %v (default policy must succeed)", err)
	}
	if len(res.Overrides) != 1 {
		t.Errorf("expected one override; got %v", res.Overrides)
	}
}

func TestMerge_BudgetSoftTrimsByName(t *testing.T) {
	// Two entries totalling more than the budget; trimming keeps name-sorted.
	pol := policy.Default()
	pol.LayerSizeBudget = 100
	pol.BudgetMode = policy.BudgetSoftWarn
	big := fixturePack("org", pack.LayerOrg,
		pack.ContextEntry{Name: "a", Kind: pack.KindGlossary, Body: []byte("x"), SizeBytes: 60},
		pack.ContextEntry{Name: "b", Kind: pack.KindGlossary, Body: []byte("y"), SizeBytes: 60},
	)
	res, err := Merge(Request{
		Layers: []LayerInput{{Layer: pack.LayerOrg, Pack: big}},
		Policy: pol,
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "a" {
		t.Errorf("expected only 'a' kept under soft budget; got %+v", res.Entries)
	}
	if len(res.Layers[0].Trimmed) == 0 || res.Layers[0].Trimmed[0] != "b" {
		t.Errorf("trimmed list wrong: %+v", res.Layers[0].Trimmed)
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected a warning")
	}
}

func TestMerge_BudgetHardFails(t *testing.T) {
	pol := policy.Default()
	pol.LayerSizeBudget = 50
	pol.BudgetMode = policy.BudgetHardFail
	p := fixturePack("org", pack.LayerOrg,
		pack.ContextEntry{Name: "a", Kind: pack.KindGlossary, Body: []byte("x"), SizeBytes: 100},
	)
	_, err := Merge(Request{
		Layers: []LayerInput{{Layer: pack.LayerOrg, Pack: p}},
		Policy: pol,
	})
	var oe *policy.ErrOversizeLayer
	if !errors.As(err, &oe) {
		t.Fatalf("expected ErrOversizeLayer, got %v", err)
	}
	if oe.Layer != pack.LayerOrg {
		t.Errorf("layer = %q", oe.Layer)
	}
}

func TestMerge_EmptyLayersTolerated(t *testing.T) {
	res, err := Merge(Request{Policy: policy.Default()})
	if err != nil {
		t.Fatalf("empty merge must not error: %v", err)
	}
	if len(res.Entries) != 0 {
		t.Errorf("Entries len = %d", len(res.Entries))
	}
}

// findEntry is a tiny test helper.
func findEntry(r *Result, name string) (ResolvedEntry, bool) {
	for _, e := range r.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return ResolvedEntry{}, false
}

func TestErrConflict_ErrorContainsName(t *testing.T) {
	// belt-and-braces: confirm the error string actually exposes the
	// failing entry so operators can act without unmarshalling.
	c := &policy.ErrConflict{Conflicts: []policy.ConflictReport{{EntryName: "term"}}}
	if !strings.Contains(c.Error(), "term") {
		t.Errorf("error = %q", c.Error())
	}
}
