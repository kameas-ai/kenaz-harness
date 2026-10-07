package settings

// Owner wire-contract ruling 2026-10-06, WP02: the composite applier
// dispatches mandated_items by kind, reconciles against the previously
// applied set (an EMPTY section removes everything), and hands per-item
// statuses to the ACK.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// fakeMandatedWorkflows is a race-safe fleet.MandatedWorkflows. In-memory
// deliberately (WP-PI AC-PI-2): these tests pin the composite applier's
// dispatch and ACK; workflow persistence is pinned in core/rpc/views/workflows.
type fakeMandatedWorkflows struct {
	mu        sync.Mutex
	installed map[string]string // workflowID -> catalogID
	removed   []string
}

func (f *fakeMandatedWorkflows) InstallMandatedWorkflow(_ context.Context, catalogID, _ string, payload []byte) (string, json.RawMessage, error) {
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.ID == "" {
		return "", nil, errors.New("not a workflow document")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installed == nil {
		f.installed = map[string]string{}
	}
	f.installed[doc.ID] = catalogID
	return doc.ID, nil, nil
}

func (f *fakeMandatedWorkflows) RemoveMandatedWorkflow(_ context.Context, workflowID, catalogID string, _ json.RawMessage, _ bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.installed[workflowID] == catalogID {
		delete(f.installed, workflowID)
	}
	f.removed = append(f.removed, workflowID)
	return false, nil
}

func (f *fakeMandatedWorkflows) snapshot() (map[string]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := make(map[string]string, len(f.installed))
	for k, v := range f.installed {
		inst[k] = v
	}
	return inst, append([]string(nil), f.removed...)
}

func mandatedFixture(t *testing.T) (*compositeConfigApplier, *slashcmd.SkillStore, *slashcmd.Registry, *fakeMandatedWorkflows) {
	t.Helper()
	store := slashcmd.NewSkillStore(t.TempDir())
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	wf := &fakeMandatedWorkflows{}
	state := &fleetState{dataDir: t.TempDir(), skillStore: store, skillRegistry: registry, mandatedWorkflows: wf}
	return &compositeConfigApplier{state: state}, store, registry, wf
}

func statusByCatalog(st []fleet.MandatedItemStatus) map[string]fleet.MandatedItemStatus {
	out := map[string]fleet.MandatedItemStatus{}
	for _, s := range st {
		out[s.CatalogID] = s
	}
	return out
}

func TestApplyBundleItems_DispatchByKind_ThenEmptySectionRemovesAll(t *testing.T) {
	applier, store, registry, wf := mandatedFixture(t)
	ctx := context.Background()
	skill, _ := json.Marshal(slashcmd.Skill{ID: "org/review", Trigger: "review", Kind: slashcmd.KindText, Body: "b"})
	b1 := &fleet.Bundle{BundleID: 1, MandatedItems: []fleet.BundleMandatedItem{
		{CatalogID: "c-pack", Kind: fleet.MandatedKindPack, Version: "1", Payload: json.RawMessage(`{}`)},
		{CatalogID: "c-skill", Kind: fleet.MandatedKindSkill, Version: "1.2.0", Payload: skill},
		{CatalogID: "c-wf", Kind: fleet.MandatedKindWorkflow, Version: "3", Payload: json.RawMessage(`{"id":"nightly"}`)},
	}}
	errs, st := applier.ApplyBundleItems(ctx, b1)
	by := statusByCatalog(st)
	if by["c-skill"].Status != fleet.MandatedStatusApplied || by["c-wf"].Status != fleet.MandatedStatusApplied {
		t.Fatalf("skill/workflow statuses = %+v", st)
	}
	if by["c-pack"].Status != fleet.MandatedStatusRefused || by["c-pack"].Error == "" {
		t.Fatalf("pack status = %+v, want refused with a named error", by["c-pack"])
	}
	// Review F1: a refused kind is a per-item status naming the error —
	// never a silent drop, but NOT a bundle error either (applied:true).
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none: refusals are per-item, the bundle applies", errs)
	}
	if !strings.Contains(by["c-pack"].Error, fleet.ErrMandatedKindUnsupported.Error()) {
		t.Fatalf("pack status error %q does not name ErrMandatedKindUnsupported", by["c-pack"].Error)
	}
	// The skill is the org's copy, stamped with the envelope's catalog id + version.
	sk, err := store.Get("org/review")
	if err != nil || sk.Source != slashcmd.SkillSourceMandated || sk.CatalogID != "c-skill" || sk.Version != "1.2.0" {
		t.Fatalf("stored skill = %+v, %v", sk, err)
	}
	if _, ok := registry.Lookup("review"); !ok {
		t.Fatal("mandated skill not registered")
	}
	if inst, _ := wf.snapshot(); inst["nightly"] != "c-wf" {
		t.Fatalf("workflow not installed via the workflow consumer: %v", inst)
	}

	// Bundle 2: empty section. Everything previously mandated is removed.
	errs, st = applier.ApplyBundleItems(ctx, &fleet.Bundle{BundleID: 2})
	if len(errs) != 0 {
		t.Fatalf("empty-section reconcile errs = %v", errs)
	}
	by = statusByCatalog(st)
	if by["c-skill"].Status != fleet.MandatedStatusRemoved || by["c-wf"].Status != fleet.MandatedStatusRemoved {
		t.Fatalf("statuses = %+v, want both removed", st)
	}
	if _, ok := by["c-pack"]; ok {
		t.Error("a refused (never installed) item must not be reported removed")
	}
	if _, err := store.Get("org/review"); err == nil {
		t.Error("de-mandated skill still in the store")
	}
	if _, ok := registry.Lookup("review"); ok {
		t.Error("de-mandated skill still registered")
	}
	if inst, removed := wf.snapshot(); len(inst) != 0 || len(removed) != 1 {
		t.Errorf("workflow consumer: installed=%v removed=%v, want removed", inst, removed)
	}

	// Bundle 3: still empty — nothing left to remove, nothing reported.
	if errs, st = applier.ApplyBundleItems(ctx, &fleet.Bundle{BundleID: 3}); len(errs) != 0 || len(st) != 0 {
		t.Errorf("idempotent empty reconcile: errs=%v statuses=%v", errs, st)
	}
}

func TestApplyBundleItems_WorkflowConsumerUnwired_FailsNamed(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{dataDir: t.TempDir()}}
	errs, st := applier.ApplyBundleItems(context.Background(), &fleet.Bundle{BundleID: 1, MandatedItems: []fleet.BundleMandatedItem{
		{CatalogID: "c-wf", Kind: fleet.MandatedKindWorkflow, Version: "1", Payload: json.RawMessage(`{"id":"w"}`)},
	}})
	if len(st) != 1 || st[0].Status != fleet.MandatedStatusFailed {
		t.Fatalf("statuses = %+v, want failed", st)
	}
	named := false
	for _, e := range errs {
		named = named || errors.Is(e, fleet.ErrMandatedConsumerUnwired)
	}
	if !named {
		t.Fatalf("errs = %v, want ErrMandatedConsumerUnwired", errs)
	}
}

// End to end through the REAL ConfigPoller: a signed bundle with a mandated
// skill + pack; the ACK body on the wire carries machine_id and the per-item
// statuses (fleet's delivered ACK shape).
func TestConfigPoller_MandatedItems_ACKCarriesMachineAndItems(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	restore := fleet.SetSigningKeyForTesting(pub)
	defer restore()

	var ackMu sync.Mutex
	var ackBody []byte
	snapshotACK := func() []byte {
		ackMu.Lock()
		defer ackMu.Unlock()
		return append([]byte(nil), ackBody...)
	}
	skill, _ := json.Marshal(slashcmd.Skill{ID: "org/x", Trigger: "xcmd", Kind: slashcmd.KindText, Body: "b"})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/configs", func(w http.ResponseWriter, r *http.Request) {
		b := &fleet.Bundle{BundleID: 1, IssuedAt: time.Now(), MandatedItems: []fleet.BundleMandatedItem{
			{CatalogID: "11111111-1111-4111-8111-111111111111", Kind: fleet.MandatedKindPack, Version: "1", Payload: json.RawMessage(`{}`)},
			{CatalogID: "22222222-2222-4222-8222-222222222222", Kind: fleet.MandatedKindSkill, Version: "1", Payload: skill},
		}}
		if err := fleet.SignBundleForTesting(b, priv); err != nil {
			t.Errorf("sign: %v", err)
			return
		}
		data, _ := json.Marshal(b)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/api/v1/configs/1/ack", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ackMu.Lock()
		ackBody = body
		ackMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := fleet.SaveTokens(fleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fleet.ClearTokens() }()
	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC()})

	applier, _, _, _ := mandatedFixture(t)
	poller := fleet.NewConfigPoller(fleet.NewClientForTesting(srv.URL), applier.state.dataDir, applier)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()

	var raw []byte
	for deadline := time.Now().Add(1500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if raw = snapshotACK(); len(raw) > 0 {
			break
		}
	}
	if len(raw) == 0 {
		t.Fatal("ACK never posted")
	}
	var ack struct {
		Applied   bool     `json:"applied"`
		MachineID string   `json:"machine_id"`
		Errors    []string `json:"errors"`
		Items     []struct {
			CatalogID string `json:"catalog_id"`
			Kind      string `json:"kind"`
			Version   string `json:"version"`
			Status    string `json:"status"`
			Error     string `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("decode ACK %s: %v", raw, err)
	}
	// Review F1: a refused pack is items[].status=refused with applied:true.
	if !ack.Applied {
		t.Errorf("applied:false for a bundle whose only non-applied item was REFUSED — refusals must not fail the bundle (errors=%v)", ack.Errors)
	}
	if ack.MachineID == "" {
		t.Error("ACK carries no machine_id")
	}
	got := map[string]string{}
	for _, it := range ack.Items {
		got[it.Kind] = it.Status
	}
	if got["skill"] != "applied" || got["pack"] != "refused" {
		t.Errorf("ACK items = %+v, want skill applied + pack refused", ack.Items)
	}
}

// Review F12: a bundle that fails verification never reaches the applier,
// so an (attacker-served) EMPTY mandated_items section removes nothing.
func TestConfigPoller_UnverifiableEmptyBundle_ZeroRemovals(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, _ := ed25519.GenerateKey(rand.Reader)
	restore := fleet.SetSigningKeyForTesting(pub)
	defer restore()

	applier, store, registry, _ := mandatedFixture(t)
	skill, _ := json.Marshal(slashcmd.Skill{ID: "org/keep", Trigger: "keepcmd", Kind: slashcmd.KindText, Body: "b"})
	if errs, _ := applier.ApplyBundleItems(context.Background(), &fleet.Bundle{BundleID: 1, MandatedItems: []fleet.BundleMandatedItem{
		{CatalogID: "c-keep", Kind: fleet.MandatedKindSkill, Version: "1", Payload: skill},
	}}); len(errs) != 0 {
		t.Fatal(errs)
	}

	var mu sync.Mutex
	served := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/configs", func(w http.ResponseWriter, r *http.Request) {
		b := &fleet.Bundle{BundleID: 2, IssuedAt: time.Now()} // empty mandated_items
		_ = fleet.SignBundleForTesting(b, foreign)            // NOT the pinned key
		data, _ := json.Marshal(b)
		mu.Lock()
		served++
		mu.Unlock()
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := fleet.SaveTokens(fleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fleet.ClearTokens() }()
	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC()})
	poller := fleet.NewConfigPoller(fleet.NewClientForTesting(srv.URL), applier.state.dataDir, applier)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()
	for deadline := time.Now().Add(1500 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if poller.Status().LastError != "" {
			break
		}
	}
	mu.Lock()
	n := served
	mu.Unlock()
	if n == 0 || poller.Status().LastError == "" {
		t.Fatalf("fixture: unverifiable bundle not served/rejected (served=%d, status=%+v)", n, poller.Status())
	}
	if _, err := store.Get("org/keep"); err != nil {
		t.Fatal("an unverifiable empty bundle removed a mandated skill")
	}
	if _, ok := registry.Lookup("keepcmd"); !ok {
		t.Fatal("an unverifiable empty bundle unregistered a mandated skill")
	}
}
