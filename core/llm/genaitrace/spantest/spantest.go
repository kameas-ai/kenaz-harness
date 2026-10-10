// Package spantest is the test kit for the gen_ai.chat span
// (llm-call-telemetry-01LLMTEL01 WP01): it records the spans an adapter
// call produces through the real instrumentation choke point
// (genaitrace.Instrument) and asserts the exact attribute set — every key
// on the allowlist, no content anywhere. Imported only by _test.go files,
// like core/llm/wirecheck.
package spantest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

// globalMu serialises tests that swap the process TracerProvider.
var globalMu sync.Mutex

// Record installs a recording TracerProvider as the OTel global for the
// rest of t, restoring the previous one on cleanup. Tests that call it
// are serialised against each other (the provider is process-global).
func Record(t testing.TB) *tracetest.SpanRecorder {
	t.Helper()
	globalMu.Lock()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
		globalMu.Unlock()
	})
	return rec
}

// Named returns the ended spans called name, in end order.
func Named(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// Drive runs one adapter call through genaitrace.Instrument exactly as
// registry.Stream does, drains it, and returns the final response and the
// one gen_ai.chat span it produced.
// cost stands in for the registry's cost readout; ProviderCost is the
// readout with no price table.
func Drive(t testing.TB, rec *tracetest.SpanRecorder, a llm.ProviderAdapter, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte, cost genaitrace.CostFunc) (llm.Response, sdktrace.ReadOnlySpan) {
	t.Helper()
	before := len(Named(rec, genaitrace.SpanNameChat))
	call := genaitrace.CallFromRequest(req, prof)
	s, err := genaitrace.Instrument(context.Background(), call, cost, func(ctx context.Context) (llm.Stream, error) {
		return a.Stream(ctx, req, prof, cred)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range s.Events() {
	}
	resp, err := s.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	spans := Named(rec, genaitrace.SpanNameChat)
	if len(spans) != before+1 {
		t.Fatalf("got %d new gen_ai.chat spans, want 1", len(spans)-before)
	}
	return resp, spans[len(spans)-1]
}

// ProviderCost is the cost readout with no price table: the adapter's
// provider-reported cost, if any.
func ProviderCost(r llm.Response) llm.Cost { return r.Cost }

// Present, as a want value in AssertExact, asserts only that the key is
// set (for timing attributes such as kenaz.llm.ttft_ms).
var Present = present{}

type present struct{}

// Attrs flattens a span's attributes into key → Go value (string, int64,
// float64, bool, []string).
func Attrs(s sdktrace.ReadOnlySpan) map[string]any {
	m := map[string]any{}
	for _, kv := range s.Attributes() {
		m[string(kv.Key)] = value(kv.Value)
	}
	return m
}

func value(v attribute.Value) any {
	switch v.Type() {
	case attribute.STRING:
		return v.AsString()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.BOOL:
		return v.AsBool()
	case attribute.STRINGSLICE:
		return v.AsStringSlice()
	}
	return v.Emit()
}

// AssertExact fails unless s carries exactly want (same keys, same
// values), every key is on the genaitrace allowlist, the span carries no
// events, links or status description, and no string value contains any
// of content (the prompt, completion, tool argument and system texts the
// fixture sent or received).
func AssertExact(t testing.TB, s sdktrace.ReadOnlySpan, want map[string]any, content ...string) {
	t.Helper()
	got := Attrs(s)
	AssertAllowlisted(t, s, content...)
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		g, gok := got[k]
		w, wok := want[k]
		switch {
		case !gok:
			t.Errorf("attribute %s missing, want %v", k, w)
		case !wok:
			t.Errorf("unexpected attribute %s = %v", k, g)
		case w == Present:
		case fmt.Sprint(g) != fmt.Sprint(w):
			t.Errorf("attribute %s = %#v, want %#v", k, g, w)
		}
	}
}

// AssertAllowlisted checks the never-content invariants without pinning
// values: allowlisted keys only, no events/links/status text, and no
// value containing any content string.
func AssertAllowlisted(t testing.TB, s sdktrace.ReadOnlySpan, content ...string) {
	t.Helper()
	allowed := map[string]genaitrace.AttrSpec{}
	for _, a := range genaitrace.SpanAttributes {
		allowed[a.Key] = a
	}
	for _, kv := range s.Attributes() {
		if _, ok := allowed[string(kv.Key)]; !ok {
			t.Errorf("span %s carries non-allowlisted attribute %s", s.Name(), kv.Key)
		}
		text := kv.Value.Emit()
		for _, c := range content {
			if c != "" && strings.Contains(text, c) {
				t.Errorf("span %s attribute %s carries content %q", s.Name(), kv.Key, c)
			}
		}
	}
	if n := len(s.Events()); n != 0 {
		t.Errorf("span %s has %d events; data must travel in attributes only", s.Name(), n)
	}
	if n := len(s.Links()); n != 0 {
		t.Errorf("span %s has %d links", s.Name(), n)
	}
	if d := s.Status().Description; d != "" {
		t.Errorf("span %s status description %q; status must be code-only", s.Name(), d)
	}
}

// Content strings the fixture request sends and fixture responses return.
// None may appear in any span attribute.
const (
	SystemText     = "SECRET-SYSTEM-PROMPT-TEXT"
	PromptText     = "SECRET-USER-PROMPT-TEXT"
	CompletionText = "SECRET-COMPLETION-TEXT"
	ToolArgsText   = "SECRET-TOOL-ARGUMENTS"
	ToolResultText = "SECRET-TOOL-RESULT"
	SessionID      = "session-raw-id-0001"
)

// Content lists every content string above, for AssertExact.
var Content = []string{SystemText, PromptText, CompletionText, ToolArgsText, ToolResultText, SessionID}

// Request is the content-bearing fixture request every adapter table
// test sends: a system prompt, a user turn, one tool, max_tokens 512,
// temperature 0.3 and a session id.
func Request(profileID string) llm.GenerationRequest {
	return llm.GenerationRequest{
		ProfileID: profileID,
		System:    SystemText,
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, PromptText)},
		Tools: []llm.ToolSpec{{
			Name:        "search",
			Description: "search the web",
			InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			TokenEst:    42,
		}},
		Params:    map[string]any{"max_tokens": 512, "temperature": 0.3},
		SessionID: SessionID,
	}
}

// BaseWant is the request-side attribute set of Request dispatched to
// provider (a gen_ai.provider.name value) with model, outside any call
// sequence (attempt 0, not a fallback). Callers add the response side.
func BaseWant(provider, model string) map[string]any {
	return map[string]any{
		"gen_ai.operation.name":            "chat",
		"gen_ai.provider.name":             provider,
		"gen_ai.system":                    provider,
		"gen_ai.request.model":             model,
		"gen_ai.request.max_tokens":        int64(512),
		"gen_ai.request.temperature":       0.3,
		"kenaz.llm.attempt":                int64(0),
		"kenaz.llm.fallback":               false,
		"kenaz.llm.stream":                 true,
		"kenaz.llm.tool_count":             int64(1),
		"kenaz.llm.tool_schema_tokens_est": int64(42),
		"kenaz.session.hash":               genaitrace.SessionHash(SessionID),
		"kenaz.llm.ttft_ms":                Present,
	}
}

// With returns base plus extra (extra wins).
func With(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
