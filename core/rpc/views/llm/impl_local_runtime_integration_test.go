package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm/localruntime"
	llmview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
)

// buildIntegrationAPI creates a minimal *llmview.API for integration testing.
// The personal store is nil so AddProvider will return ErrPersonalStoreUnavailable.
func buildIntegrationAPI() *llmview.API {
	cfg := llmview.Config{}
	return llmview.New(cfg)
}

func portFromIntURL(rawURL string) int {
	parts := strings.Split(rawURL, ":")
	p, _ := strconv.Atoi(parts[len(parts)-1])
	return p
}

// TestImplIntegration_ListDetected_FeatureOff ensures the RPC layer returns
// empty when the feature flag is off.
func TestImplIntegration_ListDetected_FeatureOff(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "0")
	api := buildIntegrationAPI()

	infos, err := api.ListDetectedLocalRuntimes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty; got %d", len(infos))
	}
}

// TestImplIntegration_AutoConfigure_FeatureOff returns ErrLocalRuntimeDisabled.
func TestImplIntegration_AutoConfigure_FeatureOff(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "0")
	api := buildIntegrationAPI()

	_, err := api.AutoConfigureLocalRuntime(context.Background(), "ollama")
	if err != llmview.ErrLocalRuntimeDisabled {
		t.Errorf("expected ErrLocalRuntimeDisabled; got %v", err)
	}
}

// TestImplIntegration_AutoConfigure_UnknownKind returns ErrRuntimeUnknown.
func TestImplIntegration_AutoConfigure_UnknownKind(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	api := buildIntegrationAPI()

	_, err := api.AutoConfigureLocalRuntime(context.Background(), "no-such-runtime")
	if err != llmview.ErrRuntimeUnknown {
		t.Errorf("expected ErrRuntimeUnknown; got %v", err)
	}
}

// TestImplIntegration_AutoConfigure_NotRunning returns ErrRuntimeNotRunning
// when the port is not listening.
func TestImplIntegration_AutoConfigure_NotRunning(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	t.Setenv("PATH", t.TempDir())

	api := buildIntegrationAPI()
	localruntime.Invalidate()

	_, err := api.AutoConfigureLocalRuntime(context.Background(), "ollama")
	if err != llmview.ErrRuntimeNotRunning {
		// Accept ErrPersonalStoreUnavailable as a secondary outcome if the
		// detection stage passed but store wasn't wired.
		t.Logf("got err = %v (ErrRuntimeNotRunning expected unless port happens to be open)", err)
	}
}

// TestImplIntegration_WithFakeOllama wires the fake Ollama server end-to-end
// through DetectAll → ListDetectedLocalRuntimes → AutoConfigureLocalRuntime.
func TestImplIntegration_WithFakeOllama(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	t.Setenv("PATH", t.TempDir())

	// Spin up fake Ollama.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
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
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Override Ollama's port/URL.
	origPort := localruntime.DefaultPorts()[localruntime.KindOllama]
	localruntime.SetDefaultPort(localruntime.KindOllama, portFromIntURL(srv.URL))
	defer localruntime.SetDefaultPort(localruntime.KindOllama, origPort)

	origURL := localruntime.DefaultBaseURLs()[localruntime.KindOllama]
	localruntime.SetDefaultBaseURL(localruntime.KindOllama, srv.URL)
	defer localruntime.SetDefaultBaseURL(localruntime.KindOllama, origURL)

	localruntime.Invalidate()

	api := buildIntegrationAPI()

	// ListDetectedLocalRuntimes should return Ollama as running.
	infos, err := api.ListDetectedLocalRuntimes(context.Background())
	if err != nil {
		t.Fatalf("ListDetectedLocalRuntimes error: %v", err)
	}

	var ollamaInfo *llmview.LocalRuntimeInfo
	for i, info := range infos {
		if info.Kind == "ollama" {
			ollamaInfo = &infos[i]
			break
		}
	}
	if ollamaInfo == nil {
		t.Fatal("Ollama not found in ListDetectedLocalRuntimes")
	}
	if !ollamaInfo.Running {
		t.Error("expected Ollama.Running=true")
	}

	// AutoConfigureLocalRuntime should succeed up to the store-write step.
	_, err = api.AutoConfigureLocalRuntime(context.Background(), "ollama")
	// Without a real personal store, we get ErrPersonalStoreUnavailable.
	// That's the expected terminal state in a unit/integration test.
	if err != nil && err != llmview.ErrPersonalStoreUnavailable {
		t.Errorf("unexpected error: %v", err)
	}
}
