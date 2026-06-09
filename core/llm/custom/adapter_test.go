package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// sseResponse returns an http.Handler that serves a minimal SSE stream
// with a single text token and [DONE].
func sseResponse(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frame := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{"content": token}, "finish_reason": nil},
			},
		}
		data, _ := json.Marshal(frame)
		fmt.Fprintf(w, "data: %s\n\n", data)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}
}

// TestAdapterKind verifies Kind() returns the canonical string.
func TestAdapterKind(t *testing.T) {
	a := New()
	if a == nil {
		t.Skip("custom-openai disabled by env flag")
	}
	if got := a.Kind(); got != Kind {
		t.Errorf("Kind() = %q, want %q", got, Kind)
	}
}

// TestCompileTimeWitnesses is a compile-time check that the interfaces
// are satisfied (already enforced by var _ lines in adapter.go, but
// verifying here for documentation).
func TestCompileTimeWitnesses(t *testing.T) {
	var _ llm.ProviderAdapter = (*Adapter)(nil)
	var _ llm.ModelLister = (*Adapter)(nil)
}

// TestStreamChatEndpointURL verifies trailing-/v1 normalization table.
func TestStreamChatEndpointURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"trailing /v1", "http://localhost:8000/v1", "http://localhost:8000/v1/chat/completions"},
		{"trailing /v1/", "http://localhost:8000/v1/", "http://localhost:8000/v1/chat/completions"},
		{"full path", "http://localhost:8000/v1/chat/completions", "http://localhost:8000/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChatEndpoint(tc.baseURL)
			if err != nil {
				t.Fatalf("ChatEndpoint: %v", err)
			}
			if got != tc.want {
				t.Errorf("ChatEndpoint(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}

// TestStreamWireShape verifies the adapter sends the correct wire shape.
func TestStreamWireShape(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		received = body
		// Return a minimal SSE response.
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := &Adapter{
		httpc:     srv.Client(),
		templates: &Registry{templates: nil, byID: map[string]*Template{}},
	}

	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "gpt-3.5-turbo",
		Endpoint: srv.URL + "/v1",
		Cred:     llm.CredentialReference{Kind: "env", Locator: "TEST"},
		Defaults: map[string]any{"auth_scheme": "none"},
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hello")},
	}

	stream, err := a.Stream(context.Background(), req, prof, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Error("response content is empty")
	}

	// Verify wire shape.
	var wireBody map[string]any
	if err := json.Unmarshal(received, &wireBody); err != nil {
		t.Fatalf("parse wire body: %v", err)
	}
	if wireBody["model"] != "gpt-3.5-turbo" {
		t.Errorf("wire model = %v, want gpt-3.5-turbo", wireBody["model"])
	}
	if wireBody["stream"] != true {
		t.Errorf("wire stream = %v, want true", wireBody["stream"])
	}
}

// TestGateOutgoingToolsBlocked verifies tools request is blocked when matrix says false.
func TestGateOutgoingToolsBlocked(t *testing.T) {
	// Create adapter with a fake store that returns tool_calling:false.
	a := &Adapter{
		httpc:     http.DefaultClient,
		templates: &Registry{templates: nil, byID: map[string]*Template{}},
		store:     &fakeStore{matrix: &CapabilityMatrix{ToolCalling: CapabilityValueFalse}},
	}
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "llama",
		Endpoint: "http://localhost:8000/v1",
		Defaults: map[string]any{"auth_scheme": "none"},
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Tools:     []llm.ToolSpec{{Name: "calc", Description: "a tool", InputSchema: json.RawMessage(`{}`)}},
	}
	// Track HTTP calls: should be zero.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HTTP call should not have been made when gate blocks")
	}))
	defer srv.Close()

	prof.Endpoint = srv.URL + "/v1"
	_, err := a.Stream(context.Background(), req, prof, nil)
	if err == nil {
		t.Fatal("expected error from gate, got nil")
	}
	var gateErr *ErrCustomEndpointMissingCapability
	if !isCapErr(err, &gateErr) {
		t.Errorf("expected *ErrCustomEndpointMissingCapability, got %T: %v", err, err)
	}
}

// TestGateOutgoingNoMatrixPermits verifies requests pass when no matrix probed.
func TestGateOutgoingNoMatrixPermits(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := &Adapter{
		httpc:     srv.Client(),
		templates: &Registry{templates: nil, byID: map[string]*Template{}},
		store:     &fakeStore{matrix: nil}, // no matrix
	}
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "llama",
		Endpoint: srv.URL + "/v1",
		Defaults: map[string]any{"auth_scheme": "none"},
	}
	req := llm.GenerationRequest{ProfileID: "test"}
	stream, err := a.Stream(context.Background(), req, prof, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()
	if callCount == 0 {
		t.Error("expected HTTP call to be made when no matrix")
	}
}

// TestStreamBearerAuth verifies bearer token is sent in Authorization header.
func TestStreamBearerAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	a := &Adapter{
		httpc:     srv.Client(),
		templates: &Registry{templates: nil, byID: map[string]*Template{}},
	}
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "m",
		Endpoint: srv.URL + "/v1",
		Defaults: map[string]any{"auth_scheme": "bearer"},
	}
	stream, err := a.Stream(context.Background(), llm.GenerationRequest{ProfileID: "test"}, prof, []byte("mytoken"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer prefix", gotAuth)
	}
}

// fakeStore implements CapabilityStore for tests.
type fakeStore struct {
	matrix *CapabilityMatrix
}

func (f *fakeStore) Get(_ context.Context, _ string) (*CapabilityMatrix, error) {
	return f.matrix, nil
}

func isCapErr(err error, target **ErrCustomEndpointMissingCapability) bool {
	e, ok := err.(*ErrCustomEndpointMissingCapability)
	if ok {
		*target = e
	}
	return ok
}
