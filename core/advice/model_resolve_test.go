package advice

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestIsLayaFamilyModel_PlaceholderTable pins the PLACEHOLDER
// family-detection table (layaModelMatchTable) OQ-1 will replace with
// the real laya model naming/size(s) once the kenaz-ml team resolves it
// (spec §8). Pinned so replacing the table is a visible, single-slice
// diff caught by this test failing, not a silent behaviour change.
func TestIsLayaFamilyModel_PlaceholderTable(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"laya", true},
		{"laya-1b-instruct", true},
		{"LAYA-7B", true}, // case-insensitive
		{"qwen2.5:7b-instruct", false},
		{"claude-haiku-4.5", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isLayaFamilyModel(tc.model); got != tc.want {
			t.Errorf("isLayaFamilyModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func advisorProfile(id, kind, templateID string, models ...string) corellm.ProviderProfile {
	return corellm.ProviderProfile{ID: id, Kind: kind, TemplateID: templateID, Models: models}
}

// TestResolveAdvisorModel is the ladder table test tasks.md WP02
// requires: explicit setting wins when available, local laya is used
// when the setting is unset (or set but unavailable), and — UNLIKE
// risk.ResolveRaterModel — there is no "any available model" rung: a
// miss on both explicit setting and local laya resolves to RungNone.
func TestResolveAdvisorModel(t *testing.T) {
	t.Run("rung 1: explicit setting wins when available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b", "qwen2.5:7b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "qwen2.5:7b-instruct"}
		gotProfile, gotModel, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungExplicitSetting {
			t.Errorf("rung = %q, want %q", gotRung, RungExplicitSetting)
		}
		if gotProfile != "ollama1" || gotModel != "qwen2.5:7b-instruct" {
			t.Errorf("got (%q, %q), want (ollama1, qwen2.5:7b-instruct)", gotProfile, gotModel)
		}
		if gotUnbenchmarked {
			t.Errorf("unbenchmarked = true for an explicit deliberate choice, want false")
		}
	})

	t.Run("rung 2: unset setting falls to local laya on an ollama profile", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b-instruct"),
		}
		gotProfile, gotModel, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(AdvisorModelSetting{}, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungLocalLaya {
			t.Errorf("rung = %q, want %q", gotRung, RungLocalLaya)
		}
		if gotProfile != "ollama1" || gotModel != "laya-1b-instruct" {
			t.Errorf("got (%q, %q), want (ollama1, laya-1b-instruct)", gotProfile, gotModel)
		}
		if !gotUnbenchmarked {
			t.Errorf("unbenchmarked = false for local laya (no benchmark of record yet, OQ-3), want true")
		}
	})

	// "both availability-miss fallthroughs" (tasks.md WP02's proof list):
	// (a) an explicit setting present but unavailable on its profile falls
	// through to rung 2, and (b) an explicit setting AND no local-laya
	// candidate anywhere falls all the way through to RungNone — neither
	// miss is an error.
	t.Run("availability-miss fallthrough (a): explicit setting unavailable falls to local laya", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "a-model-that-was-removed"}
		gotProfile, gotModel, gotRung, _, ok := ResolveAdvisorModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungLocalLaya {
			t.Errorf("rung = %q, want %q (availability miss must fall through, not error)", gotRung, RungLocalLaya)
		}
		if gotProfile != "ollama1" || gotModel != "laya-1b-instruct" {
			t.Errorf("got (%q, %q), want (ollama1, laya-1b-instruct)", gotProfile, gotModel)
		}
	})

	t.Run("availability-miss fallthrough (b): no explicit setting and no local laya resolves to RungNone", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			// Not an ollama profile at all — rung 2 never even scans it.
			advisorProfile("anthropic1", "anthropic", "", "claude-haiku-4.5"),
		}
		setting := AdvisorModelSetting{ProviderID: "anthropic1", ModelID: "a-model-that-does-not-exist"}
		_, _, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(setting, profiles)
		if ok {
			t.Fatal("expected ok=false — no explicit setting available and no local laya candidate; " +
				"unlike risk.ResolveRaterModel there is no any-available fallback rung")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
		if !gotUnbenchmarked {
			t.Errorf("unbenchmarked = false for RungNone, want true")
		}
	})

	t.Run("no configured profiles at all resolves to RungNone, not ok", func(t *testing.T) {
		_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, nil)
		if ok {
			t.Fatal("expected ok=false with zero profiles")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("an ollama profile with no laya-matching model resolves to RungNone", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "qwen2.5:7b-instruct"),
		}
		_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, profiles)
		if ok {
			t.Fatal("expected ok=false — the only configured profile has no laya-family model")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("ladder order: explicit setting is preferred over local laya even when both are available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b-instruct", "qwen2.5:7b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "qwen2.5:7b-instruct"}
		_, gotModel, gotRung, _, ok := ResolveAdvisorModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungExplicitSetting {
			t.Errorf("rung = %q, want %q", gotRung, RungExplicitSetting)
		}
		if gotModel != "qwen2.5:7b-instruct" {
			t.Errorf("model = %q, want qwen2.5:7b-instruct", gotModel)
		}
	})

	t.Run("fleet rung is compiled but unreachable", func(t *testing.T) {
		if fleetRungEnabled {
			t.Fatal("fleetRungEnabled must stay false pending OQ-2 — flip only when tasks.md WP09 wires the real fleet contract")
		}
	})
}

// TestResolveAdvisorModel_DoesNotFallBackToFirstProfile is the ladder's
// own profiles[0]-regression case (tasks.md WP02's proof list), mirrored
// from risk.TestResolveRaterModel_PrefersProviderDefaultOverFirstProfile
// but proving a STRONGER property for the advisor: where the risk
// rater's ladder still has a generic "any available" rung c to fall back
// on, the advisor ladder has none. profiles[0] here is a big reasoning
// model with an AVAILABLE model — exactly the shape that would have
// silently resolved via the pre-01PMRA01 profiles[0].Model defect this
// mission's spec explicitly cites as the reason the advisor ladder has
// no such rung at all. The correct answer is RungNone, not profiles[0].
func TestResolveAdvisorModel_DoesNotFallBackToFirstProfile(t *testing.T) {
	profiles := []corellm.ProviderProfile{
		advisorProfile("reasoning-chat-profile", "anthropic", "", "claude-opus-4-1"),
		advisorProfile("ollama1", "ollama", "", "qwen2.5:7b-instruct"), // no laya-family model either
	}
	_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, profiles)
	if ok {
		t.Fatal("expected ok=false — neither profile has an explicit setting hit or a laya-family model; " +
			"the resolver must NOT fall back to profiles[0] (claude-opus-4-1) the way the pre-01PMRA01 " +
			"risk rater used to")
	}
	if gotRung != RungNone {
		t.Errorf("rung = %q, want %q", gotRung, RungNone)
	}
}
