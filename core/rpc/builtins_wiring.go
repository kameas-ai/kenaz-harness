// Built-in tool wiring. Holds the chassis-side registration of the
// in-binary tools (websearch, bash) onto the toolloop's BuiltinRegistry
// so the chat surface can reach them. Gating is owned by the Settings
// store: the toolloop's EnabledFilter consults the predicate here on
// every tool listing / dispatch, so toggling Settings.WebSearchEnabled
// or Settings.BashEnabled in the UI takes effect on the next chat turn
// without a process restart.
package rpc

import (
	"context"
	"io"
	"path/filepath"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
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
	corereadctx "github.com/kameas-ai/kenaz-harness/core/tools/readcontextfile"
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
// writes via kenaz__todo_write. Session data is evicted when the session
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
	// taskReg is the process's background-task registry
	// (subagent-control-and-background-tasks-01PMZB11 UNIT-3). nil
	// means run_in_background falls back to synchronous execution (test
	// harness path, same as before this parameter existed) and
	// kenaz__monitor is not registered (see the monitor block below).
	taskReg *coretasks.Registry,
) {
	if registry == nil {
		return
	}

	// websearch: always wireable. The Cedar gate is the real engine
	// when the chassis has one (nil-core / test path falls back to
	// AllowAll) and covers both the query leg and the page-fetch leg —
	// see constructWebSearch. The websearch.New constructor requires
	// Aggregator/Fetcher/Extractor — we use the package's shipped
	// defaults.
	if ws := constructWebSearch(cedarEngine); ws != nil {
		registry.Register(ws)
		logging.L().Info("rpc.builtins.register",
			"tool", ws.Name(),
			"cedar_gate", cedarEngine != nil,
		)
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
	// Background-task seam (subagent-control-and-background-tasks-
	// 01PMZB11 UNIT-3). Only wired when taskReg is non-nil — the same
	// nil-tolerant contract every other optional dependency in this
	// file follows, and bash.go's own schema selection already keys off
	// exactly this: BackgroundSpawn == nil means the model is never
	// offered run_in_background, so it must actually be nil (not a
	// func wrapping a nil registry) when taskReg is nil.
	var bgSpawn corebash.BackgroundSpawnFunc
	var bgWriters corebash.BackgroundWritersFunc
	var bgSetPID corebash.BackgroundSetPIDFunc
	var bgEnd corebash.BackgroundEndFunc
	if taskReg != nil {
		bgSpawn = func(ctx context.Context, sessionID, cmd, description string, pid int) (string, error) {
			return taskReg.Register(ctx, coretasks.RegisterOpts{
				Kind:           coretasks.KindBash,
				OwnerSessionID: sessionID,
				Cmd:            cmd,
				Description:    description,
				PID:            pid,
			})
		}
		bgWriters = func(taskID string) (io.Writer, io.Writer, bool) {
			stdout, ok1 := taskReg.StdoutWriter(taskID)
			stderr, ok2 := taskReg.StderrWriter(taskID)
			if !ok1 || !ok2 {
				return nil, nil, false
			}
			return stdout, stderr, true
		}
		bgSetPID = func(taskID string, pid int) {
			taskReg.SetPID(taskID, pid)
		}
		bgEnd = func(ctx context.Context, taskID string, exitCode int) {
			_ = taskReg.End(ctx, taskID, exitCode)
		}
	}
	bashTool := corebash.New(corebash.Options{
		SandboxRoot:       sandboxRoot,
		Store:             bashStore,
		CedarEngine:       cedarEngine,
		PromptRegistry:    promptRegistry,
		DataDir:           dataDir,
		BackgroundSpawn:   bgSpawn,
		BackgroundWriters: bgWriters,
		BackgroundSetPID:  bgSetPID,
		BackgroundEnd:     bgEnd,
		// UNIT-3: was never wired, so every background task's
		// OwnerSessionID was permanently empty — Tasks_ListBySession /
		// Tasks_AbortBySession (the session-close-dialog surface, §14
		// E-004) could never find a session's own background tasks.
		SessionIDFromCtx: toolloop.SessionIDFromContext,
		// Unwired sweep 2026-08-14: this was never passed, so the
		// Settings dial was permanently false in every shipped build
		// while BashPermissionModal.vue kept offering "Allow always"
		// for dangerous commands. The user granted it, the backend
		// demoted it to AllowOnce, and the next identical command
		// re-prompted with no explanation.
		PermissionCacheDangerousOps: dangerousOpsCacheLookup(store),
		// trust-surfaces-that-fire-01PMZ202 WP22 / UNIT-20 (FR-017):
		// Logger was never set, so t.logf returned early at every one
		// of bash's 18 Cedar-gate log sites (bash.go:734) — including
		// every gate decision and a swallowed snippet-write failure, in
		// every shipped build. Logger: logging.L() matches the pattern
		// every sibling tool in this file already uses.
		Logger: logging.L(),
	})
	registry.Register(bashTool)
	workspaceSource := "none"
	if c != nil {
		workspaceSource = string(c.Workspace().Source)
	}
	logging.L().Info("rpc.builtins.register",
		"tool", bashTool.Name(),
		"sandbox", sandboxRoot,
		"workspace_source", workspaceSource,
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
	//
	// WP04 landed: the Delegate below is the REAL elicit RPC bridge, and
	// calling this tool parks the turn on a channel that only the
	// frontend's AskUserQuestion dialog can release (10-minute deadline in
	// elicitview.API.OpenDialog). A stale comment here once claimed the
	// Delegate was still nil, which made the parked turn read as a benign
	// "not wired" stub; it was not. TestAskUserQuestionDelegateIsWired
	// (builtins_wiring_test.go) pins the wiring so the claim cannot rot
	// back into a lie.
	//
	// elicitAPI is nil only in test-fixture paths (New(nil) + no elicitAPI
	// constructed); there the tool returns errKindNotWired gracefully.
	// In production the API.New() path always constructs it.
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

	// kenaz__skill: model-invoked skill dispatcher (model-invoked-skills-catalog-01KZNP3E WP02).
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

	// kenaz__list_secrets: lists secrets exposed to the model (model-secret-references-01KW7M5A WP06/WP10).
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

	// kenaz__web_fetch: makes authenticated HTTP requests on behalf of
	// the model (model-secret-references-01KW7M5A WP07). Gated behind a
	// Cedar network-policy gate (host allowlist, same pattern as websearch)
	// AND a user-facing Settings toggle (crash-recovery-tool-gating-0XQTC4RK
	// FR-005). Default OFF — the tool resolves @secret: references at
	// request time so the gate must be explicit rather than implicit.
	var webFetchGateEngine cedar.Gate = cedar.AllowAll{}
	if cedarEngine != nil {
		webFetchGateEngine = cedarEngine
	}
	webFetchTool := corewebfetch.New(corewebfetch.Options{
		Gate:    webFetchGateEngine,
		Enabled: webFetchEnabledLookup(store),
	})
	registry.Register(webFetchTool)
	logging.L().Info("rpc.builtins.register",
		"tool", webFetchTool.Name(),
		"cedar_gate", cedarEngine != nil,
	)

	// kenaz__subagent_dispatch: model-callable sub-agent spawner
	// (branch-subagent-interactive-01KZNP3B WP03).
	//
	// NOT registered when Seam is nil (crash-recovery-tool-gating-0XQTC4RK
	// FR-007): advertising a tool that always returns seam_not_configured
	// wastes model turns and creates confusing failures. Skip registration
	// until the live BranchSeam is wired. The tool will appear in the
	// catalog (and in the predicate switch) only when Seam is non-nil.
	//
	// READ THIS BEFORE "FIXING" THE NIL (unwired sweep, 2026-08-14).
	// A real BranchSeam already exists and is already wired into the
	// kernel: core/rpc/api.go's newGraphManagerWithDeps sets
	// `deps.Branch = graphview.NewBranchSeamAdapter(convMgr, ...)`.
	// Pointing subagentSeam at it would NOT make this tool work. That
	// adapter is storage-only by design — its own doc says it "does NOT
	// spawn a child kernel run by itself… A future v2 will thread a
	// child run-spawner through this seam — captured as a hook
	// (RunSpawner) but unwired for v1" — and WaitForChildRun is a
	// `return nil` no-op. Registering the tool against it would give the
	// model a dispatcher that forks a session nothing ever executes and
	// then reports success, which is a worse failure than the tool being
	// absent.
	//
	// The guard comes out when a child RUN SPAWNER exists, not when a
	// seam does. Same condition clears the `branch` / `merge` lines in
	// scripts/ci/allowlists/i3-unexercised-kinds.txt.
	{
		var subagentSeam agentgraph.BranchSeam // nil — no child-run spawner yet
		if subagentSeam != nil {
			var dataDir string
			if c != nil {
				dataDir = c.DataDir()
			}
			subagentTool := coresubagent.New(coresubagent.Options{
				DataDir: dataDir,
				Seam:    subagentSeam,
				Tasks:   nil, // wired by background-task-monitor in WP06
			})
			registry.Register(subagentTool)
			logging.L().Info("rpc.builtins.register", "tool", subagentTool.Name())
		} else {
			logging.L().Info("rpc.builtins.subagent_dispatch_skipped",
				"reason", "BranchSeam not yet wired — tool omitted from model catalog (FR-007)",
			)
		}
	}

	// kenaz__enter_plan_mode / kenaz__exit_plan_mode: plan-mode posture
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

// webFetchEnabledLookup returns a closure kenaz__web_fetch consults inside Call
// to honour the live WebFetch Settings toggle. nil store collapses to "disabled"
// — correct fail-closed posture for a network-capable tool.
// (crash-recovery-tool-gating-0XQTC4RK FR-005)
func webFetchEnabledLookup(store settings.SettingsStore) func() bool {
	if store == nil {
		return func() bool { return false }
	}
	return func() bool {
		v, err := store.LoadWebFetchEnabled()
		if err != nil {
			logging.L().Warn("rpc.builtins.web_fetch_enabled_lookup.read_failed", "err", err.Error())
			// Default-off: soft-fail to disabled so the network tool stays off.
			return false
		}
		return v
	}
}

// dangerousOpsCacheLookup returns the closure the bash tool consults at
// gate time to decide whether an AllowAlways grant on a dangerous-tier
// command may be persisted as a .cedar snippet. nil store, or a read
// error, collapses to false — the same fail-closed posture the demotion
// path has always had. Read live rather than at construction because the
// dial is user-toggleable and the permission modal renders from it.
func dangerousOpsCacheLookup(store settings.SettingsStore) func() bool {
	if store == nil {
		return func() bool { return false }
	}
	return func() bool {
		v, err := store.LoadPermissionCacheDangerousOps()
		if err != nil {
			logging.L().Warn("rpc.builtins.permission_cache_dangerous_ops_lookup.read_failed", "err", err.Error())
			return false
		}
		return v
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

// registerFSRequestTool registers the kenaz__request_filesystem_access
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
// filesystem write access). nil promptRegistry / empty dataDir degrade
// the Prompter/PolicyDir fields to their own safe defaults (NoOpPrompter,
// snippet persistence disabled) — same nil-tolerant shape as the bash
// tool's own PromptRegistry/DataDir wiring above.
//
// (builtin-filesystem-tools-01KR3N4P WP02–WP06;
// trust-surfaces-that-fire-01PMZ202 WP22 / UNIT-20 adds Prompter +
// PolicyDir, C-16: both land in the same commit because PolicyDir's
// two call sites are unreachable until a real Prompter exists — a
// Prompter landing alone makes "Allow always" silently degrade to
// allow-once, re-prompting forever.)
func registerFSBuiltinTools(
	registry *toolloop.BuiltinRegistry,
	cedarEngine *cedar.Engine,
	store settings.SettingsStore,
	promptRegistry *cedar.Registry,
	dataDir string,
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
	var policyDir string
	if dataDir != "" {
		policyDir = filepath.Join(dataDir, cedar.PolicyDir)
	}
	gate := corefs.NewGate(corefs.GateOptions{
		Engine: fsGateEngine,
		// B-4/R-01: the interactive arm — an adapter over
		// cedar.Registry.RequestInteractive, the same registry bash
		// already prompts through, so an attended user gets the modal
		// bash already has. promptRegistry is the process-shared
		// singleton (nil only on the test-harness / nil-core path);
		// corefs.CedarPrompter degrades a nil Registry to PromptDeny,
		// i.e. NoOpPrompter's own behaviour — the "interactive when a
		// channel is attached, NoOpPrompter otherwise" composite from
		// the coordination note falls out of that nil-tolerance for
		// free, with no explicit fallback chain to write here.
		Prompter: &corefs.CedarPrompter{Registry: promptRegistry},
		// C-16: PolicyDir was doubly dead (unset here, and both write
		// call sites sat inside switch arms NoOpPrompter could never
		// reach). Wiring the Prompter alone would silently degrade
		// every "Allow always" grant to allow-once. Same
		// <DataDir>/policy convention bash's own snippet writer uses
		// (bash.go:598, cedar.PolicyDir).
		PolicyDir: policyDir,
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
//
// cedarEngine is the production policy engine (nil on the test-chassis
// / nil-core path). It is threaded into BOTH legs of the tool:
//
//   - the Fetcher, which retrieves each result page, and
//   - each Backend, which issues the query itself.
//
// Both legs matter. Before this was wired the Fetcher took a hardcoded
// AllowAll and the backends took no gate at all, so a user's
// `forbid network_request` policy was ignored end to end. Same nil-safe
// shape as the web_fetch gate above.
func constructWebSearch(cedarEngine *cedar.Engine) *corewebsearch.Tool {
	var gate cedar.Gate = cedar.AllowAll{}
	if cedarEngine != nil {
		gate = cedarEngine
	}
	fetcher, err := corewebsearch.NewFetcher(corewebsearch.FetcherOpts{
		PolicyGate: gate,
	})
	if err != nil {
		return nil
	}
	backends := []corewebsearch.Backend{
		corewebsearch.NewDuckDuckGoBackend(corewebsearch.WithDuckDuckGoGate(gate)),
		corewebsearch.NewWikipediaBackend(corewebsearch.WithWikipediaGate(gate)),
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
// its sandbox root: the core's RESOLVED agent workspace (spec 089 —
// the granted /workspace mount in a workbench, <DataDir>/agent-workspace
// otherwise). Falls back to /tmp/kenaz-bash for the nil-core test path.
func defaultBashSandbox(c *core.Core) string {
	if c != nil && c.WorkspaceDir() != "" {
		return c.WorkspaceDir()
	}
	return filepath.Join("/tmp", "kenaz-bash")
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
		// ── web_fetch (crash-recovery-tool-gating-0XQTC4RK FR-005) ──
		// Default OFF: the tool makes outbound HTTP requests; the user must
		// explicitly opt in. Cedar network policy applies at call time regardless.
		case corewebfetch.ToolName:
			v, err := store.LoadWebFetchEnabled()
			if err != nil {
				logging.L().Warn("rpc.builtins.predicate.read_failed",
					"tool", name, "err", err.Error())
				// Default-off: fail to disabled on a transient settings error.
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

		// ── Plan-mode tools (plan-mode-posture-01KZNP3F) ──
		// Always-on: these are posture-management tools with no dangerous
		// side-effects on their own. The Cedar gate enforces write restrictions
		// while plan_mode is active; the tools themselves are always enabled.
		case coreplanmode.EnterToolName, coreplanmode.ExitToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		// ── read_context_file (unified-context-artifacts-01NCTXU01) ──
		// Always-on: reading context module files on demand is low-risk and
		// is the expected companion behaviour to module attachment. Path-
		// confinement enforced within the tool itself (module root boundary).
		case corereadctx.ToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		// ── list_secrets (model-secret-references-01KW7M5A) ──
		// Always-on when registered (the registration guard is the nil
		// exposureIdx check; once registered it should always be usable).
		case corelistsecrets.ToolName:
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		case coreaskuser.ToolName:
			// ask_user_question (ask-user-question-interactive-01KZNP3G):
			// always-on. This is the interactive elicitation primitive — the
			// model's only way to pause a turn and ask the user a structured
			// question. There is no dedicated Settings toggle for it (unlike
			// bash/web_fetch/etc. it has no dangerous side effect of its
			// own, it just prompts), so there is nothing to gate on; the
			// correct default is always-on. This case was missing, which
			// meant the tool fell through to the fail-closed default below
			// and was denied from every tool catalog (observed twice per
			// request in prod logs as "no explicit predicate case").
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true

		case coresubagent.ToolName:
			// subagent_dispatch (branch-subagent-interactive-01KZNP3B):
			// always-on at this coarse Settings-store gate. Registration
			// itself is already gated behind a non-nil BranchSeam (see
			// registerBuiltinTools above — the seam is hardcoded nil today,
			// so this tool is not currently reachable in production), and
			// per-call authorization is enforced by Cedar's
			// ActionToolSubagentDispatch action once the seam is wired, not
			// by a Settings dial. Added proactively so wiring the seam in a
			// future mission doesn't silently repeat the ask_user_question
			// regression.
			logging.L().Info("rpc.builtins.predicate", "tool", name, "enabled", true)
			return true
		}
		// Fail-closed: an unknown tool name has no explicit predicate case.
		// Deny the tool and emit a WARN so the developer knows to add a case
		// (crash-recovery-tool-gating-0XQTC4RK FR-006).
		logging.L().Warn("rpc.builtins.predicate.unknown_tool",
			"tool", name,
			"action", "deny",
			"reason", "no explicit predicate case — add one to builtinEnabledPredicate",
		)
		return false
	}
}

// registerReadContextFileTool wires the kenaz__read_context_file
// built-in into the registry. The tool allows the agent to read on-demand
// files from attached context modules (unified-context-artifacts-01NCTXU01).
//
// nil registry, nil library, or nil moduleSource are no-ops — the tool is
// not registered when the context library or attachment manager are absent
// (test/nil-core paths).
func registerReadContextFileTool(
	registry *toolloop.BuiltinRegistry,
	lib *corecontexts.Library,
	moduleSource corereadctx.ModuleSource,
) {
	if registry == nil || lib == nil || moduleSource == nil {
		logging.L().Info("rpc.builtins.read_context_file_skipped",
			"reason", "library or module source not wired")
		return
	}
	tool := corereadctx.New(corereadctx.Options{
		Library: lib,
		Modules: moduleSource,
	})
	registry.Register(tool)
	logging.L().Info("rpc.builtins.register", "tool", tool.Name())
}
