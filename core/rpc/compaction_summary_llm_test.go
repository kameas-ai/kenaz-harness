package rpc

// compaction_summary_llm_test.go — chat-turn-integrity-01PMZ606 WP08.
// AC-010 (WP08 half) + the fallback test tasks.md prescribes.
//
// No live provider call: the registry's anthropic adapter transport is
// swapped for an httptest server via Registry.RegisterAdapter — the same
// seam core/rpc/cost_reducer_wiring_test.go and
// core/rpc/model_history_anthropic_test.go use — so this drives a real
// wire round-trip (registry -> capability gate -> policy guard ->
// credential resolver -> adapter -> HTTP) without a network call ever
// leaving the machine.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
)

// anthropicSSEServer starts an httptest server that speaks just enough
// Anthropic streaming-SSE to return replyText as the model's full
// response text.
func anthropicSSEServer(t *testing.T, replyText string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		frames := []string{
			`{"type":"message_start","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, replyText),
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":8}}`,
			`{"type":"message_stop"}`,
		}
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// erroringServer always returns a 500 so the adapter test can prove the
// FallbackEnabled -> heuristic path without needing a well-formed error
// body.
func erroringServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const compactionProbeProfileID = "zz-compaction-summary-probe"

func loadCompactionProbeProfile(t *testing.T, reg corellm.Registry, endpoint string) {
	t.Helper()
	reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(endpoint)))
	t.Setenv("ZZ_COMPACTION_PROBE_KEY", "test-key-bytes")
	prof := corellm.ProviderProfile{
		ID:    compactionProbeProfileID,
		Kind:  anthropic.Kind,
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ZZ_COMPACTION_PROBE_KEY"},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
}

// sampleTranscript is long enough that heuristicSummary's 80-char-per-
// message truncation would visibly mangle it, so the two code paths
// (real LLM vs. heuristic fallback) are trivially distinguishable by
// output shape as well as by content.
func sampleTranscript() []coreag.Message {
	return []coreag.Message{
		{Role: "user", Content: "What is the capital of France, and can you also tell me a bit about its history?"},
		{Role: "assistant", Content: "The capital of France is Paris. It has a rich history dating back over two thousand years."},
	}
}

// TestRegisterManualCompactionStrategies_SummaryReachesRealLLM is AC-010's
// WP08 half: NewSummaryStrategy is registered with a non-nil LLM, and a
// summary run reaches the adapter — proven by the returned summary
// carrying the httptest server's distinctive reply text, which
// heuristicSummary (the nil-LLM fallback) could never produce since it
// only ever echoes back truncated fragments of the INPUT messages.
//
// Mutation: restore the summary registration to nil (i.e. skip the
// `if llm := newCompactionSummaryLLM(...)` block in
// registerManualCompactionStrategies). Must fail — the returned content
// reverts to heuristicSummary's pipe-joined truncation and no longer
// contains the server's reply text.
func TestRegisterManualCompactionStrategies_SummaryReachesRealLLM(t *testing.T) {
	const wantReply = "WP08_LIVE_LLM_SUMMARY_MARKER: Paris, ~2000 years of history."
	srv := anthropicSSEServer(t, wantReply)

	reg := llmRegistryOverDataDir(t, t.TempDir())
	loadCompactionProbeProfile(t, reg, srv.URL)

	deps := &chat.CompactionDeps{
		CompactionModel: func() (compaction.ProviderProfileRef, bool) {
			return compaction.ProviderProfileRef{ProviderID: compactionProbeProfileID}, true
		},
	}

	pipeline := compaction.NewPipeline(
		compaction.WithResolver(compaction.NewMemoryResolverWithDefaults(compaction.PresetForTier("balanced"))),
	)
	// Boot-time state: drop_oldest + a nil-LLM summary, exactly what
	// newGraphManagerWithDeps registers before reg exists.
	pipeline.RegisterStrategy(compaction.NewDropOldestStrategy())
	pipeline.RegisterStrategy(compaction.NewSummaryStrategy(nil))

	// The WP08 fix under test: re-registers "summary" with a real LLM.
	registerManualCompactionStrategies(pipeline, deps, nil, reg)

	result, err := pipeline.Run(context.Background(), compaction.CompactRequest{
		RunID:    "manual:test-session",
		Scope:    compaction.ScopeKey{SessionID: "test-session"},
		Site:     compaction.SiteManual,
		Override: compaction.StrategySummary,
		Input: compaction.ContextSlice{
			Messages: sampleTranscript(),
		},
	})
	if err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}
	if len(result.Compacted.Messages) == 0 {
		t.Fatalf("Compacted.Messages is empty, want the summary message")
	}
	got := result.Compacted.Messages[0].Content
	if !strings.Contains(got, wantReply) {
		t.Fatalf("summary content = %q, want it to contain the live-server reply %q — "+
			"registerManualCompactionStrategies is not reaching the httptest server, "+
			"the summary strategy is still running the nil-LLM heuristic fallback",
			got, wantReply)
	}
}

// TestCompactionSummaryLLM_FallsBackToHeuristicOnAdapterError is the
// fallback half tasks.md WP08 calls for: an adapter error must not fail
// compaction outright — SummaryStrategy.FallbackEnabled (the default,
// and the only production configuration) catches it and produces the
// heuristic summary instead.
func TestCompactionSummaryLLM_FallsBackToHeuristicOnAdapterError(t *testing.T) {
	srv := erroringServer(t)

	reg := llmRegistryOverDataDir(t, t.TempDir())
	loadCompactionProbeProfile(t, reg, srv.URL)

	deps := &chat.CompactionDeps{
		CompactionModel: func() (compaction.ProviderProfileRef, bool) {
			return compaction.ProviderProfileRef{ProviderID: compactionProbeProfileID}, true
		},
	}

	pipeline := compaction.NewPipeline(
		compaction.WithResolver(compaction.NewMemoryResolverWithDefaults(compaction.PresetForTier("balanced"))),
	)
	pipeline.RegisterStrategy(compaction.NewDropOldestStrategy())
	pipeline.RegisterStrategy(compaction.NewSummaryStrategy(nil))
	registerManualCompactionStrategies(pipeline, deps, nil, reg)

	result, err := pipeline.Run(context.Background(), compaction.CompactRequest{
		RunID:    "manual:test-session-2",
		Scope:    compaction.ScopeKey{SessionID: "test-session-2"},
		Site:     compaction.SiteManual,
		Override: compaction.StrategySummary,
		Input: compaction.ContextSlice{
			Messages: sampleTranscript(),
		},
	})
	if err != nil {
		t.Fatalf("Pipeline.Run: %v (a provider error must degrade to the heuristic summary, not fail compaction)", err)
	}
	if len(result.Compacted.Messages) == 0 {
		t.Fatalf("Compacted.Messages is empty, want the heuristic summary message")
	}
	// heuristicSummary joins role:truncated-content fragments with " | ".
	got := result.Compacted.Messages[0].Content
	if !strings.Contains(got, "user:") || !strings.Contains(got, "assistant:") {
		t.Fatalf("summary content = %q, want the heuristic role-prefixed fallback shape", got)
	}
}

// TestCompactionSummaryLLM_Generate_NoModelConfigured pins the typed
// error compactionSummaryLLM.Generate returns when neither the request
// nor defaultModel can supply a provider — the seam SummaryStrategy's
// FallbackEnabled path relies on.
func TestCompactionSummaryLLM_Generate_NoModelConfigured(t *testing.T) {
	adapter := newCompactionSummaryLLM(llmRegistryOverDataDir(t, t.TempDir()), nil)
	if adapter == nil {
		t.Fatal("newCompactionSummaryLLM returned nil for a non-nil registry")
	}
	_, err := adapter.Generate(context.Background(), coreag.LLMRequest{
		Messages: []coreag.Message{{Role: "user", Content: "x"}},
	})
	if !errors.Is(err, errNoCompactionSummaryModel) {
		t.Fatalf("Generate error = %v, want errNoCompactionSummaryModel", err)
	}
}

// TestNewCompactionSummaryLLM_NilRegistry pins the nil-chassis degrade:
// a nil registry must not construct an adapter that always errors — the
// caller (registerManualCompactionStrategies) uses this to skip
// re-registration and leave the earlier nil-LLM heuristic strategy in
// place.
func TestNewCompactionSummaryLLM_NilRegistry(t *testing.T) {
	if got := newCompactionSummaryLLM(nil, nil); got != nil {
		t.Fatalf("newCompactionSummaryLLM(nil, nil) = %#v, want nil", got)
	}
}
