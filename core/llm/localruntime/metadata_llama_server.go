package localruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	RegisterFetcher(KindLlamaServer, &llamaServerFetcher{})
}

const (
	llamaServerMaxFiles   = 500
	llamaServerMaxWallclock = 3 * time.Second
)

// llamaServerFetcher fetches the model list from llama-server.
// llama-server's /v1/models endpoint only exposes the currently-loaded model.
// We walk common model directories to find all available GGUF files.
type llamaServerFetcher struct{}

// llamaServerModelResp is the OpenAI-compatible /v1/models JSON shape.
type llamaServerModelResp struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (f *llamaServerFetcher) FetchModels(ctx context.Context, info RuntimeInfo) ([]LocalRuntimeModel, error) {
	// First: try the /v1/models endpoint to get the currently loaded model.
	active, _ := fetchLlamaServerActive(ctx, info.DefaultBaseURL)

	// Second: walk common model directories for available GGUF files.
	walked, err := walkLlamaServerDirs(ctx)
	if err != nil {
		// Return partial (active only) — not a fatal error.
		if len(active) > 0 {
			return active, nil
		}
		return nil, nil
	}

	// Merge: active model first, then walked files (dedup by ID).
	seen := map[string]bool{}
	out := make([]LocalRuntimeModel, 0, len(active)+len(walked))
	for _, m := range active {
		if !seen[m.ID] {
			out = append(out, m)
			seen[m.ID] = true
		}
	}
	for _, m := range walked {
		if !seen[m.ID] {
			out = append(out, m)
			seen[m.ID] = true
		}
	}
	return out, nil
}

func fetchLlamaServerActive(ctx context.Context, baseURL string) ([]LocalRuntimeModel, error) {
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

	var r llamaServerModelResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	out := make([]LocalRuntimeModel, 0, len(r.Data))
	for _, d := range r.Data {
		out = append(out, LocalRuntimeModel{
			ID:          d.ID,
			DisplayName: filepath.Base(d.ID),
			QuantLevel:  ParseQuantLevel(d.ID),
		})
	}
	return out, nil
}

// walkLlamaServerDirs walks well-known model directories for GGUF files.
// Bails at llamaServerMaxFiles files or llamaServerMaxWallclock wallclock.
// The dirs are resolved via the package-level llamaServerModelDirs variable
// so tests can override without touching the filesystem.
func walkLlamaServerDirs(ctx context.Context) ([]LocalRuntimeModel, error) {
	dirs := llamaServerModelDirs()

	deadline := time.Now().Add(llamaServerMaxWallclock)
	fileCount := 0
	var out []LocalRuntimeModel

	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip inaccessible paths
			}
			if time.Now().After(deadline) {
				return fs.SkipAll
			}
			if fileCount >= llamaServerMaxFiles {
				return fs.SkipAll
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".gguf") {
				return nil
			}
			fileCount++
			info, _ := d.Info()
			var size int64
			if info != nil {
				size = info.Size()
			}
			name := strings.TrimSuffix(d.Name(), ".gguf")
			out = append(out, LocalRuntimeModel{
				ID:          path,
				DisplayName: name,
				SizeBytes:   size,
				QuantLevel:  ParseQuantLevel(name),
			})
			return nil
		})
		if err != nil {
			continue
		}
		// Check context cancellation between directories.
		select {
		case <-ctx.Done():
			return out, nil
		default:
		}
	}
	return out, nil
}

// llamaServerModelDirs is the injectable resolver for the set of directories
// to walk when looking for GGUF files. Tests override this variable.
var llamaServerModelDirs = realLlamaServerModelDirs

// realLlamaServerModelDirs is the production implementation.
func realLlamaServerModelDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, "models"),
		filepath.Join(home, ".local", "share", "llama.cpp", "models"),
		"/usr/local/share/llama.cpp/models",
		"/opt/llama.cpp/models",
	}
}
