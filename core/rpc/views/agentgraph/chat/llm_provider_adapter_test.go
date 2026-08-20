package chat

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestGenerate_CarriesMaxTokensAndTemperature is WP01 of
// model-request-path-live-01PMDL01: before this fix Generate() built
// GenerationRequest without MaxTokens/Temperature at all, silently
// dropping every per-node sampling knob authored in ModelAttrs. Asserts
// a node with max_tokens/temperature set produces a request carrying
// them (via Params, the channel every production adapter reads), and
// that empty node values produce no override at all — not a forced
// zero/empty entry that would clobber a provider or profile default.
func TestGenerate_CarriesMaxTokensAndTemperature(t *testing.T) {
	temp := 0.42

	cases := []struct {
		name       string
		req        coreag.LLMRequest
		wantParams map[string]any
	}{
		{
			name: "max_tokens and temperature both set",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				MaxTokens:    2048,
				Temperature:  &temp,
			},
			wantParams: map[string]any{"max_tokens": 2048, "temperature": temp},
		},
		{
			name: "max_tokens only",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				MaxTokens:    512,
			},
			wantParams: map[string]any{"max_tokens": 512},
		},
		{
			name: "temperature only",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				Temperature:  &temp,
			},
			wantParams: map[string]any{"temperature": temp},
		},
		{
			name:       "empty node values yield provider defaults, not forced zeros",
			req:        coreag.LLMRequest{SystemPrompt: "base"},
			wantParams: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &capturingRegistry{}
			adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

			if _, err := adapter.Generate(context.Background(), tc.req); err != nil {
				t.Fatalf("Generate: %v", err)
			}

			gotParams := reg.snapshot().Params
			if len(tc.wantParams) == 0 {
				if len(gotParams) != 0 {
					t.Fatalf("expected no Params overrides (provider defaults), got %#v", gotParams)
				}
				return
			}
			if len(gotParams) != len(tc.wantParams) {
				t.Fatalf("Params length mismatch: got %#v want %#v", gotParams, tc.wantParams)
			}
			for k, want := range tc.wantParams {
				if got := gotParams[k]; got != want {
					t.Errorf("Params[%q] = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// TestGenerate_CarriesExpandedKnobSurface is WP05 of
// model-request-path-live-01PMDL01: extends the WP01 wiring to the rest
// of the ModelAttrs knob surface (TopP, TopK, FrequencyPenalty,
// PresencePenalty, Seed, ParallelToolCalls — routed through the same
// Params channel as MaxTokens/Temperature) plus StopSequences, which is
// a typed top-level field on GenerationRequest rather than a Params
// entry. Asserts each new knob reaches the seam→Generate() boundary
// correctly, and that omitted/zero-value knobs produce no override.
func TestGenerate_CarriesExpandedKnobSurface(t *testing.T) {
	topP := 0.9
	freqPenalty := 0.25
	presPenalty := -0.1

	topK := 40
	seed := 12345
	parallelToolCalls := true
	reasoningBudget := 4096

	req := coreag.LLMRequest{
		SystemPrompt:          "base",
		TopP:                  &topP,
		TopK:                  &topK,
		FrequencyPenalty:      &freqPenalty,
		PresencePenalty:       &presPenalty,
		Seed:                  &seed,
		ParallelToolCalls:     &parallelToolCalls,
		StopSequences:         []string{"STOP", "END"},
		ReasoningBudgetTokens: &reasoningBudget,
	}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()

	wantParams := map[string]any{
		"top_p":               topP,
		"top_k":               topK,
		"frequency_penalty":   freqPenalty,
		"presence_penalty":    presPenalty,
		"seed":                seed,
		"parallel_tool_calls": parallelToolCalls,
	}
	if len(gen.Params) != len(wantParams) {
		t.Fatalf("Params length mismatch: got %#v want %#v", gen.Params, wantParams)
	}
	for k, want := range wantParams {
		if got := gen.Params[k]; got != want {
			t.Errorf("Params[%q] = %v (%T), want %v (%T)", k, got, got, want, want)
		}
	}

	if len(gen.StopSequences) != 2 || gen.StopSequences[0] != "STOP" || gen.StopSequences[1] != "END" {
		t.Errorf("StopSequences = %#v, want [STOP END]", gen.StopSequences)
	}
	if gen.Reasoning == nil || !gen.Reasoning.Enabled || gen.Reasoning.BudgetTokens != 4096 {
		t.Errorf("Reasoning = %#v, want {Enabled:true BudgetTokens:4096}", gen.Reasoning)
	}
}

// TestGenerate_OmittedKnobsProduceNoOverride verifies that a node with
// none of the WP05 knobs set produces neither Params entries for them
// nor a StopSequences override — the zero-value-means-unset convention
// must not clobber provider/profile defaults with forced zeros.
func TestGenerate_OmittedKnobsProduceNoOverride(t *testing.T) {
	req := coreag.LLMRequest{SystemPrompt: "base"}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()
	for _, k := range []string{"top_p", "top_k", "frequency_penalty", "presence_penalty", "seed", "parallel_tool_calls"} {
		if _, ok := gen.Params[k]; ok {
			t.Errorf("Params[%q] unexpectedly present: %#v", k, gen.Params)
		}
	}
	if len(gen.StopSequences) != 0 {
		t.Errorf("StopSequences = %#v, want empty", gen.StopSequences)
	}
	if gen.Reasoning != nil {
		t.Errorf("Reasoning = %#v, want nil", gen.Reasoning)
	}
}

// TestGenerate_ToolMessage_CarriesIsError is WP01 of
// tool-error-legibility-01PMDL02: before this fix, a Role="tool" Message
// with IsError=true was translated into a corellm.ToolResult without
// IsError, so a failed tool call reached the wire looking identical to a
// success. Asserts the seam's IsError flag threads through onto the
// tool_result content block for both a failing and a succeeding call.
func TestGenerate_ToolMessage_CarriesIsError(t *testing.T) {
	cases := []struct {
		name        string
		isError     bool
		wantIsError bool
	}{
		{name: "failing tool result sets IsError", isError: true, wantIsError: true},
		{name: "succeeding tool result leaves IsError false", isError: false, wantIsError: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &capturingRegistry{}
			adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

			req := coreag.LLMRequest{
				SystemPrompt: "base",
				Messages: []coreag.Message{
					{
						Role:       "tool",
						Name:       "svc__broken",
						ToolCallID: "call-1",
						Content:    "boom",
						IsError:    tc.isError,
					},
				},
			}

			if _, err := adapter.Generate(context.Background(), req); err != nil {
				t.Fatalf("Generate: %v", err)
			}

			gen := reg.snapshot()
			if len(gen.Messages) != 1 {
				t.Fatalf("expected 1 translated message, got %d: %#v", len(gen.Messages), gen.Messages)
			}
			blocks := gen.Messages[0].Content
			if len(blocks) != 1 || blocks[0].Type != "tool_result" || blocks[0].ToolResult == nil {
				t.Fatalf("expected one tool_result block, got %#v", blocks)
			}
			if got := blocks[0].ToolResult.IsError; got != tc.wantIsError {
				t.Errorf("ToolResult.IsError = %v, want %v", got, tc.wantIsError)
			}
		})
	}
}

// TestGenerate_CarriesResponseSchema is
// structured-output-is-reachable-01PMZE14 WP02's AC-001 half at the
// adapter-translation boundary: before this fix, LLMRequest.ResponseSchema
// (the seam field WP02 added) was read nowhere in Generate(), so
// GenerationRequest.ResponseFormat stayed permanently nil regardless of
// what the seam carried. Typed field, not Params — mirrors how
// StopSequences/Reasoning are handled rather than folded into the
// untyped Params map.
func TestGenerate_CarriesResponseSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)
	req := coreag.LLMRequest{SystemPrompt: "base", ResponseSchema: schema}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()
	if gen.ResponseFormat == nil {
		t.Fatalf("ResponseFormat is nil; ResponseSchema did not reach GenerationRequest")
	}
	if gen.ResponseFormat.Mode != "json_schema" {
		t.Errorf("ResponseFormat.Mode = %q, want %q", gen.ResponseFormat.Mode, "json_schema")
	}
	if string(gen.ResponseFormat.Schema) != string(schema) {
		t.Errorf("ResponseFormat.Schema = %s, want %s", gen.ResponseFormat.Schema, schema)
	}
}

// TestGenerate_NoResponseSchemaYieldsNoResponseFormat mirrors the seam's
// zero-means-unset convention for the new field.
func TestGenerate_NoResponseSchemaYieldsNoResponseFormat(t *testing.T) {
	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gen := reg.snapshot(); gen.ResponseFormat != nil {
		t.Errorf("ResponseFormat = %#v, want nil", gen.ResponseFormat)
	}
}

// schemaAwareRegistry is a corellm.Registry fake that behaves like a
// real structured-output-capable provider would: it returns
// schema-conformant JSON when the wire request carries a ResponseFormat,
// and non-conformant prose otherwise. Unlike capturingRegistry (which
// always returns a fixed canned response regardless of the request),
// this is the "fake adapter that returns schema-violating prose unless
// the wire body carries the schema" AC-001 calls for
// (structured-output-is-reachable-01PMZE14 spec §9) — proving the
// constraint reaches the wire by observing its effect on the RESPONSE,
// not by asserting a field was set.
type schemaAwareRegistry struct {
	mu      sync.Mutex
	lastReq corellm.GenerationRequest
}

func (r *schemaAwareRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *schemaAwareRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *schemaAwareRegistry) Evict(_ string) error                           { return nil }
func (r *schemaAwareRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, nil
}
func (r *schemaAwareRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (r *schemaAwareRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.lastReq = req
	r.mu.Unlock()
	text := `Sure! Here's my answer: pass`
	if req.ResponseFormat != nil && req.ResponseFormat.Mode == "json_schema" && len(req.ResponseFormat.Schema) > 0 {
		text = `{"verdict":"pass"}`
	}
	return &schemaAwareStream{content: text}, nil
}

type schemaAwareStream struct{ content string }

func (s *schemaAwareStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *schemaAwareStream) Cancel() error { return nil }
func (s *schemaAwareStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: s.content}},
		FinishReason: "stop",
	}, nil
}

// TestGenerate_ResponseSchemaConstrainsTheResponse is AC-001's
// falsification test proper: it does NOT assert a field was set (the
// spec explicitly forbids that — "the whole point of this AC is that a
// field being set is what the tree already looks like today"). It
// asserts the RESPONSE: against a fake that only emits schema-conformant
// JSON when the wire body carries the constraint, the resulting
// LLMResponse.Content parses as valid JSON precisely when
// LLMRequest.ResponseSchema was populated. This goes red if the
// translation edit (Generate()'s ResponseFormat assignment) is reverted,
// independent of TestGenerate_CarriesResponseSchema above.
func TestGenerate_ResponseSchemaConstrainsTheResponse(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)

	t.Run("schema present -> conformant JSON response", func(t *testing.T) {
		reg := &schemaAwareRegistry{}
		adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)
		resp, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base", ResponseSchema: schema})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
			t.Fatalf("response %q did not parse as JSON: %v", resp.Content, err)
		}
	})

	t.Run("schema absent -> non-conformant prose (today's behaviour, unchanged)", func(t *testing.T) {
		reg := &schemaAwareRegistry{}
		adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)
		resp, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(resp.Content), &out); err == nil {
			t.Fatalf("expected non-JSON prose when no schema was authored, got parseable JSON: %q", resp.Content)
		}
	})
}
