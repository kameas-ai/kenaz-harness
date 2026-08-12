package chat

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// autonomy-knobs-live-01PMAG02 WP06.
//
// recapStyle was resolved through the three-layer inheritance chain,
// given a source trace, rendered in the Settings panel, and consumed by
// nothing. This wires it as a system-prompt layer and pins that each
// mode produces a distinct instruction.

func TestBuildRecapBlock_EachModeIsDistinct(t *testing.T) {
	t.Parallel()
	seen := map[string]autonomy.RecapMode{}
	for _, mode := range []autonomy.RecapMode{autonomy.RecapNone, autonomy.RecapBrief, autonomy.RecapFull} {
		a := &LLMProviderAdapter{recapStyle: func() autonomy.RecapMode { return mode }}
		got := a.buildRecapBlock()
		if strings.TrimSpace(got) == "" {
			t.Errorf("mode %q produced an empty block — the knob would stay inert", mode)
			continue
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("mode %q produced the same text as %q — modes must be distinguishable", mode, prev)
		}
		seen[got] = mode
	}
	if len(seen) != 3 {
		t.Fatalf("distinct blocks = %d, want 3", len(seen))
	}
}

// RecapNone must NOT be empty: "say nothing extra" is a real instruction
// and differs from having no opinion. Only a nil resolver yields no
// layer.
func TestBuildRecapBlock_NoneIsAnInstructionNotAnAbsence(t *testing.T) {
	t.Parallel()
	a := &LLMProviderAdapter{recapStyle: func() autonomy.RecapMode { return autonomy.RecapNone }}
	if got := a.buildRecapBlock(); strings.TrimSpace(got) == "" {
		t.Fatal("RecapNone rendered empty — cannot be distinguished from an unwired knob")
	}
}

// A nil resolver (no autonomy provider) appends no layer, which is what
// keeps a harness with no autonomy wiring byte-identical to pre-01PMAG02.
func TestBuildRecapBlock_NilResolverAppendsNothing(t *testing.T) {
	t.Parallel()
	if got := (&LLMProviderAdapter{}).buildRecapBlock(); got != "" {
		t.Fatalf("nil resolver produced %q, want empty", got)
	}
	var nilAdapter *LLMProviderAdapter
	if got := nilAdapter.buildRecapBlock(); got != "" {
		t.Fatalf("nil adapter produced %q, want empty", got)
	}
}

// An unrecognised mode degrades to no layer rather than a wrong one.
func TestBuildRecapBlock_UnknownModeAppendsNothing(t *testing.T) {
	t.Parallel()
	a := &LLMProviderAdapter{recapStyle: func() autonomy.RecapMode { return autonomy.RecapMode("bogus") }}
	if got := a.buildRecapBlock(); got != "" {
		t.Fatalf("unknown mode produced %q, want empty", got)
	}
}
