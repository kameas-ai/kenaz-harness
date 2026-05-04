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
	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	corebash "github.com/sigil-tech/kaneaz-harness/core/tools/bash"
	corefsrequest "github.com/sigil-tech/kaneaz-harness/core/tools/fsrequest"
	coresaveartifact "github.com/sigil-tech/kaneaz-harness/core/tools/saveartifact"
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
//
// nil artifactsMgr is tolerated too: the save_artifact tool is simply
// not registered when no artifacts manager is wired (test harness
// path). EnabledFilter then naturally returns nil for the tool name.
//
// cedarEngine and promptRegistry are WP03 Cedar gate dependencies.
// Both are nil-tolerant: when nil the bash tool falls back to the
// legacy allowlist-based gate so the test harness path and nil-core
// callers keep working unchanged.
func registerBuiltinTools(
	c *core.Core,
	registry *toolloop.BuiltinRegistry,
	bashStore *corebash.Store,
	artifactsMgr *coreart.Manager,
	store settings.SettingsStore,
	cedarEngine *cedar.Engine,
	promptRegistry *cedar.Registry,
) {
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
		logging.L().Info("rpc.builtins.register", "tool", ws.Name())
	} else {
		logging.L().Warn("rpc.builtins.websearch_construct_failed")
	}

	// bash: requires a sandbox root. Prefer <DataDir>/agent-workspace
	// when available; fall back to the OS tempdir so tests + nil-core
	// callers still get a working tool.
	//
	// CedarEngine and PromptRegistry are wired when the chassis has a
	// real DataDir; nil falls back to the legacy allowlist gate so the
	// test harness path (registerBuiltinTools with nil core) still works.
	sandboxRoot := defaultBashSandbox(c)
	var dataDir string
	if c != nil {
		dataDir = c.DataDir()
	}
	bashTool := corebash.New(corebash.Options{
		SandboxRoot:    sandboxRoot,
		Store:          bashStore,
		CedarEngine:    cedarEngine,
		PromptRegistry: promptRegistry,
		DataDir:        dataDir,
	})
	registry.Register(bashTool)
	logging.L().Info("rpc.builtins.register",
		"tool", bashTool.Name(),
		"sandbox", sandboxRoot,
		"cedar_gate", cedarEngine != nil,
	)

	// save_artifact: pipes (title, content) into the artifact CAS
	// pipeline. Only registered when the chassis wired an artifacts
	// manager (production); the test harness path with nil manager
	// silently skips registration, which the EnabledFilter handles
	// gracefully.
	//
	// The Enabled callback the tool consults is the SAME Settings
	// dial the EnabledFilter consults; passing it here gives us
	// defence-in-depth against stale model-side tool catalogs that
	// fire calls after the user toggled the dial off mid-session.
	if artifactsMgr != nil {
		saveArtifactTool := coresaveartifact.New(coresaveartifact.Options{
			Manager: artifactsMgr,
			Enabled: saveArtifactEnabledLookup(store),
		})
		registry.Register(saveArtifactTool)
		logging.L().Info("rpc.builtins.register", "tool", saveArtifactTool.Name())
	} else {
		logging.L().Info("rpc.builtins.save_artifact_skipped",
			"reason", "no artifacts manager wired")
	}
}

// registerFSRequestTool registers the kaneaz__request_filesystem_access
// built-in after the toolsAPI is wired (must be called after
// newToolsAPI returns). nil registry or nil toolsAPI are no-ops so the
// test harness path stays clean.
func registerFSRequestTool(registry *toolloop.BuiltinRegistry, toolsAPI tools.ToolsAPI) {
	if registry == nil || toolsAPI == nil {
		return
	}
	fsReqTool := corefsrequest.New(corefsrequest.Options{
		Delegate: toolsAPI,
	})
	registry.Register(fsReqTool)
	logging.L().Info("rpc.builtins.register", "tool", fsReqTool.Name())
}

// saveArtifactEnabledLookup returns a closure the saveartifact tool
// consults inside Call to honour the live Settings dial. nil store
// collapses to "always enabled" (test harness path); this matches the
// builtinEnabledPredicate's behaviour for nil settings impl.
func saveArtifactEnabledLookup(store settings.SettingsStore) func() bool {
	if store == nil {
		return func() bool { return true }
	}
	return func() bool {
		v, err := store.LoadSaveArtifactEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.save_artifact_lookup.read_failed", "err", err.Error())
			// Soft-fail to "enabled" so a transient settings-store
			// glitch doesn't disable a default-on tool. The frontend
			// toggle remains the source of truth.
			return true
		}
		return v
	}
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
		logging.L().Info("rpc.builtins.predicate.no_store")
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
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				return false
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v
		case corebash.Name:
			v, err := store.LoadBash()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				return false
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v
		case coresaveartifact.ToolName:
			v, err := store.LoadSaveArtifactEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-on tool: soft-fail to enabled on a transient
				// store error so first-launch ergonomics survive a
				// settings-file glitch.
				return true
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v
		case corefsrequest.ToolName:
			v, err := store.LoadFSRequestAccessEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-on: soft-fail to enabled so the tool works on first launch.
				return true
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v
		}
		return true
	}
}
