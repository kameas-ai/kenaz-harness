package llm_test

import (
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/bedrock"
	"github.com/kameas-ai/kenaz-harness/core/llm/gemini"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
)

// TestStructuredOutputAdapter_ExactlyFourImplementors is
// structured-output-is-reachable-01PMZE14 WP08's AC-012, disposition
// (b): the interface's doc comment was rewritten to describe what is
// true (a compile-time marker with no dispatcher, spec D-4) rather than
// promising a prompt-engineering fallback nothing selects. This test
// pins the implementor set that claim describes: exactly the four
// adapters that predate this mission (anthropic, openai, openrouter,
// bedrock).
//
// Must go red if a fifth `var _ llm.StructuredOutputAdapter` assertion
// is added anywhere — spec D-4 calls that "the mission adding to the
// ledger it is closing." gemini gained real structured-output support
// in this same mission (WP04) and deliberately does NOT implement this
// interface, because implementing it would add zero behaviour with no
// dispatcher to route through.
func TestStructuredOutputAdapter_ExactlyFourImplementors(t *testing.T) {
	implementors := map[string]any{
		"anthropic":  (*anthropic.Adapter)(nil),
		"openai":     (*openai.Adapter)(nil),
		"openrouter": (*openrouter.Adapter)(nil),
		"bedrock":    (*bedrock.Adapter)(nil),
	}
	for name, im := range implementors {
		if _, ok := im.(llm.StructuredOutputAdapter); !ok {
			t.Errorf("%s.Adapter no longer implements llm.StructuredOutputAdapter — update this test or the interface's doc comment", name)
		}
	}

	nonImplementors := map[string]any{
		"gemini": (*gemini.Adapter)(nil),
	}
	for name, im := range nonImplementors {
		if _, ok := im.(llm.StructuredOutputAdapter); ok {
			t.Errorf("%s.Adapter implements llm.StructuredOutputAdapter — spec D-4: a fifth assertion with no dispatcher adds zero behaviour and is the class this mission exists to end", name)
		}
	}
}
