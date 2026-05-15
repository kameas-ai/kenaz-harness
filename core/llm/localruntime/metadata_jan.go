package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	RegisterFetcher(KindJan, &janFetcher{})
}

// janFetcher fetches the model list from the Jan runtime.
// Jan exposes an OpenAI-compatible /v1/models endpoint.
// It also stores models on disk under ~/jan/models/.
type janFetcher struct{}

// janModelsResp is the JSON shape returned by GET /v1/models.
type janModelsResp struct {
	Data []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

func (f *janFetcher) FetchModels(ctx context.Context, info RuntimeInfo) ([]LocalRuntimeModel, error) {
	// Try HTTP endpoint first.
	if models, err := fetchJanHTTP(ctx, info.DefaultBaseURL); err == nil {
		return models, nil
	}
	// Fall back to directory walker.
	return walkJanModels(ctx)
}

func fetchJanHTTP(ctx context.Context, baseURL string) ([]LocalRuntimeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/v1/models", baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jan /v1/models: status %d", resp.StatusCode)
	}

	var r janModelsResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}

	out := make([]LocalRuntimeModel, 0, len(r.Data))
	for _, d := range r.Data {
		dn := d.Name
		if dn == "" {
			dn = d.ID
		}
		out = append(out, LocalRuntimeModel{
			ID:          d.ID,
			DisplayName: dn,
			QuantLevel:  ParseQuantLevel(d.ID),
		})
	}
	return out, nil
}

// walkJanModels walks the Jan models directory (~/.jan/models on macOS/Linux).
// Handles missing directories gracefully.
func walkJanModels(_ context.Context) ([]LocalRuntimeModel, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}

	// Jan's default model directory on macOS / Linux.
	modelDir := filepath.Join(home, ".jan", "models")
	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return nil, nil
	}

	out := make([]LocalRuntimeModel, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Each subdirectory is a model ID.
		modelID := e.Name()
		// Look for a model.json inside to get size information.
		size := janModelSizeBytes(filepath.Join(modelDir, modelID))
		out = append(out, LocalRuntimeModel{
			ID:          modelID,
			DisplayName: strings.ReplaceAll(modelID, "-", " "),
			SizeBytes:   size,
			QuantLevel:  ParseQuantLevel(modelID),
		})
	}
	return out, nil
}

// janModelSizeBytes sums the sizes of .gguf / .bin files in a Jan model
// directory.
func janModelSizeBytes(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".gguf") && !strings.HasSuffix(name, ".bin") {
			continue
		}
		info, err := e.Info()
		if err == nil {
			total += info.Size()
		}
	}
	return total
}
