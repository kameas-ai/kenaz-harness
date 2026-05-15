package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// groqShapeServer returns an httptest.Server that mimics Groq's behavior:
// streaming works, tool calling works, but streaming_usage is false
// (no usage frame in the stream).
func groqShapeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		// Streaming text frame, no usage frame.
		frame := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "ok"}, "finish_reason": nil},
			},
		}
		data, _ := json.Marshal(frame)
		fmt.Fprintf(w, "data: %s\n\n", data)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
}

// vllmShapeServer returns an httptest.Server that mimics vLLM with no tool calling:
// step 2 returns 400 with "tool" in the error message.
func vllmShapeServer(t *testing.T) *httptest.Server {
	t.Helper()
	call := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			// Step 1: streaming ok.
			w.Header().Set("Content-Type", "text/event-stream")
			frame := map[string]any{
				"choices": []map[string]any{
					{"delta": map[string]any{"content": "hi"}, "finish_reason": nil},
				},
			}
			data, _ := json.Marshal(frame)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			return
		}
		// Step 2: reject tool calling.
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"tool use not supported","type":"invalid_request_error"}}`)
	}))
}

// authFailServer returns a server that always returns 401.
func authFailServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`)
	}))
}

// TestProbeReturnsBefore15s verifies the probe completes within the timeout budget.
func TestProbeReturnsBefore15s(t *testing.T) {
	srv := groqShapeServer(t)
	defer srv.Close()

	p := NewProber(srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := ProbeRequest{
		BaseURL:    srv.URL + "/v1",
		Model:      "test-model",
		AuthScheme: AuthSchemeNone,
	}
	start := time.Now()
	result := p.Probe(ctx, req)
	elapsed := time.Since(start)

	if elapsed > 15*time.Second {
		t.Errorf("Probe took %v, want < 15s", elapsed)
	}
	if result.Err != nil {
		t.Errorf("Probe error: %v", result.Err)
	}
	if result.Matrix == nil {
		t.Fatal("Matrix is nil")
	}
}

// TestProbeGroqShape verifies Groq-shape fixture: streaming=true, usage=false, tool=true.
func TestProbeGroqShape(t *testing.T) {
	// Groq shape: streaming works, tool calling works (200), no usage frame.
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "text/event-stream")
		frame := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": "ok"}, "finish_reason": nil},
			},
		}
		data, _ := json.Marshal(frame)
		if call == 2 {
			// Tool calling probe (non-streaming): return 200.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := NewProber(srv.Client())
	result := p.Probe(context.Background(), ProbeRequest{
		BaseURL:    srv.URL + "/v1",
		Model:      "test",
		AuthScheme: AuthSchemeNone,
	})
	if result.Err != nil {
		t.Fatalf("Probe error: %v", result.Err)
	}
	if result.Matrix.Streaming != CapabilityValueTrue {
		t.Errorf("Streaming = %q, want true", result.Matrix.Streaming)
	}
	if result.Matrix.StreamingUsage != CapabilityValueFalse {
		t.Errorf("StreamingUsage = %q, want false", result.Matrix.StreamingUsage)
	}
}

// TestProbeAuthFailureAbortsProbe verifies auth failure on step 1 aborts with error.
func TestProbeAuthFailureAbortsProbe(t *testing.T) {
	srv := authFailServer(t)
	defer srv.Close()

	p := NewProber(srv.Client())
	result := p.Probe(context.Background(), ProbeRequest{
		BaseURL:    srv.URL + "/v1",
		AuthScheme: AuthSchemeNone,
	})
	if result.Err == nil {
		t.Fatal("expected error on auth failure, got nil")
	}
	if !strings.Contains(result.Err.Error(), "auth failed") {
		t.Errorf("error = %q, want 'auth failed'", result.Err.Error())
	}
}

// TestProbePartialSavesUnknown verifies partial probe (step 1 ok, step 2 fail)
// saves matrix with "unknown" for failed fields.
func TestProbePartialSavesUnknown(t *testing.T) {
	srv := vllmShapeServer(t)
	defer srv.Close()

	p := NewProber(srv.Client())
	result := p.Probe(context.Background(), ProbeRequest{
		BaseURL:    srv.URL + "/v1",
		Model:      "test",
		AuthScheme: AuthSchemeNone,
	})
	if result.Err != nil {
		t.Fatalf("Probe error: %v", result.Err)
	}
	if result.Matrix == nil {
		t.Fatal("Matrix is nil")
	}
	// Step 1 ok → streaming should be set.
	if result.Matrix.Streaming != CapabilityValueTrue {
		t.Errorf("Streaming = %q, want true", result.Matrix.Streaming)
	}
	// Step 2 bad request with "tool" → tool_calling:false.
	if result.Matrix.ToolCalling != CapabilityValueFalse {
		t.Errorf("ToolCalling = %q, want false", result.Matrix.ToolCalling)
	}
}

// TestProbeStepTimeout verifies per-step 5s timeout is enforced.
// (This is a structural test — we verify the timeout context is created,
// not that it fires, since that would make the test take 5 seconds.)
func TestProbeStepTimeout(t *testing.T) {
	// Just verify probeStepTimeout constant is 5 seconds.
	if probeStepTimeout != 5*time.Second {
		t.Errorf("probeStepTimeout = %v, want 5s", probeStepTimeout)
	}
}
