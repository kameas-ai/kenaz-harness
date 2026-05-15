package localruntime

import "context"

// ModelFetcher is the interface implemented by per-runtime metadata fetchers.
// Each runtime has its own fetcher that knows how to query the runtime's API
// for the list of locally-available models.
// (local-model-runtimes-01KQ8VMZ WP03)
type ModelFetcher interface {
	// FetchModels returns the list of models available in the given runtime.
	// A partial list may be returned on timeout or after hitting file limits.
	// Implementations must not return an error for a running-but-empty runtime.
	FetchModels(ctx context.Context, info RuntimeInfo) ([]LocalRuntimeModel, error)
}

// fetchers is the registry mapping RuntimeKind → ModelFetcher.
// Populated by WP03's init() calls in each metadata_*.go file.
var fetchers = map[RuntimeKind]ModelFetcher{}

// RegisterFetcher registers a fetcher for a runtime kind. Called from
// each metadata_*.go file's init().
func RegisterFetcher(kind RuntimeKind, f ModelFetcher) {
	fetchers[kind] = f
}

// FindFetcher returns the fetcher for kind, or nil when none is registered.
func FindFetcher(kind RuntimeKind) ModelFetcher {
	return fetchers[kind]
}
