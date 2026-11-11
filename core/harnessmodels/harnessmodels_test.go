package harnessmodels

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// hostProfile mirrors what Kenaz delivers today: KENAZ_HARNESS_MODEL is the
// first catalog entry, KENAZ_HARNESS_MODELS the whole list.
func hostProfile() *corellm.ProviderProfile {
	return &corellm.ProviderProfile{
		ID:     "kenaz-host",
		Kind:   "anthropic",
		Model:  "claude-sonnet-4-5",
		Models: []string{"claude-sonnet-4-5", "claude-opus-5-5", "claude-opus-4-5", "claude-haiku-4-5"},
	}
}

func bySlot(t *testing.T, effs []Effective) map[Slot]Effective {
	t.Helper()
	out := map[Slot]Effective{}
	for _, e := range effs {
		out[e.Slot] = e
	}
	return out
}

func TestResolve_DerivedDefaultsFromSessionProvider(t *testing.T) {
	r := NewResolver(Overrides{}, nil)
	got := bySlot(t, r.ResolveAll(Inputs{Provider: hostProfile()}))
	want := map[Slot]string{
		SlotSubagentDefault: "claude-sonnet-4-5",
		SlotBranchAdvisor:   "claude-sonnet-4-5",
		SlotCompaction:      "claude-haiku-4-5",
		SlotAdvisor:         "claude-sonnet-4-5",
		SlotRiskRater:       "claude-sonnet-4-5",
	}
	for s, m := range want {
		e := got[s]
		if e.ModelID != m || e.Source != SourceDerived || e.ProviderID != "kenaz-host" {
			t.Errorf("%s = %+v, want %s derived on kenaz-host", s, e, m)
		}
	}
}

func TestResolve_NoProviderResolvesNothing(t *testing.T) {
	r := NewResolver(Overrides{SubagentDefault: "claude-haiku-4-5"}, nil)
	e := r.Resolve(SlotSubagentDefault, Inputs{})
	if e.Source != SourceNone || e.ModelID != "" {
		t.Fatalf("got %+v, want none", e)
	}
}

func TestResolve_Precedence(t *testing.T) {
	r := NewResolver(Overrides{Roles: Roles{Compaction: "claude-opus-4-5", Advisor: "claude-haiku-4-5"}}, nil)
	in := Inputs{Provider: hostProfile(), Settings: map[Slot]Ref{
		SlotAdvisor: {ProviderID: "openrouter", ModelID: "x/y"},
	}}
	// host beats derived
	if e := r.Resolve(SlotCompaction, in); e.Source != SourceHost || e.ModelID != "claude-opus-4-5" {
		t.Fatalf("compaction = %+v, want host", e)
	}
	// explicit setting beats host
	if e := r.Resolve(SlotAdvisor, in); e.Source != SourceSettings || e.ModelID != "x/y" {
		t.Fatalf("advisor = %+v, want settings", e)
	}
	// org beats settings and host
	r.SetOrg(Overrides{Roles: Roles{Advisor: "claude-opus-5-5", Compaction: "claude-sonnet-4-5"}})
	if e := r.Resolve(SlotAdvisor, in); e.Source != SourceOrg || e.ModelID != "claude-opus-5-5" {
		t.Fatalf("advisor = %+v, want org", e)
	}
	if e := r.Resolve(SlotCompaction, in); e.Source != SourceOrg || e.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("compaction = %+v, want org", e)
	}
	// clearing org falls back
	r.SetOrg(Overrides{})
	if e := r.Resolve(SlotAdvisor, in); e.Source != SourceSettings {
		t.Fatalf("advisor after clear = %+v, want settings", e)
	}
}

func TestResolve_HostValueNotOfferedFallsThrough(t *testing.T) {
	r := NewResolver(Overrides{SubagentDefault: "gpt-unknown"}, nil)
	if e := r.Resolve(SlotSubagentDefault, Inputs{Provider: hostProfile()}); e.Source != SourceDerived {
		t.Fatalf("got %+v, want derived (host model not offered)", e)
	}
}

func TestParseHostEnv(t *testing.T) {
	env := map[string]string{
		EnvSubagentModel: "claude-haiku-4-5",
		EnvRoleModels:    `{"compaction":"claude-haiku-4-5","risk_rater":"claude-sonnet-4-5","bogus":"x"}`,
	}
	o, errs := ParseHostEnv(func(k string) string { return env[k] })
	if o.SubagentDefault != "claude-haiku-4-5" || o.Roles.Compaction != "claude-haiku-4-5" || o.Roles.RiskRater != "claude-sonnet-4-5" {
		t.Fatalf("overrides = %+v", o)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "bogus") {
		t.Fatalf("errs = %v, want one unknown-role error", errs)
	}
	_, errs = ParseHostEnv(func(k string) string {
		if k == EnvRoleModels {
			return "not json"
		}
		return ""
	})
	if len(errs) != 1 {
		t.Fatalf("malformed JSON errs = %v", errs)
	}
	_, errs = ParseHostEnv(func(k string) string {
		if k == EnvSubagentModel {
			return "a b"
		}
		return ""
	})
	if len(errs) != 1 {
		t.Fatalf("malformed id errs = %v", errs)
	}
}

func TestParseOrgPayload(t *testing.T) {
	o, errs := ParseOrgPayload(json.RawMessage(`{"subagent_default":"claude-sonnet-4-5","roles":{"advisor":"claude-opus-5-5","compaction":"claude-haiku-4-5","branch_advisor":"claude-sonnet-4-5","risk_rater":"claude-haiku-4-5"}}`))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if o.SubagentDefault != "claude-sonnet-4-5" || o.Roles.Advisor != "claude-opus-5-5" || o.Roles.BranchAdvisor != "claude-sonnet-4-5" || o.Roles.RiskRater != "claude-haiku-4-5" {
		t.Fatalf("o = %+v", o)
	}
	_, errs = ParseOrgPayload(json.RawMessage(`{"subagent_defualt":"x"}`))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "unknown key") {
		t.Fatalf("typo errs = %v", errs)
	}
	if _, errs = ParseOrgPayload(json.RawMessage(`[1]`)); len(errs) != 1 {
		t.Fatalf("non-object errs = %v", errs)
	}
	if o, errs = ParseOrgPayload(nil); len(errs) != 0 || !o.IsZero() {
		t.Fatalf("nil payload: %+v %v", o, errs)
	}
}

func TestValidateModels(t *testing.T) {
	o := Overrides{SubagentDefault: "claude-sonnet-4-5", Roles: Roles{Advisor: "gpt-9"}}
	kept, errs := ValidateModels(o, hostProfile().Models)
	if kept.SubagentDefault != "claude-sonnet-4-5" || kept.Roles.Advisor != "" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(errs) != 1 || !errors.Is(errs[0], ErrUnknownModel) || !strings.Contains(errs[0].Error(), "advisor") {
		t.Fatalf("errs = %v", errs)
	}
}

func TestMapModel_DeclaredProfileModelsOntoHostProvider(t *testing.T) {
	r := NewResolver(Overrides{}, nil)
	p := *hostProfile()
	cases := map[string]string{
		"anthropic/claude-haiku-4.5":  "claude-haiku-4-5", // explore / summarizer
		"anthropic/claude-sonnet-4.5": "claude-sonnet-4-5", // implementer / reviewers
		"claude-opus-4-5":             "claude-opus-4-5",   // exact
	}
	for declared, want := range cases {
		got, ok := r.MapModel(declared, p)
		if !ok || got != want {
			t.Errorf("MapModel(%q) = %q,%v want %q", declared, got, ok, want)
		}
	}
	if _, ok := r.MapModel("totally-unknown-model", p); ok {
		t.Error("unknown declared model mapped")
	}
}
