package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

func init() {
	RegisterFetcher(KindLMStudio, &lmStudioFetcher{})
}

// lmStudioFetcher fetches the model list from LM Studio.
// LM Studio exposes an OpenAI-compatible /v1/models endpoint when its local
// server is running, and can also be queried via the `lms` CLI.
type lmStudioFetcher struct{}

// lmStudioModelsResp is the OpenAI-compatible /v1/models JSON shape.
type lmStudioModelsResp struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (f *lmStudioFetcher) FetchModels(ctx context.Context, info RuntimeInfo) ([]LocalRuntimeModel, error) {
	// Try the HTTP endpoint first (available when server mode is on).
	if models, err := fetchLMStudioHTTP(ctx, info.DefaultBaseURL); err == nil && len(models) > 0 {
		return models, nil
	}
	// Fall back to the `lms` CLI. Gracefully degrade when not present.
	return fetchLMStudioCLI(ctx)
}

func fetchLMStudioHTTP(ctx context.Context, baseURL string) ([]LocalRuntimeModel, error) {
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
		return nil, fmt.Errorf("lm-studio /v1/models: status %d", resp.StatusCode)
	}

	var r lmStudioModelsResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}

	out := make([]LocalRuntimeModel, 0, len(r.Data))
	for _, d := range r.Data {
		name := d.ID
		if idx := strings.LastIndex(d.ID, "/"); idx >= 0 {
			name = d.ID[idx+1:]
		}
		out = append(out, LocalRuntimeModel{
			ID:          d.ID,
			DisplayName: name,
			QuantLevel:  ParseQuantLevel(d.ID),
		})
	}
	return out, nil
}

// fetchLMStudioCLI attempts to list models via the `lms ls` command.
// Gracefully returns an empty slice (no error) when `lms` is not found.
func fetchLMStudioCLI(ctx context.Context) ([]LocalRuntimeModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	path, err := exec.LookPath("lms")
	if err != nil {
		// lms not installed — graceful degrade.
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, path, "ls", "--json")
	out, err := cmd.Output()
	if err != nil {
		// lms present but failed — still return empty, not an error.
		return nil, nil
	}

	// lms ls --json returns a JSON array of model descriptors.
	// Structure varies by version; we extract "id" and "size" fields.
	var entries []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, nil
	}

	models := make([]LocalRuntimeModel, 0, len(entries))
	for _, e := range entries {
		dn := e.Name
		if dn == "" {
			dn = e.ID
		}
		models = append(models, LocalRuntimeModel{
			ID:          e.ID,
			DisplayName: dn,
			SizeBytes:   e.Size,
			QuantLevel:  ParseQuantLevel(e.ID),
		})
	}
	return models, nil
}
