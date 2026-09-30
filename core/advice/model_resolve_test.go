package advice

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeSidecarProbe is the WP12 amendment's test double for SidecarProbe
// — a fake Manager stand-in, exactly the shape core/mlsidecar.Manager's
// production Healthy()/Identity() pair implements, but with zero
// dependency on that package (this package predates core/mlsidecar and
// must not gain a hard import on it — SidecarProbe's own doc comment).
type fakeSidecarProbe struct {
	healthy  bool
	identity string
}

func (f fakeSidecarProbe) Healthy() bool    { return f.healthy }
func (f fakeSidecarProbe) Identity() string { return f.identity }

func advisorProfile(id, kind, templateID string, models ...string) corellm.ProviderProfile {
	return corellm.ProviderProfile{ID: id, Kind: kind, TemplateID: templateID, Models: models}
}

// TestResolveAdvisorModel is the ladder table test tasks.md WP02
// requires, updated by the WP12 amendment: explicit setting wins when
// available, the managed ML sidecar is used when the setting is unset
// (or set but unavailable) and the sidecar is healthy, and — UNLIKE
// risk.ResolveRaterModel — there is no "any available model" rung: a
// miss on both explicit setting and the sidecar resolves to RungNone.
func TestResolveAdvisorModel(t *testing.T) {
	t.Run("rung 1: explicit setting wins when available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b", "qwen2.5:7b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "qwen2.5:7b-instruct"}
		gotProfile, gotModel, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(setting, profiles, fakeSidecarProbe{healthy: true, identity: "kenaz-ml-sidecar@1.0.0"})
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

	t.Run("rung 2: unset setting falls to a healthy sidecar", func(t *testing.T) {
		gotProfile, gotModel, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(AdvisorModelSetting{}, nil, fakeSidecarProbe{healthy: true, identity: "kenaz-ml-sidecar@1.0.0"})
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungLocalLaya {
			t.Errorf("rung = %q, want %q", gotRung, RungLocalLaya)
		}
		if gotProfile != "" {
			t.Errorf("profileID = %q, want empty — the sidecar is not a corellm.ProviderProfile", gotProfile)
		}
		if gotModel != "kenaz-ml-sidecar@1.0.0" {
			t.Errorf("model = %q, want the sidecar's reported identity", gotModel)
		}
		if !gotUnbenchmarked {
			t.Errorf("unbenchmarked = false for the sidecar rung (no benchmark of record yet, OQ-3), want true")
		}
	})

	// "both availability-miss fallthroughs" (tasks.md WP02's proof list),
	// updated for the WP12 amendment: (a) an explicit setting present but
	// unavailable on its profile falls through to a healthy sidecar, and
	// (b) an explicit setting AND an unhealthy/absent sidecar falls all
	// the way through to RungNone — neither miss is an error.
	t.Run("availability-miss fallthrough (a): explicit setting unavailable falls to the sidecar", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "a-model-that-was-removed"}
		gotProfile, gotModel, gotRung, _, ok := ResolveAdvisorModel(setting, profiles, fakeSidecarProbe{healthy: true, identity: "kenaz-ml-sidecar@2.0.0"})
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungLocalLaya {
			t.Errorf("rung = %q, want %q (availability miss must fall through, not error)", gotRung, RungLocalLaya)
		}
		if gotProfile != "" || gotModel != "kenaz-ml-sidecar@2.0.0" {
			t.Errorf("got (%q, %q), want (\"\", kenaz-ml-sidecar@2.0.0)", gotProfile, gotModel)
		}
	})

	t.Run("availability-miss fallthrough (b): no explicit setting and no healthy sidecar resolves to RungNone", func(t *testing.T) {
		// Not an ollama profile at all, and irrelevant regardless — rung 2
		// no longer scans provider profiles.
		profiles := []corellm.ProviderProfile{
			advisorProfile("anthropic1", "anthropic", "", "claude-haiku-4.5"),
		}
		setting := AdvisorModelSetting{ProviderID: "anthropic1", ModelID: "a-model-that-does-not-exist"}
		_, _, gotRung, gotUnbenchmarked, ok := ResolveAdvisorModel(setting, profiles, fakeSidecarProbe{healthy: false})
		if ok {
			t.Fatal("expected ok=false — no explicit setting available and no healthy sidecar; " +
				"unlike risk.ResolveRaterModel there is no any-available fallback rung")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
		if !gotUnbenchmarked {
			t.Errorf("unbenchmarked = false for RungNone, want true")
		}
	})

	t.Run("no configured profiles and a nil sidecar resolves to RungNone, not ok", func(t *testing.T) {
		_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, nil, nil)
		if ok {
			t.Fatal("expected ok=false with zero profiles and a nil sidecar")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("an unhealthy sidecar resolves to RungNone", func(t *testing.T) {
		_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, nil, fakeSidecarProbe{healthy: false})
		if ok {
			t.Fatal("expected ok=false — the sidecar probe reports unhealthy")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("ladder order: explicit setting is preferred over the sidecar even when both are available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			advisorProfile("ollama1", "ollama", "", "laya-1b-instruct", "qwen2.5:7b-instruct"),
		}
		setting := AdvisorModelSetting{ProviderID: "ollama1", ModelID: "qwen2.5:7b-instruct"}
		_, gotModel, gotRung, _, ok := ResolveAdvisorModel(setting, profiles, fakeSidecarProbe{healthy: true, identity: "kenaz-ml-sidecar@1.0.0"})
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
// no such rung at all. The correct answer is RungNone, not profiles[0],
// even with an unhealthy sidecar in play.
func TestResolveAdvisorModel_DoesNotFallBackToFirstProfile(t *testing.T) {
	profiles := []corellm.ProviderProfile{
		advisorProfile("reasoning-chat-profile", "anthropic", "", "claude-opus-4-1"),
		advisorProfile("ollama1", "ollama", "", "qwen2.5:7b-instruct"),
	}
	_, _, gotRung, _, ok := ResolveAdvisorModel(AdvisorModelSetting{}, profiles, fakeSidecarProbe{healthy: false})
	if ok {
		t.Fatal("expected ok=false — neither profile has an explicit setting hit and the sidecar is unhealthy; " +
			"the resolver must NOT fall back to profiles[0] (claude-opus-4-1) the way the pre-01PMRA01 " +
			"risk rater used to")
	}
	if gotRung != RungNone {
		t.Errorf("rung = %q, want %q", gotRung, RungNone)
	}
}
