package toolexposure

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func resolveWith(t *testing.T, org OrgPolicy, user Settings, sessionOverride Exposure, cat []CatalogTool) ResolvedCatalog {
	t.Helper()
	rc, err := Resolve(context.Background(), Deps{
		Settings: fakeSettings{s: user},
		Sessions: fakeSessions{st: SessionState{Override: sessionOverride}},
		Projects: fakeProjects{},
		Pins:     fakePins{p: org},
	}, "s1", cat)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return rc
}

// A pinned:true entry beats the user; the same entry marked pinned:false
// is an org default the user overrides — and that decides only when the
// user is silent.
func TestOrg_PinVersusDefault(t *testing.T) {
	user := Settings{Exposure: srv(TierSummary)}
	pin := OrgPolicy{Pins: srv(TierFull), BundleID: 9}
	def := OrgPolicy{Defaults: srv(TierFull), BundleID: 9}

	if got, _ := resolveWith(t, pin, user, Exposure{}, []CatalogTool{sendMail}).Tool(sendMail.Name); got.Tier != TierFull || got.Source != LevelOrgPin {
		t.Fatalf("pinned: %+v, want full from org_pin over the user's summary", got)
	}
	if got, _ := resolveWith(t, def, user, Exposure{}, []CatalogTool{sendMail}).Tool(sendMail.Name); got.Tier != TierSummary || got.Source != LevelUser {
		t.Fatalf("org default: %+v, want the user's summary over the org default", got)
	}
	if got, _ := resolveWith(t, def, Settings{}, Exposure{}, []CatalogTool{sendMail}).Tool(sendMail.Name); got.Tier != TierFull || got.Source != LevelOrgDefault {
		t.Fatalf("org default, user silent: %+v, want full from org_default over the harness summary", got)
	}
	// A pinned server tier also beats a lower layer's tool entry.
	if got, _ := resolveWith(t, OrgPolicy{Pins: srv(TierOff)}, Settings{}, toolLevel(TierFull), []CatalogTool{sendMail}).Tool(sendMail.Name); got.Tier != TierOff || got.Source != LevelOrgPin {
		t.Fatalf("server pin vs session tool entry: %+v, want off from org_pin", got)
	}
}

// hot_set_extra tools resolve full ahead of every layer, pin included,
// and land in the Hot segment so they are part of the stable prefix.
func TestOrg_HotSetExtra(t *testing.T) {
	org := OrgPolicy{
		Pins:        srv(TierOff),
		HotSetExtra: []string{sendMail.Name},
	}
	other := CatalogTool{Name: "outlook__list-messages", Server: "outlook", Running: true}
	rc := resolveWith(t, org, Settings{Exposure: toolLevel(TierOff)}, toolLevel(TierSummary), []CatalogTool{sendMail, other})
	if got, _ := rc.Tool(sendMail.Name); got.Tier != TierFull || got.Source != LevelOrgHotSet {
		t.Fatalf("send-mail = %+v, want full from org_hot_set", got)
	}
	if got, _ := rc.Tool(other.Name); got.Tier != TierOff || got.Source != LevelOrgPin {
		t.Fatalf("list-messages = %+v, want off from the server pin", got)
	}
	p := rc.Partition()
	if len(p.Hot) != 1 || p.Hot[0].Name != sendMail.Name || len(p.Pinned) != 0 {
		t.Fatalf("partition hot=%v pinned=%v, want send-mail in Hot", p.Hot, p.Pinned)
	}
}

func TestOrg_BudgetPin(t *testing.T) {
	user := Settings{SchemaBudgetTokens: 9000}
	rc := resolveWith(t, OrgPolicy{SchemaBudgetTokens: 5000, BundleID: 3}, user, Exposure{}, nil)
	if rc.SchemaBudgetTokens != 5000 || rc.OrgBundleID != 3 {
		t.Fatalf("budget = %d bundle=%d, want 5000 from bundle 3", rc.SchemaBudgetTokens, rc.OrgBundleID)
	}
	rc = resolveWith(t, OrgPolicy{}, user, Exposure{}, nil)
	if rc.SchemaBudgetTokens != 9000 {
		t.Fatalf("budget = %d, want the user's 9000 when the org sets none", rc.SchemaBudgetTokens)
	}
}

// The field disappearing (the zero policy) hands every tool back to the
// lower layers.
func TestOrg_ZeroPolicyIsNoOpinion(t *testing.T) {
	if !(OrgPolicy{}).IsZero() {
		t.Fatal("zero policy not IsZero")
	}
	rc := resolveWith(t, OrgPolicy{}, Settings{Exposure: srv(TierOff)}, Exposure{}, []CatalogTool{sendMail})
	if got, _ := rc.Tool(sendMail.Name); got.Tier != TierOff || got.Source != LevelUser {
		t.Fatalf("send-mail = %+v, want the user's off", got)
	}
}

func TestCheckOrgPins(t *testing.T) {
	org := OrgPolicy{
		Pins: Exposure{Servers: map[string]ServerExposure{
			"outlook": {Tier: TierOff},
			"fetch":   {Tools: map[string]Tier{"fetch": TierSummary}},
		}},
		Defaults:    Exposure{Servers: map[string]ServerExposure{"github": {Tier: TierFull}}},
		HotSetExtra: []string{"kenaz__save_document"},
		BundleID:    42,
	}
	stored := Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: TierFull}}}
	type tc struct {
		name     string
		proposed Exposure
		refuse   string
	}
	cases := []tc{
		{"pinned server tier changed", srvNamed("outlook", TierSummary), "server outlook"},
		{"tool under a pinned server", Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: TierFull, Tools: map[string]Tier{"send-mail": TierFull}}}}, "tool outlook__send-mail"},
		{"pinned tool changed", Exposure{Servers: map[string]ServerExposure{"fetch": {Tools: map[string]Tier{"fetch": TierFull}}}}, "tool fetch__fetch"},
		{"hot_set_extra lowered", Exposure{Servers: map[string]ServerExposure{"kenaz": {Tools: map[string]Tier{"save_document": TierSummary}}}}, "tool kenaz__save_document"},
		{"stored value written back", stored, ""},
		{"entry dropped", Exposure{}, ""},
		{"unpinned tool of a server with only tool pins", Exposure{Servers: map[string]ServerExposure{"fetch": {Tier: TierOff, Tools: map[string]Tier{"other": TierFull}}}}, ""},
		{"org default overridden", srvNamed("github", TierOff), ""},
		{"unrelated server", srvNamed("slack", TierOff), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckOrgPins(org, stored, c.proposed)
			if c.refuse == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var pe *PinnedError
			if !errors.Is(err, ErrPinnedByOrg) || !errors.As(err, &pe) {
				t.Fatalf("err = %v, want a *PinnedError wrapping ErrPinnedByOrg", err)
			}
			if !strings.Contains(err.Error(), c.refuse) || !strings.Contains(err.Error(), "set by your organisation (fleet config bundle 42)") {
				t.Fatalf("message %q does not name %q and the pin's origin", err, c.refuse)
			}
		})
	}
}

func TestCheckOrgBudget(t *testing.T) {
	org := OrgPolicy{SchemaBudgetTokens: 5000, BundleID: 7}
	if err := CheckOrgBudget(org, 9000, 9000); err != nil {
		t.Fatalf("stored budget written back: %v", err)
	}
	err := CheckOrgBudget(org, 9000, 12000)
	if !errors.Is(err, ErrPinnedByOrg) || !strings.Contains(err.Error(), "5000 tokens") {
		t.Fatalf("budget change: err = %v", err)
	}
	if err := CheckOrgBudget(OrgPolicy{}, 9000, 12000); err != nil {
		t.Fatalf("no org budget: %v", err)
	}
}

func TestOrgPolicy_View(t *testing.T) {
	org := OrgPolicy{
		Pins:               Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: TierOff, Tools: map[string]Tier{"send-mail": TierSummary}}}},
		Defaults:           Exposure{Servers: map[string]ServerExposure{"fetch": {Tier: TierFull}}},
		SchemaBudgetTokens: 5000,
		HotSetExtra:        []string{"kenaz__save_document", "malformed"},
		BundleID:           4,
	}
	want := OrgExposure{
		Settings: []OrgSetting{
			{Server: "fetch", Tier: TierFull},
			{Server: "kenaz", Tool: "save_document", Tier: TierFull, Pinned: true, PinnedBy: PinnedByOrg},
			{Server: "outlook", Tier: TierOff, Pinned: true, PinnedBy: PinnedByOrg},
			{Server: "outlook", Tool: "send-mail", Tier: TierSummary, Pinned: true, PinnedBy: PinnedByOrg},
		},
		SchemaBudgetTokens: 5000,
		BundleID:           4,
	}
	if got := org.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("View() = %+v\nwant     %+v", got, want)
	}
	if got := (OrgPolicy{}).View(); got.Settings == nil || len(got.Settings) != 0 {
		t.Fatalf("zero View().Settings = %#v, want empty non-nil", got.Settings)
	}
}

// A hot_set_extra tool under a pinned-off entry: the pin row for that tool
// is dropped from the view (it does not apply); the hot row remains.
func TestOrgPolicy_ViewDropsPinOverriddenByHotSet(t *testing.T) {
	org := OrgPolicy{
		Pins:        Exposure{Servers: map[string]ServerExposure{"outlook": {Tier: TierOff, Tools: map[string]Tier{"send-mail": TierOff}}}},
		HotSetExtra: []string{"outlook__send-mail"},
	}
	want := []OrgSetting{
		{Server: "outlook", Tier: TierOff, Pinned: true, PinnedBy: PinnedByOrg},
		{Server: "outlook", Tool: "send-mail", Tier: TierFull, Pinned: true, PinnedBy: PinnedByOrg},
	}
	if got := org.View().Settings; !reflect.DeepEqual(got, want) {
		t.Fatalf("View().Settings = %+v\nwant %+v", got, want)
	}
}

func TestSplitName(t *testing.T) {
	for in, want := range map[string][2]string{
		"outlook__send-mail": {"outlook", "send-mail"},
		"a__b__c":            {"a", "b__c"},
	} {
		s, tl, ok := SplitName(in)
		if !ok || s != want[0] || tl != want[1] {
			t.Errorf("SplitName(%q) = %q, %q, %v", in, s, tl, ok)
		}
	}
	for _, bad := range []string{"", "outlook", "__x", "outlook__"} {
		if _, _, ok := SplitName(bad); ok {
			t.Errorf("SplitName(%q) ok", bad)
		}
	}
}

func srvNamed(server string, tier Tier) Exposure {
	return Exposure{Servers: map[string]ServerExposure{server: {Tier: tier}}}
}
