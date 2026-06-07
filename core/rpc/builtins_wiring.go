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

	"github.com/kameas-ai/kenaz-harness/core"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	corefsbuiltins "github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
	corefsrequest "github.com/kameas-ai/kenaz-harness/core/tools/fsrequest"
	coresaveartifact "github.com/kameas-ai/kenaz-harness/core/tools/saveartifact"
	coreskilltool "github.com/kameas-ai/kenaz-harness/core/tools/skill"
	coresleep "github.com/kameas-ai/kenaz-harness/core/tools/sleep"
	coretodo "github.com/kameas-ai/kenaz-harness/core/tools/todo"
	coreupdateartifact "github.com/kameas-ai/kenaz-harness/core/tools/updateartifact"
	corewebsearch "github.com/kameas-ai/kenaz-harness/core/tools/websearch"
	coreaskuser "github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	corelistsecrets "github.com/kameas-ai/kenaz-harness/core/tools/listsecrets"
	corewebfetch "github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
	coresecrets "github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	coreplanmode "github.com/kameas-ai/kenaz-harness/core/tools/planmode"
)

// GlobalFSReadSet is the process-global ReadSet shared across all sessions.
// The set tracks which file paths have been read in a given session so the
// edit_file tool can enforce the read-before-edit contract (ErrEditWithoutRead).
// Constructed once at startup; all fs builtin tools bind against this instance.
// (builtin-filesystem-tools-01KR3N4P WP04)
var GlobalFSReadSet = corefsbuiltins.NewReadSet()

// GlobalTodoStore is the process-global per-session todo list store shared
// across all sessions. The store holds the structured task list the model
// writes via kaneaz__todo_write. Session data is evicted when the session
// ends (Drop call from the lifecycle manager).
// (builtin-tools-search-and-elicitation-01KZNP3D WP05)
var GlobalTodoStore = coretodo.NewStore()

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
	elicitAPI *elicitview.API,
	slashDispatch *coreslashcmd.Dispatch,
	exposureIdx *coresecrets.ExposureIndex,
	budget *refs.Budget,
	posture coreplanmode.SessionPostureManager,
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

	// sleep: passive no-side-effect tool; always registered (default-allow,
	// tool.passive Cedar action). Never gated by a Settings toggle.
	// Satisfies FR-009 .. FR-011 (builtin-tools-search-and-elicitation-01KZNP3D WP04).
	sleepTool := coresleep.New()
	registry.Register(sleepTool)
	logging.L().Info("rpc.builtins.register", "tool", sleepTool.Name())

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

	// update_artifact: writes a new version row for an existing artifact.
	// Gated behind the same FSWriteEnabled toggle as the write-family fs
	// builtins — updating content is a write operation. Only registered
	// when an artifacts manager is wired (same nil-guard as save_artifact).
	if artifactsMgr != nil {
		updateArtifactTool := coreupdateartifact.New(coreupdateartifact.Options{
			Updater: artifactsMgr,
			Enabled: fsWriteEnabledLookup(store),
		})
		registry.Register(updateArtifactTool)
		logging.L().Info("rpc.builtins.register", "tool", updateArtifactTool.Name())
	} else {
		logging.L().Info("rpc.builtins.update_artifact_skipped",
			"reason", "no artifacts manager wired")
	}

	// todo_write: session-scoped structured task list. Gated behind the
	// TodoEnabled toggle (default OFF — user opts in from Tools panel).
	// Uses the process-global GlobalTodoStore keyed by session ID.
	// (builtin-tools-search-and-elicitation-01KZNP3D WP05)
	todoTool := coretodo.New(coretodo.Options{
		Store:   GlobalTodoStore,
		Enabled: todoEnabledLookup(store),
	})
	registry.Register(todoTool)
	logging.L().Info("rpc.builtins.register", "tool", todoTool.Name())

	// ask_user_question: interactive elicitation tool (mission
	// ask-user-question-interactive-01KZNP3G, WP01/WP04).
	//
	// The tool is default-on (asking the user a question is low-risk).
	// The Delegate is nil until WP04 wires the elicit RPC bridge; the
	// tool returns errKindNotWired gracefully in that case.
	//
	// elicitAPI is nil only in test-fixture paths (New(nil) + no elicitAPI
	// constructed). In production the API.New() path always constructs it.
	var askDelegate coreaskuser.Delegate
	if elicitAPI != nil {
		askDelegate = elicitAPI
	}
	askTool := coreaskuser.New(coreaskuser.Options{
		Delegate: askDelegate,
	})
	registry.Register(askTool)
	logging.L().Info("rpc.builtins.register",
		"tool", askTool.Name(),
		"delegate_wired", askDelegate != nil,
	)

	// kaneaz__skill: model-invoked skill dispatcher (model-invoked-skills-catalog-01KZNP3E WP02).
	// Default-on: invoking a user-authored skill is low-risk and expected behaviour.
	// The tool is nil-safe: when slashDispatch is nil (test harness path, no
	// real Core) it returns a friendly "not configured" error rather than
	// panicking — so we always register it.
	skillTool := coreskilltool.New(coreskilltool.Options{
		Dispatch: slashDispatch,
	})
	registry.Register(skillTool)
	logging.L().Info("rpc.builtins.register",
		"tool", skillTool.Name(),
		"dispatch_wired", slashDispatch != nil,
	)

	// kaneaz__list_secrets: lists secrets exposed to the model (model-secret-references-01KW7M5A WP06/WP10).
	// Always registered when an ExposureIndex is wired; nil index is a
	// no-op so the test harness path (nil exposureIdx) stays clean.
	if exposureIdx != nil {
		listSecretsTool := corelistsecrets.New(corelistsecrets.Options{
			Index:  exposureIdx,
			Budget: budget,
		})
		registry.Register(listSecretsTool)
		logging.L().Info("rpc.builtins.register",
			"tool", listSecretsTool.Name(),
			"budget_wired", budget != nil,
		)
	} else {
		logging.L().Info("rpc.builtins.list_secrets_skipped",
			"reason", "no exposure index wired")
	}

	// kaneaz__web_fetch: makes authenticated HTTP requests on behalf of
	// the model (model-secret-references-01KW7M5A WP07). Always registered;
	// the tool resolves @secret: references at request time via the
	// per-context Resolver so no plaintext enters the conversation context.
	webFetchTool := corewebfetch.New(corewebfetch.Options{})
	registry.Register(webFetchTool)
	logging.L().Info("rpc.builtins.register", "tool", webFetchTool.Name())

	// kaneaz__subagent_dispatch: model-callable sub-agent spawner
	// (branch-subagent-interactive-01KZNP3B WP03). Always registered.
	// The BranchSeam is nil until WP07 wires the live seam; the tool
	// returns a clean "seam_not_configured" error in that case so model
	// self-correction is straightforward.
	{
		var dataDir string
		if c != nil {
			dataDir = c.DataDir()
		}
		subagentTool := coresubagent.New(coresubagent.Options{
			DataDir: dataDir,
			Seam:    nil, // wired by branch session manager in WP07
			Tasks:   nil, // wired by background-task-monitor in WP06
		})
		registry.Register(subagentTool)
		logging.L().Info("rpc.builtins.register", "tool", subagentTool.Name())
	}

	// kaneaz__enter_plan_mode / kaneaz__exit_plan_mode: plan-mode posture
	// tools (plan-mode-posture-01KZNP3F WP03/WP04). Both require a
	// SessionPostureManager to read and mutate the session's autonomy layer.
	// ExitTool additionally requires an ArtifactCapturer to persist the
	// plan artifact. Both are always registered when a posture manager is
	// wired; nil posture silently skips registration (test harness path).
	if posture != nil {
		enterTool := coreplanmode.NewEnterTool(coreplanmode.EnterOptions{
			Posture: posture,
		})
		registry.Register(enterTool)
		logging.L().Info("rpc.builtins.register", "tool", enterTool.Name())

		if artifactsMgr != nil {
			exitTool := coreplanmode.NewExitTool(coreplanmode.ExitOptions{
				Posture:   posture,
				Artifacts: artifactsMgr,
			})
			registry.Register(exitTool)
			logging.L().Info("rpc.builtins.register", "tool", exitTool.Name())
		} else {
			logging.L().Info("rpc.builtins.exit_plan_mode_skipped",
				"reason", "no artifacts manager wired")
		}
	} else {
		logging.L().Info("rpc.builtins.plan_mode_tools_skipped",
			"reason", "no posture manager wired")
	}
}

// fsWriteEnabledLookup returns a closure the update_artifact tool
// consults inside Call to honour the live FSWrite Settings dial. nil
// store collapses to "disabled" — correct default-off posture for any
// write tool. This mirrors the RegisterFSBuiltinTools gate.
func fsWriteEnabledLookup(store settings.SettingsStore) func() bool {
	if store == nil {
		return func() bool { return false }
	}
	return func() bool {
		v, err := store.LoadFSWriteEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.update_artifact_lookup.read_failed", "err", err.Error())
			return false
		}
		return v
	}
}

// todoEnabledLookup returns a closure the todo_write tool consults inside
// Call to honour the live Todo Settings dial. nil store collapses to
// "disabled" — correct default-off posture. Mirrors the FS write lookup.
// (builtin-tools-search-and-elicitation-01KZNP3D WP05)
func todoEnabledLookup(store settings.SettingsStore) func() bool {
	if store == nil {
		return func() bool { return false }
	}
	return func() bool {
		v, err := store.LoadTodoEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.todo_enabled_lookup.read_failed", "err", err.Error())
			return false
		}
		return v
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

// registerFSBuiltinTools installs the read and write family of builtin
// filesystem tools into the registry. Each tool is gated behind the
// per-family settings dial (LoadFSReadEnabled / LoadFSWriteEnabled) so
// the Tools panel toggles take effect on the next chat turn without a
// process restart.
//
// nil registry is a no-op so the test harness path stays clean. nil
// cedarEngine falls back to AllowAll (same pattern as websearch).
// nil store falls back to "disabled" (correct default-off posture for
// filesystem write access).
//
// (builtin-filesystem-tools-01KR3N4P WP02–WP06)
func registerFSBuiltinTools(
	registry *toolloop.BuiltinRegistry,
	cedarEngine *cedar.Engine,
	store settings.SettingsStore,
) {
	if registry == nil {
		return
	}

	// Gate: construct a *corefs.Gate with the existing Cedar engine.
	// When cedarEngine is nil we pass cedar.AllowAll{} explicitly so the
	// gate's GateOptions.Engine field is a non-nil interface and the Gate
	// doesn't need to special-case a nil *Engine pointer vs a nil interface.
	var fsGateEngine cedar.Gate = cedar.AllowAll{}
	if cedarEngine != nil {
		fsGateEngine = cedarEngine
	}
	gate := corefs.NewGate(corefs.GateOptions{
		Engine: fsGateEngine,
	})

	// Per-family enabled closures. Soft-fail to false on transient store
	// errors so filesystem tools stay off rather than on by default.
	fsReadEnabled := func() bool {
		if store == nil {
			return false
		}
		v, err := store.LoadFSReadEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.fs_read_enabled.read_failed", "err", err.Error())
			return false
		}
		return v
	}
	fsWriteEnabled := func() bool {
		if store == nil {
			return false
		}
		v, err := store.LoadFSWriteEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.fs_write_enabled.read_failed", "err", err.Error())
			return false
		}
		return v
	}

	opts := corefsbuiltins.Options{
		Gate:         gate,
		ReadSet:      GlobalFSReadSet,
		ReadEnabled:  fsReadEnabled,
		WriteEnabled: fsWriteEnabled,
	}

	fsTools := []toolloop.BuiltinTool{
		corefsbuiltins.NewReadFileTool(opts),
		corefsbuiltins.NewListDirTool(opts),
		corefsbuiltins.NewGlobTool(opts),
		corefsbuiltins.NewGrepTool(opts),
		corefsbuiltins.NewWriteFileTool(opts),
		corefsbuiltins.NewEditFileTool(opts),
		corefsbuiltins.NewListOpenWorklistTool(opts),
	}
	for _, tool := range fsTools {
		registry.Register(tool)
		logging.L().Info("rpc.builtins.register", "tool", tool.Name())
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

		// ── Builtin filesystem tools (builtin-filesystem-tools-01KR3N4P) ──
		// Read-family tools: default OFF until the user opts in from the Tools panel.
		case corefsbuiltins.NameReadFile,
			corefsbuiltins.NameListDir,
			corefsbuiltins.NameGlob,
			corefsbuiltins.NameGrep,
			corefsbuiltins.NameListOpenWorklist:
			v, err := store.LoadFSReadEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-off: soft-fail to disabled so disk access stays off.
				return false
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v

		// Write-family tools: default OFF until the user opts in from the Tools panel.
		// update_artifact is gated by the same FSWriteEnabled toggle.
		case corefsbuiltins.NameWriteFile, corefsbuiltins.NameEditFile,
			coreupdateartifact.ToolName:
			v, err := store.LoadFSWriteEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-off: soft-fail to disabled.
				return false
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v

		// ── Todo tool (builtin-tools-search-and-elicitation-01KZNP3D) ──
		// Default OFF until the user opts in from the Tools panel.
		case coretodo.ToolName:
			v, err := store.LoadTodoEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-off: soft-fail to disabled.
				return false
			}
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", v)
			return v

		// ── Passive tools (builtin-tools-search-and-elicitation-01KZNP3D WP04) ──
		// Sleep is always-on: it has no side effects and must remain available
		// for __monitor watch patterns regardless of Settings dials.
		case coresleep.ToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		// ── Skill tool (model-invoked-skills-catalog-01KZNP3E) ──
		// Default ON: invoking a user-authored skill is expected behaviour.
		// The tool is registered unconditionally; the dispatch layer enforces
		// model_invokable=true at resolution time so only eligible commands run.
		case coreskilltool.ToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		// ── Subagent dispatch tool (branch-subagent-interactive-01KZNP3B) ──
		// Default ON: the model dispatching sub-agents is the primary use case.
		// The tool degrades gracefully when the BranchSeam is not yet wired.
		case coresubagent.ToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		// ── Plan-mode tools (plan-mode-posture-01KZNP3F) ──
		// Always-on: these are posture-management tools with no dangerous
		// side-effects on their own. The Cedar gate enforces write restrictions
		// while plan_mode is active; the tools themselves are always enabled.
		case coreplanmode.EnterToolName, coreplanmode.ExitToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true
		}
		return true
	}
}
