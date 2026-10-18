package advice

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func validToyKind(id string) AdviceKind {
	return AdviceKind{
		ID:            id,
		PromptVersion: "v1",
		SafetyClass:   SafetyReversible,
		Extract:       func(input any) (Features, error) { return input, nil },
		RenderPrompt:  func(f Features) (string, string) { return "sys", "user" },
	}
}

func TestAdviceKind_ValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(k AdviceKind) AdviceKind
		wantErr string
	}{
		{"missing id", func(k AdviceKind) AdviceKind { k.ID = ""; return k }, "id"},
		{"missing prompt_version", func(k AdviceKind) AdviceKind { k.PromptVersion = ""; return k }, "prompt_version"},
		{"missing safety class", func(k AdviceKind) AdviceKind { k.SafetyClass = ""; return k }, "safety_class"},
		{"invalid safety class", func(k AdviceKind) AdviceKind { k.SafetyClass = "made_up"; return k }, "safety_class"},
		{"missing extractor", func(k AdviceKind) AdviceKind { k.Extract = nil; return k }, "extractor"},
		{"missing prompt renderer", func(k AdviceKind) AdviceKind { k.RenderPrompt = nil; return k }, "prompt_renderer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := tc.mutate(validToyKind("validate_probe_" + tc.name))
			err := Register(k)
			if err == nil {
				unregisterForTest(k.ID)
				t.Fatalf("Register(%+v) = nil, want an error naming %q", k, tc.wantErr)
			}
			if !containsSubstr(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestAdviceKind_RegisterGetAllCount(t *testing.T) {
	before := Count()

	k1 := validToyKind("registry_probe_a")
	k2 := validToyKind("registry_probe_b")
	if err := Register(k1); err != nil {
		t.Fatalf("Register(k1): %v", err)
	}
	defer unregisterForTest(k1.ID)
	if err := Register(k2); err != nil {
		t.Fatalf("Register(k2): %v", err)
	}
	defer unregisterForTest(k2.ID)

	if Count() != before+2 {
		t.Fatalf("Count() = %d, want %d", Count(), before+2)
	}

	got, ok := Get(k1.ID)
	if !ok || got.ID != k1.ID {
		t.Fatalf("Get(%q) = (%+v, %v), want a hit", k1.ID, got, ok)
	}

	all := All()
	found := 0
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatalf("All() is not sorted by ID: %q > %q", all[i-1].ID, all[i].ID)
		}
	}
	for _, k := range all {
		if k.ID == k1.ID || k.ID == k2.ID {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("All() contains %d of the 2 registered probes, want 2", found)
	}
}

func TestAdviceKind_RegisterDuplicateErrors(t *testing.T) {
	k := validToyKind("duplicate_probe")
	if err := Register(k); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	defer unregisterForTest(k.ID)

	err := Register(k)
	if err == nil {
		t.Fatal("second Register with the same id = nil, want ErrKindAlreadyRegistered")
	}
	if !errors.Is(err, ErrKindAlreadyRegistered) {
		t.Errorf("errors.Is(err, ErrKindAlreadyRegistered) = false; err = %v", err)
	}
}

func TestAdviceKind_MustRegisterPanicsOnInvalidKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustRegister with a missing safety class did not panic")
		}
	}()
	MustRegister(AdviceKind{ID: "panic_probe", PromptVersion: "v1"})
}

// TestRequireCanAutoAct is spec §3's compile-visible enforcement point:
// a suggest-only kind must never pass, a reversible kind must always
// pass, and an unregistered id must error rather than silently allow.
func TestRequireCanAutoAct(t *testing.T) {
	reversible := validToyKind("autoact_probe_reversible")
	if err := Register(reversible); err != nil {
		t.Fatalf("Register(reversible): %v", err)
	}
	defer unregisterForTest(reversible.ID)

	suggestOnly := validToyKind("autoact_probe_suggest_only")
	suggestOnly.SafetyClass = SafetySuggestOnly
	if err := Register(suggestOnly); err != nil {
		t.Fatalf("Register(suggestOnly): %v", err)
	}
	defer unregisterForTest(suggestOnly.ID)

	if err := RequireCanAutoAct(reversible.ID); err != nil {
		t.Errorf("RequireCanAutoAct(reversible) = %v, want nil", err)
	}
	if err := RequireCanAutoAct(suggestOnly.ID); !errors.Is(err, ErrSuggestOnlyCannotAutoAct) {
		t.Errorf("RequireCanAutoAct(suggestOnly) = %v, want ErrSuggestOnlyCannotAutoAct", err)
	}
	if err := RequireCanAutoAct("does_not_exist"); !errors.Is(err, ErrKindNotRegistered) {
		t.Errorf("RequireCanAutoAct(unregistered) = %v, want ErrKindNotRegistered", err)
	}
}

// TestAC08_FourthKindRequiresOnlyRegistryAndExtractor is spec.md AC-08's
// direct proof: "adding a toy fourth advice kind in a test registers,
// resolves, renders, and captures with zero changes outside its own
// registry entry and its extractor." Everything below this comment is
// new (a toy kind, its extractor, its renderer); every mechanism it
// drives — Register, Get, FakeAdvisor.Recommend (which internally calls
// FeaturesHash for caching and Dismiss for the capture-suppression
// proof) — is pre-existing seam code this test does not touch, matching
// what branch_now/compact_now/escalate_model (WP04-06) will each do in
// their own packages.
func TestAC08_FourthKindRequiresOnlyRegistryAndExtractor(t *testing.T) {
	type toyFeatures struct {
		Signal string `json:"signal"`
	}
	toyExtract := func(input any) (Features, error) {
		s, ok := input.(string)
		if !ok {
			return nil, fmt.Errorf("ac08_toy_kind: want string input, got %T", input)
		}
		return toyFeatures{Signal: s}, nil
	}
	toyRender := func(f Features) (string, string) {
		tf, ok := f.(toyFeatures)
		if !ok {
			return "", ""
		}
		return "ac08 toy system prompt", "ac08 toy user prompt: " + tf.Signal
	}

	kind := AdviceKind{
		ID:            "ac08_toy_kind",
		PromptVersion: "v1",
		SafetyClass:   SafetyReversible,
		Extract:       toyExtract,
		RenderPrompt:  toyRender,
	}

	// Registers.
	if err := Register(kind); err != nil {
		t.Fatalf("Register: %v", err)
	}
	defer unregisterForTest(kind.ID)

	got, ok := Get(kind.ID)
	if !ok {
		t.Fatal("Get after Register: not found")
	}

	// Resolves + renders: the extractor + renderer this test defined,
	// invoked through the generic AdviceKind fields, with no per-kind
	// special-casing anywhere in the seam.
	features, err := got.Extract("hello-world")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	system, user := got.RenderPrompt(features)
	if system == "" || user == "" {
		t.Fatalf("RenderPrompt produced an empty prompt half: system=%q user=%q", system, user)
	}

	// Captures: FakeAdvisor drives the exact same Recommend/Dismiss
	// mechanics LLMAdvisor does — cache keyed on (session, kind,
	// featuresHash, promptVersion), Dismiss suppressing a future
	// identical ask — with zero kind-specific code.
	fa := NewFakeAdvisor().ScriptFor(kind.ID, Recommendation{
		Decision: true, Confidence: 88, KindID: kind.ID, PromptVersion: kind.PromptVersion,
	})
	sess := SessionContext{SessionID: "ac08-sess"}

	rec, err := fa.Recommend(context.Background(), got, features, sess)
	if err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if !rec.Decision || rec.Confidence != 88 {
		t.Fatalf("got %+v, want Decision=true Confidence=88", rec)
	}
	if fa.CallCount() != 1 {
		t.Fatalf("CallCount = %d, want 1", fa.CallCount())
	}

	fa.Dismiss(sess, got, features)
	if _, err := fa.Recommend(context.Background(), got, features, sess); !errors.Is(err, ErrNoAdvice) {
		t.Fatalf("Recommend after Dismiss: err = %v, want ErrNoAdvice", err)
	}
	if fa.CallCount() != 1 {
		t.Fatalf("CallCount after dismiss+re-ask = %d, want 1 (AC-03: no re-show, no re-call)", fa.CallCount())
	}
}

func containsSubstr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOfSubstr(s, sub) >= 0)
}

func indexOfSubstr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
