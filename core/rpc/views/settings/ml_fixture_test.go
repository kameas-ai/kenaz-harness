package settings

// ml_fixture_test.go — legal (kameas-ai-11, 2026-10-16): the local fallback
// notice's exclusions paragraph is Fleet's, not the harness's own. Pinned
// against kenaz-fleet's shared fixture, mirrored byte-identical in
// testdata/ml_notice_text.json (provenance: ml_notice_text.PROVENANCE.md).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type mlNoticeFixtureCase struct {
	Revision       int    `json:"revision"`
	Org            string `json:"org"`
	RetentionDays  int    `json:"retention_days"`
	Policy         string `json:"policy"`
	IncludeAIUsage bool   `json:"include_ai_usage"`
	Exclusions     struct {
		Paths          []string `json:"paths"`
		Commands       []string `json:"commands"`
		ExcludeBrowser bool     `json:"exclude_browser"`
	} `json:"exclusions"`
	LegacyNotes []string `json:"legacy_exclusion_notes"`
	Text        string   `json:"text"`
}

func loadMLNoticeFixture(t *testing.T) []mlNoticeFixtureCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/ml_notice_text.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []mlNoticeFixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 9 {
		t.Fatalf("fixture has %d cases, want >= 9", len(f.Cases))
	}
	return f.Cases
}

// Every fixture case: MLExclusionsSummary is byte-identical to the
// exclusions paragraph Fleet appended ("" when nothing is excluded).
func TestMLExclusionsSummary_MatchesFleetFixture(t *testing.T) {
	withLegacy := 0
	for i, c := range loadMLNoticeFixture(t) {
		got := MLExclusionsSummary(c.Exclusions.Paths, c.Exclusions.Commands, c.Exclusions.ExcludeBrowser, c.LegacyNotes)
		paras := strings.Split(c.Text, "\n\n")
		last := paras[len(paras)-1]
		if got == "" {
			// Nothing excluded: Fleet appended no paragraph, so the text
			// ends with the policy paragraph.
			if last != mlNoticeMemberChoice && last != mlNoticeRequired {
				t.Errorf("case %d: empty summary but the fixture ends with %q", i, last)
			}
			continue
		}
		if got != last {
			t.Errorf("case %d:\n got %q\nwant %q", i, got, last)
		}
		if len(c.LegacyNotes) > 0 {
			withLegacy++
		}
	}
	if withLegacy < 2 {
		t.Errorf("fixture cases with legacy notes = %d, want >= 2", withLegacy)
	}
}

// Every revision-1 case (the only revision this harness renders locally):
// the fallback notice, exclusions included, is Fleet's text in full (the
// heading is the title, not in Fleet's text).
func TestRenderMLNotice_Rev1MatchesFleetFixture(t *testing.T) {
	n1 := 0
	for i, c := range loadMLNoticeFixture(t) {
		if c.Revision != localNoticeTextRevision {
			continue
		}
		n1++
		n := RenderMLNotice(c.Org, c.RetentionDays, c.Policy, false)
		n.ExclusionsText = MLExclusionsSummary(c.Exclusions.Paths, c.Exclusions.Commands, c.Exclusions.ExcludeBrowser, c.LegacyNotes)
		got := strings.TrimPrefix(n.PlainText(), n.Heading+"\n\n")
		if got != c.Text {
			t.Errorf("case %d (policy %s):\n got %q\nwant %q", i, c.Policy, got, c.Text)
		}
	}
	if n1 == 0 {
		t.Fatal("no revision-1 cases in the fixture")
	}
}

// The status view carries the same summary (shown outside a notice) and
// the fallback notice carries it as ExclusionsText.
func TestFleetMLStatus_ExclusionsSummary(t *testing.T) {
	r := newMLRig(t, true)
	body := mlBody(true, "on", false, true, false, 4, "", 90, false)
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	m["exclusions"] = map[string]any{"paths": []string{"legal/**"}, "commands": []string{}, "exclude_browser": false}
	m["legacy_exclusion_notes"] = []string{"no HR team", "no legal"}
	b, _ := json.Marshal(m)
	r.fleet.set(func(f *mlFakeFleet) { f.state = string(b) })
	v, err := r.api.FleetMLStatus(t.Context())
	if err != nil || v.Notice == nil {
		t.Fatalf("FleetMLStatus: %+v, %v", v, err)
	}
	want := "Excluded from upload: file paths matching: legal/**. Your organization also listed these exclusions " +
		"in its own words. Fleet can't apply them automatically, so matching activity may still be uploaded: " +
		"no HR team; no legal."
	if v.ExclusionsSummary != want || v.Notice.ExclusionsText != want || !strings.HasSuffix(v.NoticeText, "\n\n"+want) {
		t.Errorf("summary = %q / %q / text %q, want %q", v.ExclusionsSummary, v.Notice.ExclusionsText, v.NoticeText, want)
	}
}

// The exclusions template this harness mirrors is the one Fleet's fixture
// declares (kenaz-fleet 84338a0e, top-level exclusions_template).
func TestLocalExclusionsTemplate_MatchesFleetFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/ml_notice_text.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		ExclusionsTemplate *int `json:"exclusions_template"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.ExclusionsTemplate == nil {
		t.Fatal("fixture has no top-level exclusions_template")
	}
	if *f.ExclusionsTemplate != localExclusionsTemplate {
		t.Errorf("localExclusionsTemplate = %d, fixture exclusions_template = %d", localExclusionsTemplate, *f.ExclusionsTemplate)
	}
}
