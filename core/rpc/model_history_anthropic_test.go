package rpc

// captureAnthropicRequestBody drives the REAL anthropic adapter and
// returns the JSON body it wrote to the wire.
//
// The adapter's own buildRequestBody is unexported, so a cross-package
// test cannot call it — and going through Stream() against an httptest
// server is the more honest assertion anyway: what this returns is
// literally what api.anthropic.com would have received, produced by the
// production renderer, with no re-implementation anywhere in the path.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
)

func captureAnthropicRequestBody(t *testing.T, msgs []corellm.Message) map[string]any {
	t.Helper()

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		captured = b
		// Minimal well-formed SSE so Stream() returns cleanly rather than
		// erroring out before we can read what it sent.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range []string{
			`{"type":"message_start","message":{"id":"msg_01","role":"assistant","model":"claude-sonnet-4-5","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
			`{"type":"message_stop"}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	ad := anthropic.New(anthropic.WithEndpoint(srv.URL))
	prof := corellm.ProviderProfile{
		ID:    "p-ant",
		Kind:  anthropic.Kind,
		Model: "claude-sonnet-4-5",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	stream, err := ad.Stream(context.Background(),
		corellm.GenerationRequest{Messages: msgs}, prof, []byte("test-key"))
	if err != nil {
		t.Fatalf("anthropic Stream: %v", err)
	}
	// Drain so the server handler has certainly run.
	for range stream.Events() {
	}
	_, _ = stream.Final()

	if len(captured) == 0 {
		t.Fatal("anthropic adapter sent no request body")
	}
	var body map[string]any
	if err := json.Unmarshal(captured, &body); err != nil {
		t.Fatalf("unmarshal captured body: %v\nraw: %s", err, captured)
	}
	return body
}
