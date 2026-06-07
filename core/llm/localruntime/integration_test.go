package localruntime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm/localruntime"
)

// fakeOllamaServer starts a minimal Ollama API stub that handles /api/tags.
func fakeOllamaServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			payload := map[string]interface{}{
				"models": []map[string]interface{}{
					{
						"name": "llama3:8b-q4_K_M",
						"size": int64(4_800_000_000),
						"details": map[string]string{
							"quantization_level": "Q4_K_M",
							"parameter_size":     "8B",
						},
					},
					{
						"name": "mistral:7b",
						"size": int64(4_100_000_000),
						"details": map[string]string{
							"quantization_level": "",
							"parameter_size":     "7B",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// portFromURL extracts the integer port from a URL like http://127.0.0.1:PORT.
func portFromURL(rawURL string) int {
	parts := strings.Split(rawURL, ":")
	p, _ := strconv.Atoi(parts[len(parts)-1])
	return p
}

// TestIntegration_DetectAll_WithFakeOllama verifies that DetectAll correctly
// marks Ollama as running when a fake server is bound to its port.
func TestIntegration_DetectAll_WithFakeOllama(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	t.Setenv("PATH", t.TempDir()) // no binary

	srv := fakeOllamaServer(t)

	// Override Ollama's port and URL to point at the fake server.
	origPort := localruntime.DefaultPorts()[localruntime.KindOllama]
	localruntime.SetDefaultPort(localruntime.KindOllama, portFromURL(srv.URL))
	defer localruntime.SetDefaultPort(localruntime.KindOllama, origPort)

	origURL := localruntime.DefaultBaseURLs()[localruntime.KindOllama]
	localruntime.SetDefaultBaseURL(localruntime.KindOllama, srv.URL)
	defer localruntime.SetDefaultBaseURL(localruntime.KindOllama, origURL)

	localruntime.Invalidate()

	results := localruntime.DetectAll(context.Background())

	var ollamaInfo *localruntime.RuntimeInfo
	for _, r := range results {
		r := r
		if r.Kind == localruntime.KindOllama {
			ollamaInfo = &r
			break
		}
	}

	if ollamaInfo == nil {
		t.Fatal("Ollama not found in DetectAll results")
	}
	if !ollamaInfo.Running {
		t.Error("expected Ollama.Running=true with fake server bound")
	}
}

// TestIntegration_OllamaFetcher_WithFakeServer verifies end-to-end model
// fetching against the fake Ollama server fixture.
func TestIntegration_OllamaFetcher_WithFakeServer(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	srv := fakeOllamaServer(t)

	info := localruntime.RuntimeInfo{
		Kind:           localruntime.KindOllama,
		DefaultBaseURL: srv.URL,
		Running:        true,
	}

	f := localruntime.FindFetcher(localruntime.KindOllama)
	if f == nil {
		t.Fatal("no fetcher registered for ollama")
	}

	models, err := f.FetchModels(context.Background(), info)
	if err != nil {
		t.Fatalf("FetchModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models; got %d", len(models))
	}

	m0 := models[0]
	if m0.QuantLevel != "Q4_K_M" {
		t.Errorf("models[0].QuantLevel = %q; want Q4_K_M", m0.QuantLevel)
	}
	if m0.SizeBytes != 4_800_000_000 {
		t.Errorf("models[0].SizeBytes = %d; want 4800000000", m0.SizeBytes)
	}
}

// TestIntegration_FeatureFlag_Off verifies that with HARNESS_LOCAL_RUNTIMES=0,
// DetectAll returns an empty slice and the cache is not populated.
func TestIntegration_FeatureFlag_Off(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "0")
	localruntime.Invalidate()

	results := localruntime.DetectAll(context.Background())
	if len(results) != 0 {
		t.Errorf("expected empty slice when disabled; got %d", len(results))
	}
}

// TestIntegration_QuantizationParsing is a fixture-based test ensuring
// real-world model name strings are parsed correctly.
func TestIntegration_QuantizationParsing(t *testing.T) {
	fixtures := []struct {
		in   string
		want string
	}{
		// Real Ollama tag names
		{"llama3:8b-instruct-q4_K_M", "Q4_K_M"},
		{"phi3.5:3.8b-mini-instruct-q4_K_M", "Q4_K_M"},
		{"mistral:7b-instruct-q5_K_S", "Q5_K_S"},
		// GGUF filenames from llama-server walker
		{"Meta-Llama-3.1-8B-Instruct-Q8_0.gguf", "Q8_0"},
		{"Phi-3-mini-4k-instruct-q4.gguf", ""},   // no standard label
		{"mistral-7b-instruct-v0.2.Q5_K_M.gguf", "Q5_K_M"},
		// Float formats
		{"llama-3.1-8b-f16.gguf", "F16"},
		// Models with no quantization
		{"gpt2", ""},
	}

	for _, f := range fixtures {
		got := localruntime.ParseQuantLevel(f.in)
		if f.want == "" {
			if got != "" {
				t.Errorf("ParseQuantLevel(%q) = %q; want empty", f.in, got)
			}
		} else if got != f.want {
			t.Errorf("ParseQuantLevel(%q) = %q; want %q", f.in, got, f.want)
		}
	}
}
