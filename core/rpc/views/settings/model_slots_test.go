package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/harnessmodels"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func servedHostProfile() *corellm.ProviderProfile {
	return &corellm.ProviderProfile{
		ID: "kenaz-host", Kind: "anthropic", Model: "claude-sonnet-4-5",
		Models: []string{"claude-sonnet-4-5", "claude-opus-5-5", "claude-opus-4-5", "claude-haiku-4-5"},
	}
}

type fixedStore struct {
	SettingsStore
	s Settings
}

func (f fixedStore) LoadAll() (Settings, error) { return f.s, nil }

func newModelSlotsAPI(t *testing.T, host harnessmodels.Overrides, s Settings) (*API, *compositeConfigApplier) {
	t.Helper()
	api := &API{store: fixedStore{s: s}}
	api.SetModelSlots(harnessmodels.NewResolver(host, nil), servedHostProfile)
	return api, &compositeConfigApplier{state: api.fleet}
}

func slotOf(t *testing.T, v ModelSlotsView, s harnessmodels.Slot) harnessmodels.Effective {
	t.Helper()
	for _, e := range v.Slots {
		if e.Slot == s {
			return e
		}
	}
	t.Fatalf("slot %s missing", s)
	return harnessmodels.Effective{}
}

func TestModelSlots_ServedEmptySlotsDerive(t *testing.T) {
	api, _ := newModelSlotsAPI(t, harnessmodels.Overrides{}, Settings{})
	v, err := api.EffectiveModelSlots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.ProviderID != "kenaz-host" || len(v.Slots) != len(harnessmodels.AllSlots) {
		t.Fatalf("view = %+v", v)
	}
	for _, e := range v.Slots {
		if e.Source != harnessmodels.SourceDerived || e.ModelID == "" {
			t.Errorf("%s = %+v, want derived", e.Slot, e)
		}
	}
	if e := slotOf(t, v, harnessmodels.SlotCompaction); e.ModelID != "claude-haiku-4-5" {
		t.Errorf("compaction = %s, want the cheapest", e.ModelID)
	}
}

func TestModelSlots_ExplicitSettingWins(t *testing.T) {
	api, _ := newModelSlotsAPI(t, harnessmodels.Overrides{Roles: harnessmodels.Roles{RiskRater: "claude-haiku-4-5"}},
		Settings{RiskRaterModel: ProviderProfileRef{ProviderID: "kenaz-host", ModelID: "claude-opus-4-5"}})
	e, ok := api.ResolveModelSlot(harnessmodels.SlotRiskRater)
	if !ok || e.Source != harnessmodels.SourceSettings || e.ModelID != "claude-opus-4-5" {
		t.Fatalf("risk rater = %+v", e)
	}
}

func TestApplyBundle_HarnessModels_AppliesValidatesAndClears(t *testing.T) {
	api, applier := newModelSlotsAPI(t, harnessmodels.Overrides{SubagentDefault: "claude-haiku-4-5"}, Settings{})

	payload := json.RawMessage(`{"subagent_default":"claude-opus-4-5","roles":{"advisor":"claude-not-a-model","compaction":"claude-sonnet-4-5"}}`)
	errs := applier.ApplyBundle(context.Background(), &fleet.Bundle{BundleID: 1, OrgConfig: map[string]json.RawMessage{OrgConfigHarnessModels: payload}})
	if len(errs) != 1 || !errors.Is(errs[0], harnessmodels.ErrUnknownModel) || !strings.Contains(errs[0].Error(), "advisor") {
		t.Fatalf("errs = %v, want one unknown-model ack error for advisor", errs)
	}
	v, _ := api.EffectiveModelSlots(context.Background())
	if e := slotOf(t, v, harnessmodels.SlotSubagentDefault); e.Source != harnessmodels.SourceOrg || e.ModelID != "claude-opus-4-5" {
		t.Fatalf("subagent default = %+v, want org (beats host)", e)
	}
	if e := slotOf(t, v, harnessmodels.SlotCompaction); e.Source != harnessmodels.SourceOrg {
		t.Fatalf("compaction = %+v, want org", e)
	}
	if e := slotOf(t, v, harnessmodels.SlotAdvisor); e.Source != harnessmodels.SourceDerived {
		t.Fatalf("advisor = %+v, want derived (org value refused)", e)
	}
	if len(v.OrgErrors) != 1 {
		t.Fatalf("org errors = %v", v.OrgErrors)
	}

	// Next bundle without the section: org layer cleared, host is back.
	if errs := applier.ApplyBundle(context.Background(), &fleet.Bundle{BundleID: 2}); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	v, _ = api.EffectiveModelSlots(context.Background())
	if e := slotOf(t, v, harnessmodels.SlotSubagentDefault); e.Source != harnessmodels.SourceHost || e.ModelID != "claude-haiku-4-5" {
		t.Fatalf("after clear = %+v, want host", e)
	}
	if len(v.OrgErrors) != 0 {
		t.Fatalf("org errors not cleared: %v", v.OrgErrors)
	}
}

func TestMapSubagentModel(t *testing.T) {
	api, _ := newModelSlotsAPI(t, harnessmodels.Overrides{}, Settings{})
	p, m, src, ok := api.MapSubagentModel("anthropic/claude-haiku-4.5")
	if !ok || p != "kenaz-host" || m != "claude-haiku-4-5" || src != harnessmodels.SourceDerived {
		t.Fatalf("mapped = %s %s %s %v", p, m, src, ok)
	}
	if _, m, src, _ = api.MapSubagentModel("claude-opus-4-5"); m != "claude-opus-4-5" || src != harnessmodels.SourceProfile {
		t.Fatalf("exact = %s %s", m, src)
	}
	// host subagent default beats a tier mapping…
	api2, _ := newModelSlotsAPI(t, harnessmodels.Overrides{SubagentDefault: "claude-opus-5-5"}, Settings{})
	if _, m, src, _ = api2.MapSubagentModel("anthropic/claude-haiku-4.5"); m != "claude-opus-5-5" || src != harnessmodels.SourceHost {
		t.Fatalf("host default = %s %s", m, src)
	}
	// …and an unmappable declared model falls back to the derived default.
	if _, m, _, ok = api.MapSubagentModel("mystery"); !ok || m != "claude-sonnet-4-5" {
		t.Fatalf("fallback = %s %v", m, ok)
	}
	// Desktop: no session provider → no derivation.
	desk := &API{store: fixedStore{}}
	desk.SetModelSlots(harnessmodels.NewResolver(harnessmodels.Overrides{}, nil), nil)
	if _, _, _, ok := desk.MapSubagentModel("anthropic/claude-haiku-4.5"); ok {
		t.Fatal("desktop derived a model")
	}
}
