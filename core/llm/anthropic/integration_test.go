//go:build integration

// Package anthropic_test integration tests.
//
// Build tag: integration. The test exercises the full pipeline (registry
// + capabilities + credref + audit emitter + retry middleware + adapter)
// against an httptest fake. It is opt-in so the default `go test ./...`
// run stays at unit-test cadence.
//
// External test package (anthropic_test) avoids the import cycle that
// would arise from importing core/llm/registry (which imports the
// anthropic adapter) inside the anthropic package itself.
//
// Run with:
//
//	go test -tags=integration ./core/llm/anthropic/...

package anthropic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/anthropic"
	"github.com/sigil-tech/kaneaz-harness/core/llm/anthropic/smoke"
	"github.com/sigil-tech/kaneaz-harness/core/llm/credref"
	"github.com/sigil-tech/kaneaz-harness/core/llm/events"
	llmregistry "github.com/sigil-tech/kaneaz-harness/core/llm/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// TestPipelineEndToEnd exercises the full connector pipeline:
//
//	registry.New() -> resolve provider profile -> CapabilityGate
//	-> PolicyGuard -> CredentialResolver -> RetryMiddleware
//	-> anthropic adapter -> SSE pump -> audit events
//
// Assertions:
//
//   - chunks arrive on the events channel
//   - audit events are emitted in the canonical order
//     (request_submitted, stream_chunk × N, response_final)
//   - the resolved credential bytes are passed to the adapter
//   - the adapter never embeds the credential in any payload (it
//     surfaces only via the x-api-key header on the wire)
func TestPipelineEndToEnd(t *testing.T) {
	const apiKey = "sk-ant-integration-fixture"
	const envVar = "ANTHROPIC_INTEGRATION_API_KEY"
	t.Setenv(envVar, apiKey)

	// Fake server: validates the x-api-key header round-trips and
	// streams a minimal happy path.
	gotHeader := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-api-key")
		body, _ := io.ReadAll(r.Body)
		// Sanity: body must NOT carry the api key.
		if got := string(body); contains(got, apiKey) {
			t.Errorf("request body must not carry credential: %s", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`{"type":"message_start","message":{"id":"x","model":"claude-sonnet-4-5","usage":{"input_tokens":5,"output_tokens":0}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	// Wire the connector with a memory secrets backend (resolves
	// RefEnv via os.Getenv) and an in-memory audit sink.
	sink := &events.MemorySink{}
	emitter := events.New(sink)
	reg, err := llmregistry.New(llmregistry.Options{
		Resolver: credref.New(secrets.NewMemoryBackend()),
		Emitter:  emitter,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	// Register the adapter pointed at the fake server.
	reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL), anthropic.WithHTTPClient(srv.Client())))

	prof := corellm.ProviderProfile{
		ID:    "p-int",
		Kind:  anthropic.Kind,
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: envVar},
	}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	stream, err := reg.Stream(context.Background(), corellm.GenerationRequest{
		ProfileID: "p-int",
		Messages: []corellm.Message{
			{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "ping"}}},
		},
		SessionID: "s-int",
	})
	if err != nil {
		t.Fatalf("registry Stream: %v", err)
	}

	// Drain chunks. We expect at least the text delta + finish.
	var sawText bool
	for ev := range stream.Events() {
		if ev.Kind == corellm.StreamText {
			sawText = true
		}
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if !sawText {
		t.Fatal("no text delta arrived from the pipeline")
	}
	if resp.FinishReason != "end_turn" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if gotHeader != apiKey {
		t.Fatalf("server saw x-api-key %q, want %q", gotHeader, apiKey)
	}

	// Audit ordering: request_submitted first, response_final last,
	// stream_chunk in between (matches registry_test.go pipeline test).
	kinds := sink.Kinds()
	if len(kinds) < 3 {
		t.Fatalf("not enough audit events: %v", kinds)
	}
	if kinds[0] != events.KindRequestSubmitted {
		t.Fatalf("first kind = %s, want request_submitted", kinds[0])
	}
	if kinds[len(kinds)-1] != events.KindResponseFinal {
		t.Fatalf("last kind = %s, want response_final", kinds[len(kinds)-1])
	}
	for i := 1; i < len(kinds)-1; i++ {
		if kinds[i] != events.KindStreamChunk {
			t.Fatalf("kinds[%d] = %s, want stream_chunk", i, kinds[i])
		}
	}

	// Defense-in-depth: confirm no audit payload carries the api key.
	for _, e := range sink.Entries() {
		raw, _ := json.Marshal(e.Payload)
		if contains(string(raw), apiKey) {
			t.Fatalf("audit payload leaked credential: %s", raw)
		}
	}
}

// TestSmokeAgainstLiveAPI is the optional live-API hop. It runs only
// when ANTHROPIC_API_KEY is set in the environment, so CI default
// builds (which clear the key) skip it.
//
// Useful for an operator running `go test -tags=integration` locally
// after a key rotation to confirm the wire still works end to end.
func TestSmokeAgainstLiveAPI(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live-API smoke")
	}
	model := os.Getenv("ANTHROPIC_SMOKE_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	resp, err := smoke.Run(context.Background(), smoke.Config{
		Model:     model,
		EnvVar:    "ANTHROPIC_API_KEY",
		Prompt:    "Reply with the single word: OK.",
		MaxTokens: 16,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Smoke: %v", err)
	}
	if resp.Text == "" {
		t.Fatal("smoke response had no text")
	}
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
