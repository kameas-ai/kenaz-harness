// Built-in tool wiring. Holds the chassis-side registration of the
// in-binary tools (websearch, bash) onto the toolloop's BuiltinRegistry
// so the chat surface can reach them. Gating is owned by the Settings
// store: the toolloop's EnabledFilter consults the predicate here on
// every tool listing / dispatch, so toggling Settings.WebSearchEnabled
// or Settings.BashEnabled in the UI takes effect on the next chat turn
// without a process restart.
package rpc

import (
	"path/filepath"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	corebash "github.com/sigil-tech/kaneaz-harness/core/tools/bash"
	corewebsearch "github.com/sigil-tech/kaneaz-harness/core/tools/websearch"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// registerBuiltinTools installs the in-binary tools into the registry.
// The Settings store gates dispatch via the EnabledFilter; the
// registry holds every tool unconditionally so toggling a setting ON
// surfaces it without a re-registration roundtrip.
//
// nil core is tolerated: the websearch tool is process-local (no
// dependency on chassis state) and the bash tool falls back to a
// process tempdir when no DataDir is available.
func registerBuiltinTools(c *core.Core, registry *toolloop.BuiltinRegistry, bashStore *corebash.Store) {
	if registry == nil {
		return
	}

	// websearch: always wireable; the Cedar gate is bound to AllowAll
	// at boot. A future engine-load path swaps the gate via
	// NewFetcher's PolicyGate field. The websearch.New constructor
	// requires Aggregator/Fetcher/Extractor — we use the package's
	// shipped defaults.
	if ws := constructWebSearch(); ws != nil {
		registry.Register(ws)
	}

	// bash: requires a sandbox root. Prefer <DataDir>/agent-workspace
	// when available; fall back to the OS tempdir so tests + nil-core
	// callers still get a working tool.
	sandboxRoot := defaultBashSandbox(c)
	bashTool := corebash.New(corebash.Options{
		SandboxRoot: sandboxRoot,
		Store:       bashStore,
	})
	registry.Register(bashTool)
}

// constructWebSearch builds a websearch.Tool with the package's
// default aggregator/fetcher/extractor wiring. Returns nil if any
// component fails to construct (e.g. malformed proxy URL); the
// chassis silently drops the tool in that case so the chat surface
// stays usable.
func constructWebSearch() *corewebsearch.Tool {
	fetcher, err := corewebsearch.NewFetcher(corewebsearch.FetcherOpts{
		PolicyGate: cedar.AllowAll{},
	})
	if err != nil {
		return nil
	}
	backends := []corewebsearch.Backend{
		corewebsearch.NewDuckDuckGoBackend(),
		corewebsearch.NewWikipediaBackend(),
	}
	aggregator := corewebsearch.NewAggregator(backends, nil)
	extractor := corewebsearch.NewExtractor()
	return corewebsearch.New(corewebsearch.Options{
		Aggregator: aggregator,
		Fetcher:    fetcher,
		Extractor:  extractor,
	})
}

// defaultBashSandbox returns the workspace path the bash tool uses as
// its sandbox root. Prefers <DataDir>/agent-workspace; falls back to
// /tmp/kaneaz-bash for the test harness path.
func defaultBashSandbox(c *core.Core) string {
	if c != nil && c.DataDir() != "" {
		return filepath.Join(c.DataDir(), "agent-workspace")
	}
	return filepath.Join("/tmp", "kaneaz-bash")
}

// builtinEnabledPredicate returns a func(name) bool that the toolloop
// EnabledFilter consults on every tool listing / dispatch. Maps the
// in-binary tool names onto the corresponding Settings store toggles.
//
// A nil settings impl returns "all enabled" so the test harness path
// (rpc.New(nil)) doesn't accidentally hide tools.
func builtinEnabledPredicate(s *settings.API) func(string) bool {
	if s == nil || s.Store() == nil {
		return func(string) bool { return true }
	}
	return func(name string) bool {
		store := s.Store()
		if store == nil {
			return true
		}
		switch name {
		case corewebsearch.ToolName:
			v, err := store.LoadWebSearch()
			if err != nil {
				return false
			}
			return v
		case corebash.Name:
			v, err := store.LoadBash()
			if err != nil {
				return false
			}
			return v
		}
		return true
	}
}
