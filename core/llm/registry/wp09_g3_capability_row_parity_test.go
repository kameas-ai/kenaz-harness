package registry

// WP09 (structured-output-is-reachable-01PMZE14, UNIT-6, G-3): "a
// capability row cannot advertise what its adapter cannot serve."
//
// A Go test — not a shell script (tasks.md UNIT-6) — asserting that
// every registered adapter kind's structured_output capability row
// (core/llm/capabilities/data/*.yaml, read ONLY through
// capabilities.LoadDefault() -> Catalog.Describe, never as a raw
// file — spec §8 rule 2) agrees with what that adapter's OWN
// wire-encoding code actually does — never a struct field read
// (spec §8 rule 3):
//
//   - structured_output: false ⟹ capabilities.Gate.Check refuses a
//     Mode:"json_schema" request BEFORE any wire body is built. This
//     is the only sound assertion for "false": every encoder this
//     test can reach directly (ApplyResponseFormat, openaiwire's
//     shared body builder) has NO internal capability awareness of
//     its own — it mechanically emits response_format whenever asked,
//     regardless of what any row says. Gate.Check, wired into
//     Registry.Stream ahead of every adapter call, is the ONLY place
//     in the real pipeline a "false" row's refusal promise is kept.
//   - structured_output: true ⟹ Gate.Check allows the request AND
//     the adapter's own encoder — called directly, the same function
//     production reaches once the gate has let the request through —
//     actually carries the schema on the wire. This is the class the
//     mission's own finding names: "gemini's rows are honest only
//     because its adapter does nothing" (tasks.md UNIT-6) — a row
//     flipped true with no matching encoder arm is exactly what this
//     half catches.
//
// bedrock is a documented, deliberate exception: see the "true" case
// comment below and core/llm/bedrock/wp09_g3_row_parity_test.go,
// this file's twin one package over.
//
// Kind set mirrors this package's own registration block
// (registry.go:140-169) as of this WP: anthropic, bedrock, openai,
// openrouter unconditional; azure-openai/gemini/custom-openai/ollama
// conditional on their own env flags. A future adapter registration
// must add a case here too — the same "two lists must widen together"
// hazard tasks.md already documents for
// registry_completeness_test.go's inScopeAdapters/adapterDirs.
//
// This file intentionally never imports azure/custom/ollama's adapter
// packages or calls their New() — all three route their ENTIRE
// request body through openaiwire.BuildRequestBody (azure/adapter.go
// :386, custom/adapter.go:287, ollama/adapter.go:170), so driving that
// shared function directly IS their real wire-encoding path, without
// this test depending on their env-gated construction at all.

import (
	"encoding/json"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/azure"
	"github.com/kameas-ai/kenaz-harness/core/llm/bedrock"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/custom"
	"github.com/kameas-ai/kenaz-harness/core/llm/gemini"
	"github.com/kameas-ai/kenaz-harness/core/llm/ollama"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
	"github.com/kameas-ai/kenaz-harness/core/llm/openaiwire"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
)

// wp09G3Schema is a small, uncontroversial JSON Schema every encoder in
// this table can translate without hitting a provider-specific
// unsupported-keyword error (gemini's OpenAPI-3.0 subset in
// particular — see gemini/wire.go's checkGeminiSchemaKeywords).
var wp09G3Schema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`)

// wp09G3WireProbe drives a kind's real wire-encoding path directly —
// the same function/body Registry.Stream reaches once Gate.Check has
// allowed the request — and reports whether the resulting wire
// representation carries the schema. nil means "no externally
// reachable probe for this kind" (bedrock; see its case below).
type wp09G3WireProbe func(t *testing.T, req llm.GenerationRequest) (carriesSchema bool)

func wp09G3AnthropicProbe(t *testing.T, req llm.GenerationRequest) bool {
	t.Helper()
	wireBody := map[string]any{}
	if err := anthropic.New().ApplyResponseFormat(&req, wireBody); err != nil {
		t.Fatalf("anthropic.ApplyResponseFormat: %v", err)
	}
	_, ok := wireBody["tool_choice"]
	return ok
}

func wp09G3OpenAIProbe(t *testing.T, req llm.GenerationRequest) bool {
	t.Helper()
	wireBody := map[string]any{}
	if err := openai.New().ApplyResponseFormat(&req, wireBody); err != nil {
		t.Fatalf("openai.ApplyResponseFormat: %v", err)
	}
	_, ok := wireBody["response_format"]
	return ok
}

func wp09G3OpenRouterProbe(t *testing.T, req llm.GenerationRequest) bool {
	t.Helper()
	wireBody := map[string]any{}
	if err := openrouter.New().ApplyResponseFormat(&req, wireBody); err != nil {
		t.Fatalf("openrouter.ApplyResponseFormat: %v", err)
	}
	_, ok := wireBody["response_format"]
	return ok
}

// wp09G3OpenAIWireProbe drives azure-openai / custom-openai / ollama's
// SHARED encoder directly. All three build their entire request body
// through this one function in production, so this IS each of their
// real wire-encoding paths, not a re-implementation (spec §8 rule 3).
func wp09G3OpenAIWireProbe(t *testing.T, req llm.GenerationRequest) bool {
	t.Helper()
	body, err := openaiwire.BuildRequestBody(req, "wp09-g3-probe-model", nil)
	if err != nil {
		t.Fatalf("openaiwire.BuildRequestBody: %v", err)
	}
	_, ok := body["response_format"]
	return ok
}

func wp09G3GeminiProbe(t *testing.T, req llm.GenerationRequest) bool {
	t.Helper()
	gr, err := gemini.ToGeminiRequest(req, llm.ProviderProfile{Kind: gemini.Kind, Model: "wp09-g3-probe-model"})
	if err != nil {
		t.Fatalf("gemini.ToGeminiRequest: %v", err)
	}
	if gr.GenerationConfig == nil {
		return false
	}
	return len(gr.GenerationConfig.ResponseSchema) > 0
}

// TestG3_CapabilityRowMatchesAdapterBehaviour is UNIT-6/WP09's gate.
func TestG3_CapabilityRowMatchesAdapterBehaviour(t *testing.T) {
	cat, err := capabilities.LoadDefault()
	if err != nil {
		t.Fatalf("capabilities.LoadDefault: %v", err)
	}
	gate := capabilities.NewGate(cat)

	cases := []struct {
		name  string
		kind  string
		model string
		probe wp09G3WireProbe // nil = bedrock's documented exception, see below
	}{
		{"anthropic/provider-default", anthropic.Kind, "claude-unmatched-probe-model", wp09G3AnthropicProbe},
		{"anthropic/claude-sonnet-4 (true row)", anthropic.Kind, "claude-sonnet-4", wp09G3AnthropicProbe},

		{"openai/provider-default", openai.Kind, "gpt-3.5-turbo-unmatched-probe", wp09G3OpenAIProbe},
		{"openai/gpt-4o (true row)", openai.Kind, "gpt-4o", wp09G3OpenAIProbe},
		{"openai/o1-preview (false row)", openai.Kind, "o1-preview", wp09G3OpenAIProbe},

		{"openrouter/provider-default", openrouter.Kind, "unmatched/probe-model", wp09G3OpenRouterProbe},
		{"openrouter/openai-gpt-4o (true row)", openrouter.Kind, "openai/gpt-4o", wp09G3OpenRouterProbe},

		// azure-openai has no catalog file of its own — loader.go's
		// alias table resolves it onto openai.yaml (spec §5.3) — so
		// its true/false rows are exactly openai's.
		{"azure-openai/provider-default (aliases openai)", azure.Kind, "gpt-3.5-turbo-unmatched-probe", wp09G3OpenAIWireProbe},
		{"azure-openai/gpt-4o (true row, aliases openai)", azure.Kind, "gpt-4o", wp09G3OpenAIWireProbe},

		// custom-openai.yaml and ollama.yaml currently have NO models:
		// override that flips structured_output true (spec §5.3 /
		// ollama.yaml's own comment: "deferred to
		// local-model-runtimes-01KQ8VMZ" / "structured-output-is-
		// reachable-01PMZ808"), so only the false direction is
		// exercised here today. A future true row for either needs a
		// case added alongside it.
		{"custom-openai/provider-default", custom.Kind, "llama3.1", wp09G3OpenAIWireProbe},
		{"ollama/provider-default", ollama.Kind, "llama3.1", wp09G3OpenAIWireProbe},

		{"gemini/provider-default (true row)", gemini.Kind, "gemini-unmatched-probe-model", wp09G3GeminiProbe},
		{"gemini/gemini-1.0-pro (false row)", gemini.Kind, "gemini-1.0-pro", wp09G3GeminiProbe},

		// bedrock: the exported StructuredOutputAdapter method is a
		// DOCUMENTED no-op for wireBody (bedrock.go:179-184 — "wireBody
		// is accepted to satisfy the interface but is always ignored").
		// The real encoder (applyResponseFormatToConverseInput /
		// applyResponseFormatToConverseBodyJSON) is unexported, so it
		// cannot be driven from this package without either forking a
		// second harness inside package bedrock (which this WP's
		// sibling file does) or adding new exported production surface
		// area purely to satisfy a test — CLAUDE.md's ritual prefers
		// the former. Only the false-row half is exercised here, which
		// needs no adapter-specific code at all: Gate.Check's refusal
		// is generic across every kind.
		{"bedrock/provider-default (false row)", bedrock.Kind, "unmatched-bedrock-probe-model", nil},
		{"bedrock/titan-image (false row)", bedrock.Kind, "amazon.titan-image-v1", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desc := cat.Describe(tc.kind, tc.model)
			rowSaysTrue := desc.Has(llm.CapStructuredOutput)

			req := llm.GenerationRequest{
				ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: wp09G3Schema},
			}
			prof := llm.ProviderProfile{Kind: tc.kind, Model: tc.model}

			_, gateErr := gate.Check(req, prof)

			if !rowSaysTrue {
				if gateErr == nil {
					t.Fatalf(
						"row says structured_output=false for (%s, %s) but Gate.Check allowed a "+
							"json_schema request through unrefused — the row's refusal promise is not enforced",
						tc.kind, tc.model,
					)
				}
				if _, ok := gateErr.(*llm.ErrCapabilityUnsupported); !ok {
					t.Fatalf("Gate.Check refused (%s, %s) with a non-capability error: %v", tc.kind, tc.model, gateErr)
				}
				return // false row: refusal at the gate IS the whole contract — no wire body is ever built downstream of it.
			}

			// true row: the gate must let the request through...
			if gateErr != nil {
				t.Fatalf("row says structured_output=true for (%s, %s) but Gate.Check refused it: %v", tc.kind, tc.model, gateErr)
			}
			if tc.probe == nil {
				t.Skip("true-row wire proof lives in the adapter's own package for this kind; see the case comment above")
			}
			// ...and the adapter's own encoder must actually carry the schema.
			if !tc.probe(t, req) {
				t.Fatalf(
					"row says structured_output=true for (%s, %s) but the adapter's own wire-encoding "+
						"path did not carry the schema — the row advertises a capability the adapter cannot serve",
					tc.kind, tc.model,
				)
			}
		})
	}
}
