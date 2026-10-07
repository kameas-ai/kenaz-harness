package fleet

// mandated_quiet_test.go — skill-library-01SKLIB01 WP04 (fleet H3 + H6)
// and ledger 2026-10-06 R2/R3. Persistence goes through the REAL
// <dataDir>/fleet/mandated_applied.json (and its pending side file); the
// skill store is the real file-backed SkillStore.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// ownerMandatedWorkflows is a race-safe MandatedWorkflows that tracks which
// catalog id owns each workflow id, like the real provenance check.
// In-memory deliberately (WP-PI AC-PI-2): the applier's own state goes
// through the real mandated_applied.json here; the workflow consumer's
// persistence is pinned on real sqlite in core/rpc/views/workflows.
type ownerMandatedWorkflows struct {
	mu      sync.Mutex
	owner   map[string]string // workflow id -> mandating catalog id
	removes []removeCall
}

type removeCall struct {
	workflowID, catalogID string
	prior                 json.RawMessage
	legacy                bool
}

func (o *ownerMandatedWorkflows) InstallMandatedWorkflow(_ context.Context, catalogID, _ string, payload []byte) (string, json.RawMessage, error) {
	var doc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(payload, &doc)
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.owner == nil {
		o.owner = map[string]string{}
	}
	o.owner[doc.ID] = catalogID
	return doc.ID, nil, nil
}

func (o *ownerMandatedWorkflows) RemoveMandatedWorkflow(_ context.Context, id, catalogID string, prior json.RawMessage, legacy bool) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.removes = append(o.removes, removeCall{id, catalogID, prior, legacy})
	if o.owner[id] != catalogID {
		return false, nil
	}
	delete(o.owner, id)
	return len(prior) > 0 || legacy, nil
}

func (o *ownerMandatedWorkflows) snapshot() []removeCall {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]removeCall(nil), o.removes...)
}

func auditKinds(e *recordingAuditEmitter) map[contextaudit.Kind]int {
	out := map[contextaudit.Kind]int{}
	for _, ev := range e.snapshot() {
		out[ev.kind]++
	}
	return out
}

func mandatedSkill(t *testing.T, catalogID, version, id, body string) BundleMandatedItem {
	t.Helper()
	raw, err := json.Marshal(slashcmd.Skill{ID: id, Trigger: id, Kind: slashcmd.KindText, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return BundleMandatedItem{CatalogID: catalogID, Kind: MandatedKindSkill, Version: version, Payload: raw}
}

func newQuietApplier(t *testing.T) (*MandatedApplier, *slashcmd.SkillStore, *slashcmd.Registry, *ownerMandatedWorkflows, *recordingAuditEmitter, string) {
	t.Helper()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	wf := &ownerMandatedWorkflows{}
	em := &recordingAuditEmitter{}
	dir := t.TempDir()
	return &MandatedApplier{Skills: store, Registry: reg, Workflows: wf, DataDir: dir, Emitter: em}, store, reg, wf, em, dir
}

// AC-4: promote v1→v2 of a mandated skill with the same payload skill id.
// ACK: applied v2 + removed v1. Local audit: one upgrade, zero removals. The
// skill stays registered and is v2.
func TestMandated_PromoteIsQuietUpgrade(t *testing.T) {
	m, store, reg, _, em, _ := newQuietApplier(t)
	ctx := context.Background()
	if _, errs := m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c-v1", "1.0.0", "policy", "v1")}); len(errs) != 0 {
		t.Fatal(errs)
	}
	st, errs := m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c-v2", "2.0.0", "policy", "v2")})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	by := statusByCatalogFleet(st)
	if by["c-v2"].Status != MandatedStatusApplied || by["c-v2"].Version != "2.0.0" {
		t.Errorf("v2 status = %+v", by["c-v2"])
	}
	if by["c-v1"].Status != MandatedStatusRemoved || by["c-v1"].Version != "1.0.0" {
		t.Errorf("v1 status = %+v — fleet still needs removed for the superseded version", by["c-v1"])
	}
	k := auditKinds(em)
	if k[contextaudit.KindFleetMandatedItemUpgraded] != 1 || k[contextaudit.KindFleetMandatedItemRemoved] != 0 {
		t.Errorf("local audit = %v, want exactly one upgrade and no removal", k)
	}
	sk, err := store.Get("policy")
	if err != nil || sk.CatalogID != "c-v2" || sk.Version != "2.0.0" || sk.Body != "v2" {
		t.Fatalf("stored skill = %+v, %v", sk, err)
	}
	if _, ok := reg.Lookup("policy"); !ok {
		t.Error("the promoted skill is not registered")
	}
	for _, ev := range em.snapshot() {
		if p, ok := ev.payload.(contextaudit.FleetMandatedItemPayload); ok && ev.kind == contextaudit.KindFleetMandatedItemUpgraded {
			if p.CatalogID != "c-v1" || p.ToCatalogID != "c-v2" || p.ToVersion != "2.0.0" || p.LocalID != "policy" {
				t.Errorf("upgrade payload = %+v", p)
			}
		}
	}
}

func statusByCatalogFleet(st []MandatedItemStatus) map[string]MandatedItemStatus {
	out := map[string]MandatedItemStatus{}
	for _, s := range st {
		out[s.CatalogID] = s
	}
	return out
}

// A real withdrawal is still audited as a removal.
func TestMandated_WithdrawalAuditedAsRemoval(t *testing.T) {
	m, store, _, _, em, _ := newQuietApplier(t)
	ctx := context.Background()
	_, _ = m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c1", "1", "policy", "x")})
	st, errs := m.Apply(ctx, nil)
	if len(errs) != 0 || len(st) != 1 || st[0].Status != MandatedStatusRemoved {
		t.Fatalf("withdraw = %+v %v", st, errs)
	}
	if _, err := store.Get("policy"); err == nil {
		t.Error("withdrawn skill still stored")
	}
	if k := auditKinds(em); k[contextaudit.KindFleetMandatedItemRemoved] != 1 || k[contextaudit.KindFleetMandatedItemUpgraded] != 0 {
		t.Errorf("local audit = %v", k)
	}
}

// The user's own skill a v1 mandate took over survives the v1→v2 promote:
// withdrawing v2 restores the user's skill (the prior is carried forward
// across the persisted state file).
func TestMandated_PromoteCarriesUserPriorForward(t *testing.T) {
	m, store, reg, _, em, dir := newQuietApplier(t)
	ctx := context.Background()
	if err := slashcmd.LiveRegister(store, reg, slashcmd.Skill{ID: "policy", Trigger: "policy", Kind: slashcmd.KindText,
		Body: "mine", Source: slashcmd.SkillSourceCatalog, CatalogID: "user-cat", Version: "0.1"}); err != nil {
		t.Fatal(err)
	}
	_, _ = m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c-v1", "1", "policy", "v1")})
	// A restart between bundles: a new applier over the same state file.
	m2 := &MandatedApplier{Skills: store, Registry: reg, Workflows: &ownerMandatedWorkflows{}, DataDir: dir, Emitter: em}
	_, _ = m2.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c-v2", "2", "policy", "v2")})
	if _, errs := m2.Apply(ctx, nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	sk, err := store.Get("policy")
	if err != nil || sk.Body != "mine" || sk.Source != slashcmd.SkillSourceCatalog || sk.CatalogID != "user-cat" {
		t.Fatalf("after withdrawing v2: %+v, %v — want the user's own skill restored", sk, err)
	}
}

// Workflow promote: the superseded version's removal is not attempted (the
// new version holds the workflow) and is audited as an upgrade.
func TestMandated_WorkflowPromoteIsQuiet(t *testing.T) {
	m, _, _, wf, em, _ := newQuietApplier(t)
	ctx := context.Background()
	item := func(cid, ver string) BundleMandatedItem {
		return BundleMandatedItem{CatalogID: cid, Kind: MandatedKindWorkflow, Version: ver, Payload: json.RawMessage(`{"id":"nightly"}`)}
	}
	_, _ = m.Apply(ctx, []BundleMandatedItem{item("w1", "1")})
	st, errs := m.Apply(ctx, []BundleMandatedItem{item("w2", "2")})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if by := statusByCatalogFleet(st); by["w1"].Status != MandatedStatusRemoved || by["w2"].Status != MandatedStatusApplied {
		t.Fatalf("statuses = %+v", st)
	}
	if calls := wf.snapshot(); len(calls) != 0 {
		t.Errorf("superseded workflow removal attempted: %+v", calls)
	}
	if k := auditKinds(em); k[contextaudit.KindFleetMandatedItemUpgraded] != 1 || k[contextaudit.KindFleetMandatedItemRemoved] != 0 {
		t.Errorf("local audit = %v", k)
	}
}

// H6 (pinned): every per-item status carries the ENVELOPE version — for
// applied, refused, failed and removed — never a version resolved from the
// consumer or the payload. Fleet's "on older version" adoption counts
// depend on it.
func TestMandated_ACKVersionAlwaysEnvelopeVersion(t *testing.T) {
	m, store, _, _, _, _ := newQuietApplier(t)
	ctx := context.Background()
	// The payload claims its own version; the envelope's must win.
	raw, _ := json.Marshal(slashcmd.Skill{ID: "s", Trigger: "s", Kind: slashcmd.KindText, Body: "b", Version: "0.0.1-payload"})
	first := []BundleMandatedItem{
		{CatalogID: "gone", Kind: MandatedKindSkill, Version: "7.0.0", Payload: mustSkillPayload(t, "gone")},
	}
	if _, errs := m.Apply(ctx, first); len(errs) != 0 {
		t.Fatal(errs)
	}
	items := []BundleMandatedItem{
		{CatalogID: "ok", Kind: MandatedKindSkill, Version: "1.2.3", Payload: raw},
		{CatalogID: "pk", Kind: MandatedKindPack, Version: "4.0.0", Payload: json.RawMessage(`{}`)},
		{CatalogID: "bad", Kind: MandatedKindSkill, Version: "9.9.9", Payload: json.RawMessage(`"not a skill"`)},
	}
	st, _ := m.Apply(ctx, items)
	want := map[string][2]string{
		"ok":   {MandatedStatusApplied, "1.2.3"},
		"pk":   {MandatedStatusRefused, "4.0.0"},
		"bad":  {MandatedStatusFailed, "9.9.9"},
		"gone": {MandatedStatusRemoved, "7.0.0"},
	}
	if len(st) != len(want) {
		t.Fatalf("statuses = %+v", st)
	}
	for _, s := range st {
		w := want[s.CatalogID]
		if s.Status != w[0] || s.Version != w[1] {
			t.Errorf("%s: status=%s version=%q, want %s %q (the envelope's)", s.CatalogID, s.Status, s.Version, w[0], w[1])
		}
	}
	if sk, _ := store.Get("s"); sk.Version != "1.2.3" {
		t.Errorf("stored version = %q, want the envelope's", sk.Version)
	}
}

func mustSkillPayload(t *testing.T, id string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(slashcmd.Skill{ID: id, Trigger: id, Kind: slashcmd.KindText, Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// R3: a takeover while mandated_applied.json is UNREADABLE records its
// prior beside the file (never over it). Once the file is repaired, the
// next run merges it, a withdrawal restores the user's skill, and the side
// file is cleared.
func TestMandated_TakeoverDuringUnreadableStatePersistsPrior(t *testing.T) {
	for _, repair := range []string{"rewritten", "deleted"} {
		t.Run(repair, func(t *testing.T) {
			m, store, reg, _, _, dir := newQuietApplier(t)
			ctx := context.Background()
			if err := slashcmd.LiveRegister(store, reg, slashcmd.Skill{ID: "policy", Trigger: "policy", Kind: slashcmd.KindText,
				Body: "mine", Source: slashcmd.SkillSourceCatalog, CatalogID: "user-cat", Version: "0.1"}); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(dir, "fleet", "mandated_applied.json")
			if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
				t.Fatal(err)
			}
			corrupt := []byte("{not json")
			if err := os.WriteFile(state, corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, errs := m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c1", "1", "policy", "org")}); len(errs) != 1 {
				t.Fatalf("errs = %v, want the one F5 state-file report", errs)
			}
			if got, _ := os.ReadFile(state); string(got) != string(corrupt) {
				t.Fatal("F5 violated: the unreadable state file was overwritten")
			}
			pending, err := os.ReadFile(filepath.Join(dir, "fleet", "mandated_applied.pending.json"))
			if err != nil {
				t.Fatalf("no pending record of the takeover: %v", err)
			}
			var ps mandatedState
			if err := json.Unmarshal(pending, &ps); err != nil || len(ps.Items["skill:c1"].PriorSkill) == 0 {
				t.Fatalf("pending = %s — want the takeover's prior_skill", pending)
			}
			// Still unreadable on a second bundle: the prior carries forward.
			_, _ = m.Apply(ctx, []BundleMandatedItem{mandatedSkill(t, "c1", "1", "policy", "org")})

			switch repair {
			case "rewritten":
				if err := os.WriteFile(state, []byte(`{"schema":1,"items":{}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := os.Remove(state); err != nil {
					t.Fatal(err)
				}
			}
			if _, errs := m.Apply(ctx, nil); len(errs) != 0 {
				t.Fatalf("withdrawal errs = %v", errs)
			}
			sk, err := store.Get("policy")
			if err != nil || sk.Body != "mine" || sk.Source != slashcmd.SkillSourceCatalog {
				t.Fatalf("after withdrawal: %+v, %v — want the user's skill restored, not deleted", sk, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "fleet", "mandated_applied.pending.json")); !os.IsNotExist(err) {
				t.Errorf("pending side file not cleared after a successful save: %v", err)
			}
		})
	}
}
