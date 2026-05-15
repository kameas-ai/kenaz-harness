package localruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestOllamaFetcher_ParsesTags verifies the Ollama fetcher parses a real
// /api/tags fixture with sizes and quant levels.
func TestOllamaFetcher_ParsesTags(t *testing.T) {
	fixture := ollamaTagsResponse{
		Models: []ollamaModelEntry{
			{
				Name: "llama3:8b-instruct-q4_K_M",
				Size: 4_800_000_000,
				Details: struct {
					QuantizationLevel string `json:"quantization_level"`
					ParameterSize     string `json:"parameter_size"`
				}{QuantizationLevel: "Q4_K_M", ParameterSize: "8B"},
			},
			{
				Name: "mistral:7b",
				Size: 4_100_000_000,
				Details: struct {
					QuantizationLevel string `json:"quantization_level"`
					ParameterSize     string `json:"parameter_size"`
				}{QuantizationLevel: "", ParameterSize: "7B"},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	defer srv.Close()

	info := RuntimeInfo{
		Kind:           KindOllama,
		DefaultBaseURL: srv.URL,
		Port:           0,
		Running:        true,
		DetectedAt:     time.Now(),
	}

	f := &ollamaFetcher{}
	models, err := f.FetchModels(context.Background(), info)
	if err != nil {
		t.Fatalf("FetchModels returned error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models; got %d", len(models))
	}

	// First model: quant from Details
	m0 := models[0]
	if m0.ID != "llama3:8b-instruct-q4_K_M" {
		t.Errorf("models[0].ID = %q; want llama3:8b-instruct-q4_K_M", m0.ID)
	}
	if m0.QuantLevel != "Q4_K_M" {
		t.Errorf("models[0].QuantLevel = %q; want Q4_K_M", m0.QuantLevel)
	}
	if m0.SizeBytes != 4_800_000_000 {
		t.Errorf("models[0].SizeBytes = %d; want 4800000000", m0.SizeBytes)
	}
	if m0.ParameterCount != 8 {
		t.Errorf("models[0].ParameterCount = %f; want 8", m0.ParameterCount)
	}

	// Second model: quant from name
	m1 := models[1]
	if m1.QuantLevel != "" {
		// "mistral:7b" has no quant in name — OK if empty
		t.Logf("models[1].QuantLevel = %q (empty is fine)", m1.QuantLevel)
	}
}

func TestOllamaFetcher_HandlesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := &ollamaFetcher{}
	_, err := f.FetchModels(context.Background(), RuntimeInfo{DefaultBaseURL: srv.URL})
	if err == nil {
		t.Error("expected error on 500 response")
	}
}

// TestParseParamCount tests various parameter size strings.
func TestParseParamCount(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"7B", 7},
		{"13B", 13},
		{"70B", 70},
		{"1.5B", 1.5},
		{"400M", 0.4},
		{"", 0},
		{"unknown", 0},
	}
	for _, c := range cases {
		got := parseParamCount(c.in)
		if got != c.want {
			t.Errorf("parseParamCount(%q) = %f; want %f", c.in, got, c.want)
		}
	}
}
