package fleet

// mandated_test.go — MandatedApplier (owner ruling 2026-10-06, WP02) and the
// ACK body's conformance with fleet's validation (kenaz-fleet PR #178).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// recordingMandatedWorkflows is a race-safe MandatedWorkflows that records
// the exact payload bytes it was handed.
type recordingMandatedWorkflows struct {
	mu   sync.Mutex
	got  [][]byte
	fail error
}

func (r *recordingMandatedWorkflows) InstallMandatedWorkflow(_ context.Context, _, _ string, payload []byte) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return "", r.fail
	}
	r.got = append(r.got, append([]byte(nil), payload...))
	return "wf", nil
}

func (r *recordingMandatedWorkflows) RemoveMandatedWorkflow(context.Context, string, string) error {
	return nil
}

func (r *recordingMandatedWorkflows) payloads() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.got...)
}

func skillItem(t *testing.T, catalogID, id, trigger string) BundleMandatedItem {
	t.Helper()
	raw, err := json.Marshal(slashcmd.Skill{ID: id, Trigger: trigger, Kind: slashcmd.KindText, Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return BundleMandatedItem{CatalogID: catalogID, Kind: MandatedKindSkill, Version: "1", Payload: raw}
}

// The applied set persists across applier instances (a restart): a skill
// mandated by bundle N is removed by bundle N+1 even from a fresh process.
func TestMandatedApplier_ReconcileSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	ctx := context.Background()

	first := &MandatedApplier{Skills: store, Registry: reg, DataDir: dir}
	if _, errs := first.Apply(ctx, []BundleMandatedItem{skillItem(t, "c1", "org/a", "acmd")}); len(errs) != 0 {
		t.Fatalf("apply: %v", errs)
	}
	second := &MandatedApplier{Skills: store, Registry: reg, DataDir: dir}
	st, errs := second.Apply(ctx, nil)
	if len(errs) != 0 || len(st) != 1 || st[0].Status != MandatedStatusRemoved || st[0].CatalogID != "c1" {
		t.Fatalf("restart reconcile: st=%+v errs=%v", st, errs)
	}
	if _, err := store.Get("org/a"); err == nil {
		t.Fatal("skill survived de-mandate after restart")
	}
}

// A skill the store marks mandated but no state file knows about (residue of
// the retired mandated_skills section) is reconciled on the first bundle and
// never reported to fleet (it has no catalog id).
func TestMandatedApplier_LegacyMandatedSkillSeeded(t *testing.T) {
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	if err := slashcmd.LiveRegister(store, reg, slashcmd.Skill{ID: "old", Trigger: "oldcmd", Kind: slashcmd.KindText, Body: "x", Source: slashcmd.SkillSourceMandated}); err != nil {
		t.Fatal(err)
	}
	m := &MandatedApplier{Skills: store, Registry: reg, DataDir: t.TempDir()}
	st, errs := m.Apply(context.Background(), nil)
	if len(errs) != 0 || len(st) != 0 {
		t.Fatalf("legacy reconcile st=%+v errs=%v, want silent removal", st, errs)
	}
	if _, err := store.Get("old"); err == nil {
		t.Fatal("legacy mandated skill not removed")
	}
}

// A user's own skill is never removed by reconciliation, even if its id
// matches a de-mandated one after the user replaced... (only mandated copies).
func TestMandatedApplier_RemovalLeavesNonMandatedSkill(t *testing.T) {
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	m := &MandatedApplier{Skills: store, Registry: reg}
	ctx := context.Background()
	if _, errs := m.Apply(ctx, []BundleMandatedItem{skillItem(t, "c1", "org/a", "acmd")}); len(errs) != 0 {
		t.Fatal(errs)
	}
	// Something else now owns the id (simulated: the stored copy is no longer mandated).
	sk, _ := store.Get("org/a")
	sk.Source = slashcmd.SkillSourceCatalog
	sk.OrgManaged = false
	if err := store.Save(sk); err != nil {
		t.Fatal(err)
	}
	if _, errs := m.Apply(ctx, nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := store.Get("org/a"); err != nil {
		t.Fatal("a non-mandated skill was removed by mandate reconciliation")
	}
}

// A failed update keeps tracking the earlier install, so a later de-mandate
// still removes it.
func TestMandatedApplier_FailedUpdateKeepsTracking(t *testing.T) {
	wf := &recordingMandatedWorkflows{}
	m := &MandatedApplier{Workflows: wf}
	ctx := context.Background()
	item := BundleMandatedItem{CatalogID: "c", Kind: MandatedKindWorkflow, Version: "1", Payload: json.RawMessage(`{"id":"w"}`)}
	if _, errs := m.Apply(ctx, []BundleMandatedItem{item}); len(errs) != 0 {
		t.Fatal(errs)
	}
	wf.mu.Lock()
	wf.fail = errors.New("boom")
	wf.mu.Unlock()
	item.Version = "2"
	st, errs := m.Apply(ctx, []BundleMandatedItem{item})
	if len(errs) != 1 || st[0].Status != MandatedStatusFailed {
		t.Fatalf("st=%+v errs=%v, want failed", st, errs)
	}
	st, _ = m.Apply(ctx, nil)
	if len(st) != 1 || st[0].Status != MandatedStatusRemoved {
		t.Fatalf("de-mandate after failed update: %+v, want removed", st)
	}
}

// Unknown future kinds are refused by name, like pack/bundle.
func TestMandatedApplier_UnknownKindRefused(t *testing.T) {
	m := &MandatedApplier{}
	st, errs := m.Apply(context.Background(), []BundleMandatedItem{
		{CatalogID: "c1", Kind: MandatedKindBundle, Payload: json.RawMessage(`{}`)},
		{CatalogID: "c2", Kind: "future-kind", Payload: json.RawMessage(`{}`)},
	})
	if len(errs) != 2 || !errors.Is(errs[0], ErrMandatedKindUnsupported) || !errors.Is(errs[1], ErrMandatedKindUnsupported) {
		t.Fatalf("errs = %v", errs)
	}
	for _, s := range st {
		if s.Status != MandatedStatusRefused {
			t.Errorf("%+v, want refused", s)
		}
	}
}

// ── ACK contract (kenaz-fleet PR #178) ──────────────────────────────────────

// fleetConfigACKItem mirrors kenaz-fleet PR #178 service/config_ack.go
// configACKItem.
type fleetConfigACKItem struct {
	CatalogID string `json:"catalog_id"`      // config_ack.go configACKItem.CatalogID
	Kind      string `json:"kind"`            // .Kind
	Version   string `json:"version"`         // .Version
	Status    string `json:"status"`          // .Status
	Error     string `json:"error,omitempty"` // .Error
}

// fleetConfigACKRequest mirrors PR #178 service/handlers_config.go
// configACKRequest.
type fleetConfigACKRequest struct {
	BundleID  int64                `json:"bundle_id"`            // handlers_config.go:224
	Applied   bool                 `json:"applied"`              // :226
	ErrorMsg  string               `json:"error,omitempty"`      // :228
	MachineID string               `json:"machine_id,omitempty"` // :232
	Items     []fleetConfigACKItem `json:"items,omitempty"`      // :234
	Errors    []string             `json:"errors,omitempty"`     // :236
}

// fleetValidateACK re-implements fleet's validACKMachineID +
// validateACKDetail (PR #178) so the harness ACK body is checked against the
// server's rules, not the harness's own.
func fleetValidateACK(req fleetConfigACKRequest) string {
	clean := func(s string, max int, allowWS bool) bool {
		if len(s) > max || !utf8.ValidString(s) {
			return false
		}
		for _, r := range s {
			if unicode.IsControl(r) && !(allowWS && (r == '\n' || r == '\t')) {
				return false
			}
		}
		return true
	}
	if !clean(req.MachineID, 128, false) {
		return "machine_id"
	}
	if len(req.Items) > 500 {
		return "items count"
	}
	if len(req.Errors) > 50 {
		return "errors count"
	}
	for i, e := range req.Errors {
		if !clean(e, 1024, true) {
			return fmt.Sprintf("errors[%d]", i)
		}
	}
	kinds := map[string]bool{"skill": true, "workflow": true, "pack": true, "bundle": true}
	statuses := map[string]bool{"applied": true, "refused": true, "failed": true, "removed": true}
	for i, it := range req.Items {
		if it.CatalogID != "" {
			if _, err := uuid.Parse(it.CatalogID); err != nil {
				return fmt.Sprintf("items[%d].catalog_id", i)
			}
		}
		if it.Kind != "" && !kinds[it.Kind] {
			return fmt.Sprintf("items[%d].kind", i)
		}
		if !clean(it.Version, 64, false) {
			return fmt.Sprintf("items[%d].version", i)
		}
		if !statuses[it.Status] {
			return fmt.Sprintf("items[%d].status", i)
		}
		if !clean(it.Error, 1024, true) {
			return fmt.Sprintf("items[%d].error", i)
		}
	}
	return ""
}

func TestConfigACK_HostileInputs_PassFleetValidation(t *testing.T) {
	long := strings.Repeat("é", 2000) // multi-byte: truncation must not split a rune
	var errs []error
	for i := 0; i < 80; i++ {
		errs = append(errs, fmt.Errorf("err %d\x00\x07 line\nnext\ttab %s", i, long))
	}
	items := make([]MandatedItemStatus, 0, 600)
	for i := 0; i < 600; i++ {
		items = append(items, MandatedItemStatus{
			CatalogID: "not-a-uuid", Kind: "agent_pack", Version: strings.Repeat("v", 100) + "\x01",
			Status: MandatedStatusFailed, Error: "bad\x1b[31m " + long,
		})
	}
	items[0].CatalogID = "11111111-1111-4111-8111-111111111111"
	items[0].Kind = MandatedKindSkill
	payload := buildConfigACKPayload(9, errs, ConfigACKReport{
		MachineID: strings.Repeat("M", 200) + "\r\n\x00",
		Items:     items,
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req fleetConfigACKRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fleet cannot decode the harness ACK: %v", err)
	}
	if bad := fleetValidateACK(req); bad != "" {
		t.Fatalf("fleet would 400 this ACK on %s", bad)
	}
	if req.Applied || req.MachineID == "" || len(req.Items) != 500 || len(req.Errors) != 50 {
		t.Fatalf("applied=%v machine=%q items=%d errors=%d", req.Applied, req.MachineID, len(req.Items), len(req.Errors))
	}
	if !strings.Contains(req.Errors[0], "\n") || !strings.Contains(req.Errors[0], "\t") {
		t.Error("\\n and \\t are allowed in error strings and must be kept")
	}
	if req.Items[0].CatalogID == "" || req.Items[0].Kind != "skill" {
		t.Errorf("a valid item lost its catalog_id/kind: %+v", req.Items[0])
	}
}
