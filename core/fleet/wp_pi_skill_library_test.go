package fleet

// wp_pi_skill_library_test.go — skill-library-01SKLIB01 WP-PI.
//
// mandated_applied.json is file state, outside sqlite (v0.91.0
// PROVENANCE.md: "do not hunt for missing migrations"), so AC-PI-1's
// "start from what a previous release produced" means: start from the
// exact bytes the v0.91.0 applier wrote. The fixture below is
// json.Marshal(mandatedState{Schema: 1, ...}) of the v0.91.0 struct shape
// (catalog_id, kind, version, local_id, prior_skill, prior_catalog_workflow
// — no prior_workflow), written to the real path. WP04's code must read it,
// honour both kinds of prior it recorded, and write a file a v0.91.0
// binary can still read (downgrade safety: no field renamed or dropped).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// v091MandatedRecord is the v0.91.0 record shape, verbatim.
type v091MandatedRecord struct {
	CatalogID            string          `json:"catalog_id"`
	Kind                 string          `json:"kind"`
	Version              string          `json:"version"`
	LocalID              string          `json:"local_id"`
	PriorSkill           json.RawMessage `json:"prior_skill,omitempty"`
	PriorCatalogWorkflow bool            `json:"prior_catalog_workflow,omitempty"`
}

type v091MandatedState struct {
	Schema int                           `json:"schema"`
	Items  map[string]v091MandatedRecord `json:"items"`
}

func TestWPPI_V091MandatedAppliedFileRoundTrips(t *testing.T) {
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Device state v0.91 left: the mandated copy is installed, the user's
	// own catalog skill it took over is recorded as prior_skill.
	userSkill := slashcmd.Skill{ID: "policy", Trigger: "policy", Kind: slashcmd.KindText, Body: "mine",
		Source: slashcmd.SkillSourceCatalog, CatalogID: "user-cat", Version: "0.1"}
	priorRaw, _ := json.Marshal(userSkill)
	if err := slashcmd.LiveRegister(store, reg, slashcmd.Skill{ID: "policy", Trigger: "policy", Kind: slashcmd.KindText,
		Body: "org", Source: slashcmd.SkillSourceMandated, OrgManaged: true, CatalogID: "c-skill", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	v091 := v091MandatedState{Schema: 1, Items: map[string]v091MandatedRecord{
		"skill:c-skill":   {CatalogID: "c-skill", Kind: "skill", Version: "1", LocalID: "policy", PriorSkill: priorRaw},
		"workflow:c-wf":   {CatalogID: "c-wf", Kind: "workflow", Version: "3", LocalID: "nightly", PriorCatalogWorkflow: true},
		"workflow:c-keep": {CatalogID: "c-keep", Kind: "workflow", Version: "1", LocalID: "keep"},
	}}
	raw, _ := json.Marshal(v091)
	statePath := filepath.Join(dir, "fleet", "mandated_applied.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	// A pending side file to absorb, so the file HEAD writes carries a
	// non-empty absorbed_pending (review F2's additive field).
	pend, _ := json.Marshal(mandatedState{Schema: 1, Items: map[string]mandatedRecord{
		"workflow:c-keep": {CatalogID: "c-keep", Kind: "workflow", Version: "1", LocalID: "keep"},
	}})
	if err := os.WriteFile(filepath.Join(dir, "fleet", "mandated_applied.pending.json"), pend, 0o600); err != nil {
		t.Fatal(err)
	}

	wf := &ownerMandatedWorkflows{owner: map[string]string{"nightly": "c-wf", "keep": "c-keep"}}
	m := &MandatedApplier{Skills: store, Registry: reg, Workflows: wf, DataDir: dir}

	// 1. A bundle that still mandates c-keep only: the v0.91 records load,
	//    the skill's prior is restored, the v0.91 workflow record (flag, no
	//    snapshot) is withdrawn through the LEGACY relabel path.
	keep := BundleMandatedItem{CatalogID: "c-keep", Kind: MandatedKindWorkflow, Version: "1", Payload: json.RawMessage(`{"id":"keep"}`)}
	st, errs := m.Apply(ctx, []BundleMandatedItem{keep})
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	by := statusByCatalogFleet(st)
	if by["c-skill"].Status != MandatedStatusRemoved || by["c-wf"].Status != MandatedStatusRemoved || by["c-keep"].Status != MandatedStatusApplied {
		t.Fatalf("statuses = %+v", st)
	}
	sk, err := store.Get("policy")
	if err != nil || sk.Body != "mine" || sk.CatalogID != "user-cat" {
		t.Fatalf("v0.91 prior_skill not restored: %+v %v", sk, err)
	}
	var legacy *removeCall
	for _, c := range wf.snapshot() {
		if c.workflowID == "nightly" {
			c := c
			legacy = &c
		}
	}
	if legacy == nil || !legacy.legacy || len(legacy.prior) != 0 {
		t.Fatalf("v0.91 workflow record removal = %+v, want the legacy relabel (flag, no snapshot)", legacy)
	}

	// 2. The file WP04 wrote is still readable by the v0.91 shape: same
	//    schema, no renamed or dropped field (downgrade safety).
	out, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var head mandatedState
	if err := json.Unmarshal(out, &head); err != nil || head.AbsorbedPending == "" {
		t.Fatalf("HEAD's file should carry a non-empty absorbed_pending: %v\n%s", err, out)
	}
	// Plain Unmarshal (as v0.91 decodes — no DisallowUnknownFields): the
	// unknown absorbed_pending key must not break the downgrade read.
	var back v091MandatedState
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("v0.91 cannot decode the file WP04 wrote: %v\n%s", err, out)
	}
	if back.Schema != 1 || len(back.Items) != 1 || back.Items["workflow:c-keep"].LocalID != "keep" {
		t.Fatalf("round-tripped state = %+v", back)
	}
}
