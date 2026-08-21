package rpc

// TestNewLLMStack_CostReducer_DerivesRealCost is
// model-settings-reach-the-model-01PMZ101 WP05's AC-004, driven through
// the SAME production construction path rpc.New uses at boot
// (newLLMStack — see llmRegistryOverDataDir in
// api_cedar_gate_wiring_test.go), not a hand-built registry.New(Options{
// Cost: ...}). A test that constructs the Options struct itself proves the
// reducer works, which was never in doubt (core/llm/registry has that
// coverage) — it cannot see a missing assignment at the ONE production
// call site (core/rpc/api.go's newLLMStack), which is the actual defect
// spec §1.4 records: no call site anywhere in core/rpc ever set
// llmregistry.Options.Cost.
//
// The anthropic adapter's transport is swapped for an httptest server via
// Registry.RegisterAdapter (the OSS/enterprise extensibility seam,
// llm.Registry doc) so this drives a real wire round-trip without a live
// network call — everything upstream of the adapter (capability gate,
// Cedar policy guard, credential resolution, and the cost reducer under
// test) is the untouched production wiring newLLMStack built.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
)

func TestNewLLMStack_CostReducer_DerivesRealCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-5","usage":{"input_tokens":1000,"output_tokens":500}}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1000,"output_tokens":500}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	reg := llmRegistryOverDataDir(t, t.TempDir())
	reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))

	t.Setenv("ZZ_COST_PROBE_KEY", "test-key-bytes")
	prof := corellm.ProviderProfile{
		ID:    "zz-cost-probe",
		Kind:  anthropic.Kind,
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ZZ_COST_PROBE_KEY"},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	stream, err := reg.Stream(context.Background(), corellm.GenerationRequest{ProfileID: "zz-cost-probe"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if resp.Cost.Indeterminate {
		t.Fatal("want a derived cost through the production newLLMStack construction path, " +
			"got Indeterminate=true — core/rpc/api.go's newLLMStack is not constructing a Cost reducer")
	}
	if resp.Cost.Total <= 0 {
		t.Fatalf("want a non-zero derived Total from the real starter cost table, got %+v", resp.Cost)
	}
	if resp.Cost.Currency == "" {
		t.Fatalf("want a Currency on the derived cost, got %+v", resp.Cost)
	}
}
