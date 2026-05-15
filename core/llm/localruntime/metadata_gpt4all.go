package localruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	RegisterFetcher(KindGPT4All, &gpt4AllFetcher{})
}

// gpt4AllFetcher walks the GPT4All models directory for available model files.
// GPT4All does not expose an HTTP API for model listing; it stores models in
// a well-known local directory.
type gpt4AllFetcher struct{}

func (f *gpt4AllFetcher) FetchModels(_ context.Context, _ RuntimeInfo) ([]LocalRuntimeModel, error) {
	return walkGPT4AllModels()
}

// gpt4AllModelDirs returns the candidate directories for GPT4All models.
func gpt4AllModelDirs() []string {
	home, _ := os.UserHomeDir()
	return []string{
		// macOS
		filepath.Join(home, "Library", "Application Support", "nomic.ai", "GPT4All"),
		// Linux (common)
		filepath.Join(home, ".local", "share", "nomic.ai", "GPT4All"),
		filepath.Join(home, ".gpt4all", "models"),
		// Windows
		filepath.Join(home, "AppData", "Local", "nomic.ai", "GPT4All"),
	}
}

func walkGPT4AllModels() ([]LocalRuntimeModel, error) {
	var out []LocalRuntimeModel
	for _, dir := range gpt4AllModelDirs() {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			lower := strings.ToLower(name)
			if !strings.HasSuffix(lower, ".gguf") && !strings.HasSuffix(lower, ".bin") {
				continue
			}
			info, err := e.Info()
			var size int64
			if err == nil {
				size = info.Size()
			}
			displayName := strings.TrimSuffix(name, filepath.Ext(name))
			out = append(out, LocalRuntimeModel{
				ID:          filepath.Join(dir, name),
				DisplayName: displayName,
				SizeBytes:   size,
				QuantLevel:  ParseQuantLevel(name),
			})
		}
	}
	return out, nil
}
