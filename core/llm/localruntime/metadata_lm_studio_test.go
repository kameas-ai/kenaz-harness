package localruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLMStudioFetcher_HTTPModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"lmstudio-community/Meta-Llama-3.1-8B-Instruct-Q4_K_M"}]}`))
	}))
	defer srv.Close()

	models, err := fetchLMStudioHTTP(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchLMStudioHTTP error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected at least 1 model")
	}
	if models[0].QuantLevel == "" {
		t.Errorf("expected quant level to be parsed from ID; got empty")
	}
}

// TestLMStudioFetcher_GracefulDegradeCLI verifies that fetchLMStudioCLI
// returns an empty slice (not an error) when `lms` is not on PATH.
func TestLMStudioFetcher_GracefulDegradeCLI(t *testing.T) {
	// Set PATH to an empty temp dir so `lms` is definitely not found.
	t.Setenv("PATH", t.TempDir())

	models, err := fetchLMStudioCLI(context.Background())
	if err != nil {
		t.Errorf("expected nil error when lms not found; got: %v", err)
	}
	if models != nil && len(models) != 0 {
		t.Errorf("expected empty slice; got %d models", len(models))
	}
}
