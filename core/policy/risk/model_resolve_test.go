package risk

import (
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestProviderDefaultModel pins the exact per-provider-kind default ids
// from owner ruling 3 (2026-09-15). A table test rather than a handful of
// spot checks so an accidental edit to ProviderDefaultModel's switch
// (e.g. someone "fixing" the openrouter default back to
// deepseek-v4-flash, or dropping the deepseek custom-openai template
// special-case) fails loudly here instead of silently drifting from the
// benchmark of record.
func TestProviderDefaultModel(t *testing.T) {
	cases := []struct {
		name              string
		kind              string
		templateID        string
		wantModel         string
		wantUnbenchmarked bool
	}{
		{"openrouter", "openrouter", "", "qwen/qwen-2.5-7b-instruct", false},
		{"openai", "openai", "", "gpt-4o-mini", false},
		{"anthropic", "anthropic", "", "claude-haiku-4.5", false},
		{"deepseek via custom-openai template", "custom-openai", "deepseek", "deepseek-chat", false},
		{"other custom-openai template is unbenchmarked", "custom-openai", "groq", "gpt-4o-mini", true},
		{"custom-openai with no template id is unbenchmarked", "custom-openai", "", "gpt-4o-mini", true},
		{"bedrock is unbenchmarked", "bedrock", "", "anthropic.claude-3-5-haiku-20241022-v1:0", true},
		{"gemini is unbenchmarked", "gemini", "", "gemini-1.5-flash", true},
		{"ollama is unbenchmarked", "ollama", "", "qwen2.5:7b-instruct", true},
		{"unknown kind has no default", "some-future-kind", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, unbenchmarked := ProviderDefaultModel(tc.kind, tc.templateID)
			if model != tc.wantModel {
				t.Errorf("model = %q, want %q", model, tc.wantModel)
			}
			if unbenchmarked != tc.wantUnbenchmarked {
				t.Errorf("unbenchmarked = %v, want %v", unbenchmarked, tc.wantUnbenchmarked)
			}
		})
	}
}

func profile(id, kind, templateID string, models ...string) corellm.ProviderProfile {
	return corellm.ProviderProfile{ID: id, Kind: kind, TemplateID: templateID, Models: models}
}

// TestResolveRaterModel is the ladder table test the mission's Proofs
// section requires: explicit setting wins when available, the
// per-provider default is used when the setting is unset (or set but
// unavailable), and an availability miss on BOTH of those falls through
// to any available model. Each subtest isolates exactly one rung so a
// regression in the ladder's ordering (or a rung silently swallowed)
// fails on the specific case that exercises it — "break rung b's table
// -> test red" per the mission brief.
func TestResolveRaterModel(t *testing.T) {
	t.Run("rung a: explicit setting wins when available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct", "anthropic/claude-haiku-4.5"),
		}
		setting := RaterModelSetting{ProviderID: "or1", ModelID: "anthropic/claude-haiku-4.5"}
		gotProfile, gotModel, gotRung, ok := ResolveRaterModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungExplicitSetting {
			t.Errorf("rung = %q, want %q", gotRung, RungExplicitSetting)
		}
		if gotProfile != "or1" || gotModel != "anthropic/claude-haiku-4.5" {
			t.Errorf("got (%q, %q), want (or1, anthropic/claude-haiku-4.5)", gotProfile, gotModel)
		}
	})

	t.Run("rung b: unset setting falls to the provider default", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct", "some-other-model"),
		}
		gotProfile, gotModel, gotRung, ok := ResolveRaterModel(RaterModelSetting{}, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungProviderDefault {
			t.Errorf("rung = %q, want %q", gotRung, RungProviderDefault)
		}
		if gotProfile != "or1" || gotModel != "qwen/qwen-2.5-7b-instruct" {
			t.Errorf("got (%q, %q), want (or1, qwen/qwen-2.5-7b-instruct)", gotProfile, gotModel)
		}
	})

	t.Run("rung b: setting present but its profile is gone falls to the provider default", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct"),
		}
		// Setting points at a profile ID that no longer exists (deleted
		// provider) — this must NOT surface as an error, it must fall
		// through exactly like an unset setting.
		setting := RaterModelSetting{ProviderID: "deleted-profile", ModelID: "whatever"}
		_, gotModel, gotRung, ok := ResolveRaterModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungProviderDefault {
			t.Errorf("rung = %q, want %q", gotRung, RungProviderDefault)
		}
		if gotModel != "qwen/qwen-2.5-7b-instruct" {
			t.Errorf("model = %q, want qwen/qwen-2.5-7b-instruct", gotModel)
		}
	})

	t.Run("availability miss on explicit setting falls through to the provider default", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			// The explicit setting names a model no longer in this
			// profile's authorised list (e.g. removed upstream).
			profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct"),
		}
		setting := RaterModelSetting{ProviderID: "or1", ModelID: "a-model-that-was-removed"}
		_, gotModel, gotRung, ok := ResolveRaterModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungProviderDefault {
			t.Errorf("rung = %q, want %q (availability miss must fall through, not error)", gotRung, RungProviderDefault)
		}
		if gotModel != "qwen/qwen-2.5-7b-instruct" {
			t.Errorf("model = %q, want qwen/qwen-2.5-7b-instruct", gotModel)
		}
	})

	t.Run("rung c: no explicit setting and no provider default available falls to any available model", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			// "unknown-kind" has no ProviderDefaultModel entry at all, so
			// rung b can never fire for it.
			profile("custom1", "unknown-kind", "", "some-oddball-model"),
		}
		gotProfile, gotModel, gotRung, ok := ResolveRaterModel(RaterModelSetting{}, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungAnyAvailable {
			t.Errorf("rung = %q, want %q", gotRung, RungAnyAvailable)
		}
		if gotProfile != "custom1" || gotModel != "some-oddball-model" {
			t.Errorf("got (%q, %q), want (custom1, some-oddball-model)", gotProfile, gotModel)
		}
	})

	t.Run("availability miss on BOTH explicit setting and provider default falls through to any available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			// openrouter's default (qwen) is not in THIS profile's list,
			// and neither is the explicit setting's model.
			profile("or1", "openrouter", "", "some-custom-openrouter-model"),
		}
		setting := RaterModelSetting{ProviderID: "or1", ModelID: "a-model-that-does-not-exist"}
		gotProfile, gotModel, gotRung, ok := ResolveRaterModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungAnyAvailable {
			t.Errorf("rung = %q, want %q", gotRung, RungAnyAvailable)
		}
		if gotProfile != "or1" || gotModel != "some-custom-openrouter-model" {
			t.Errorf("got (%q, %q), want (or1, some-custom-openrouter-model)", gotProfile, gotModel)
		}
	})

	t.Run("no configured profiles at all resolves to RungNone, not ok", func(t *testing.T) {
		_, _, gotRung, ok := ResolveRaterModel(RaterModelSetting{}, nil)
		if ok {
			t.Fatal("expected ok=false with zero profiles")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("configured profiles with zero available models each resolves to RungNone, not ok", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			{ID: "empty1", Kind: "openrouter"}, // no Model, no Models
		}
		_, _, gotRung, ok := ResolveRaterModel(RaterModelSetting{}, profiles)
		if ok {
			t.Fatal("expected ok=false when no profile has any available model")
		}
		if gotRung != RungNone {
			t.Errorf("rung = %q, want %q", gotRung, RungNone)
		}
	})

	t.Run("ladder order: explicit setting is preferred over provider default even when both are available", func(t *testing.T) {
		profiles := []corellm.ProviderProfile{
			profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct", "openai/gpt-4o-mini"),
		}
		// The provider default (qwen) IS available here too, but the
		// explicit setting must win — this is what distinguishes rung a
		// from rung b and would go undetected by a test that only ever
		// left the setting unset.
		setting := RaterModelSetting{ProviderID: "or1", ModelID: "openai/gpt-4o-mini"}
		_, gotModel, gotRung, ok := ResolveRaterModel(setting, profiles)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if gotRung != RungExplicitSetting {
			t.Errorf("rung = %q, want %q", gotRung, RungExplicitSetting)
		}
		if gotModel != "openai/gpt-4o-mini" {
			t.Errorf("model = %q, want openai/gpt-4o-mini", gotModel)
		}
	})
}

// TestResolveRaterModel_FirstProfileWins pins the WP05-defect regression
// case directly: a multi-profile setup where profiles[0] is a big
// reasoning model (the historical bug: the resolver used to just return
// profiles[0].Model unconditionally) and a LATER profile is the
// openrouter default. The ladder must still find and prefer the provider
// default over blindly returning profiles[0].
func TestResolveRaterModel_PrefersProviderDefaultOverFirstProfile(t *testing.T) {
	profiles := []corellm.ProviderProfile{
		profile("reasoning-chat-profile", "anthropic", "", "claude-opus-4-1"),
		profile("or1", "openrouter", "", "qwen/qwen-2.5-7b-instruct"),
	}
	gotProfile, gotModel, gotRung, ok := ResolveRaterModel(RaterModelSetting{}, profiles)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// anthropic's own provider default (claude-haiku-4.5) is not present
	// on the first profile's Models list, so rung b keeps scanning and
	// finds openrouter's qwen default on the second profile — this is
	// the fix: it must NOT stop at profiles[0]'s claude-opus-4-1.
	if gotRung != RungProviderDefault {
		t.Errorf("rung = %q, want %q", gotRung, RungProviderDefault)
	}
	if gotProfile != "or1" || gotModel != "qwen/qwen-2.5-7b-instruct" {
		t.Errorf("got (%q, %q), want (or1, qwen/qwen-2.5-7b-instruct) — resolver must not default to profiles[0]", gotProfile, gotModel)
	}
}
