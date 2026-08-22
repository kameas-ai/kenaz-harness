package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coreaskuser "github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
	corewebfetch "github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
	corewebsearch "github.com/kameas-ai/kenaz-harness/core/tools/websearch"
)

// TestBuiltinEnabledPredicate_UnknownToolIsDenied asserts that an unknown tool
// name (one with no explicit predicate case) is denied with a WARN rather than
// silently enabled (FR-006 — fail-closed predicate default).
func TestBuiltinEnabledPredicate_UnknownToolIsDenied(t *testing.T) {
	t.Parallel()

	// Nil settings API → "all enabled" fast path (test-harness shortcut).
	// This is documented behaviour; the test-harness path with nil settings
	// should not accidentally block tools during development.
	nilPred := builtinEnabledPredicate(nil)
	if !nilPred("some__unknown_tool") {
		t.Error("nil settings API should enable everything (test-harness fast path)")
	}

	// Real settings API backed by a memoryStore → unknown tool is denied.
	api := settings.NewAPI(nil) // nil → memoryStore
	realPred := builtinEnabledPredicate(api)
	if realPred("totally__unknown_tool_xyz") {
		t.Error("unknown tool name should be denied by the fail-closed default (FR-006)")
	}
}

// TestBuiltinEnabledPredicate_KnownToolsHaveExplicitCases asserts that every
// currently-registered builtin tool has an explicit predicate case that is NOT
// accidentally caught by the fail-closed default. Turning a toggle ON must
// enable the tool.
func TestBuiltinEnabledPredicate_KnownToolsHaveExplicitCases(t *testing.T) {
	t.Parallel()

	api := settings.NewAPI(nil) // in-memory store, zero value = all defaults
	store := api.Store()
	if store == nil {
		t.Fatal("settings store is nil")
	}

	// Enable all default-off toggles so the predicate returns true for them,
	// proving each has an explicit case (not hitting the default deny branch).
	_ = store.SaveWebSearch(true)
	_ = store.SaveBash(true)
	_ = store.SaveWebFetchEnabled(true)

	pred := builtinEnabledPredicate(api)

	// Tools that should be enabled when their toggle is on.
	enabledWhenOn := []string{
		corewebsearch.ToolName, // websearch
		corebash.Name,          // bash
		corewebfetch.ToolName,  // web_fetch (FR-005)
	}
	for _, name := range enabledWhenOn {
		if !pred(name) {
			t.Errorf("tool %q is denied even though its toggle is on — missing explicit predicate case?", name)
		}
	}
}

// TestBuiltinEnabledPredicate_WebFetchDefaultOff asserts that kenaz__web_fetch
// is disabled by default (FR-005). The user must explicitly enable it.
func TestBuiltinEnabledPredicate_WebFetchDefaultOff(t *testing.T) {
	t.Parallel()

	api := settings.NewAPI(nil) // zero value → WebFetchEnabled = false
	pred := builtinEnabledPredicate(api)

	if pred(corewebfetch.ToolName) {
		t.Error("kenaz__web_fetch should be disabled by default (FR-005); got enabled")
	}
}

// TestSubagentDispatchNotRegisteredWhenSeamNil asserts that
// kenaz__subagent_dispatch is never registered by registerBuiltinTools
// itself — registration for this one tool is split out into
// registerSubagentDispatchTool (see that function's doc for why: it
// needs the LLM connector, which doesn't exist yet at
// registerBuiltinTools time). This also covers the historical FR-007
// concern (a tool that always fails must never be advertised) since
// registerBuiltinTools has no path that registers it at all.
func TestSubagentDispatchNotRegisteredWhenSeamNil(t *testing.T) {
	t.Parallel()

	registry := toolloop.NewBuiltinRegistry()
	registerBuiltinTools(
		nil, // core
		registry,
		nil, // bashStore
		nil, // artifactsMgr
		nil, // store
		nil, // cedarEngine
		nil, // promptRegistry
		nil, // elicitAPI
		nil, // slashDispatch
		nil, // exposureIdx
		nil, // budget
		nil, // posture
		nil, // taskReg
	)

	for _, name := range registry.Names() {
		if name == coresubagent.ToolName {
			t.Errorf("kenaz__subagent_dispatch is registered by registerBuiltinTools — it must only be registered by registerSubagentDispatchTool (FR-007)")
		}
	}
}

// TestRegisterSubagentDispatchTool_NilSeamOmitsTool is the falsification
// pin for FR-007 / AC-07's negative arm: with a nil seam,
// registerSubagentDispatchTool must not register the tool. Mutation:
// remove the `seam == nil` guard. Must fail (the tool would appear even
// though dispatching it can never spawn a real run).
func TestRegisterSubagentDispatchTool_NilSeamOmitsTool(t *testing.T) {
	t.Parallel()
	registry := toolloop.NewBuiltinRegistry()
	registerSubagentDispatchTool(nil, registry, nil)
	for _, name := range registry.Names() {
		if name == coresubagent.ToolName {
			t.Fatal("kenaz__subagent_dispatch is registered with a nil seam")
		}
	}
}

// TestRegisterSubagentDispatchTool_RealSeamRegistersTool is the
// falsification pin for FR-007 / AC-07's positive arm: a real
// (spawner-capable-shaped) seam must produce a registered tool.
// Mutation: revert the call site in core/rpc/api.go (the
// registerSubagentDispatchTool call inside the SetRunSpawner branch) —
// the end-to-end production-wiring assertion in
// TestSubagentDispatchRegisteredInProductionWiring
// (subagent_run_spawner_test.go) is what actually catches that
// regression; this test pins the registration function's own contract
// in isolation.
func TestRegisterSubagentDispatchTool_RealSeamRegistersTool(t *testing.T) {
	t.Parallel()
	registry := toolloop.NewBuiltinRegistry()
	seam := agentgraph.NewFakeBranchSeam()
	registerSubagentDispatchTool(nil, registry, seam)
	var found bool
	for _, name := range registry.Names() {
		if name == coresubagent.ToolName {
			found = true
		}
	}
	if !found {
		t.Fatal("kenaz__subagent_dispatch is not registered with a real seam")
	}
}

// TestBuiltinEnabledPredicate_AllRegisteredToolsHaveExplicitCase is the
// tripwire for FR-006. It boots a REAL HarnessAPI the exact way production
// does (rpc.New(c) — the same call main.go makes) so every builtin tool
// that registerBuiltinTools / registerFSRequestTool / registerFSBuiltinTools
// / registerReadContextFileTool wire in gets a chance to register, then
// pushes every name the registry actually holds through
// builtinEnabledPredicate.
//
// A tool name with no explicit `case` in builtinEnabledPredicate falls
// through to the fail-closed default branch, which logs
// "rpc.builtins.predicate.unknown_tool" — this test fails if that log line
// ever fires for a registered tool, so a newly-registered tool with a
// forgotten predicate case can't silently regress into being denied from
// every catalog. This is exactly how kenaz__ask_user_question broke: it
// was registered unconditionally in registerBuiltinTools but had no case
// in builtinEnabledPredicate, so it was denied (with a WARN) on every
// tool listing / dispatch.
func TestBuiltinEnabledPredicate_AllRegisteredToolsHaveExplicitCase(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	// WithSettingsStore(newTestStore(t)) (upgrade-path-coverage-01PMUG01
	// FR-4a): otherwise rpc.New builds its settings store via
	// settings.NewFileStoreFromEnv() and opens the developer's real
	// settings.json.
	api := New(c, WithSettingsStore(newTestStore(t)))

	registry := api.Builtins()
	if registry == nil {
		t.Fatal("builtins registry is nil — construction broke")
	}
	names := registry.Names()
	if len(names) == 0 {
		t.Fatal("builtin registry is empty — construction broke")
	}

	pred := builtinEnabledPredicate(api.settingsImpl)

	logs := captureLog(t, func() {
		for _, name := range names {
			pred(name)
		}
	})

	var unhandled []string
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Msg  string `json:"msg"`
			Tool string `json:"tool"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		if rec.Msg == "rpc.builtins.predicate.unknown_tool" {
			unhandled = append(unhandled, rec.Tool)
		}
	}
	if len(unhandled) > 0 {
		t.Errorf("builtinEnabledPredicate has no explicit case for registered tool(s) %v — "+
			"every name registered by registerBuiltinTools/registerFSRequestTool/"+
			"registerFSBuiltinTools/registerReadContextFileTool must have a case in "+
			"builtinEnabledPredicate (FR-006); registered tool names were: %v",
			unhandled, names)
	}
}

// TestBashTool_LoggerWired is UNIT-20's AC-16a (trust-surfaces-that-
// fire-01PMZ202 WP22 / FR-017): bash's Cedar gate decision logging must
// actually reach the log file in the PRODUCTION wiring path. Before
// this WP, corebash.Options.Logger was never set at the registration
// site (registerBuiltinTools), so t.logf returned early at every one
// of bash's 18 gate log sites (bash.go:734) in every shipped build —
// including a snippet-write failure that was swallowed with no trace.
// Mutation: revert `Logger: logging.L()`. Must fail (no "bash.gate."
// line is ever captured).
func TestBashTool_LoggerWired(t *testing.T) {
	// corebash.Options.Logger is set to logging.L() ONCE, at
	// registerBuiltinTools time (bash.go copies the pointer verbatim,
	// no live re-fetch) — the same pattern every sibling tool in
	// builtins_wiring.go uses. captureLog's logging.Replace swaps in a
	// NEW *slog.Logger object rather than mutating the existing one in
	// place, so it only affects logging.L() calls made AFTER the swap.
	// Construction must therefore happen INSIDE the captured closure,
	// not before it — otherwise this test silently observes nothing
	// and would falsely pass on the pre-WP22 state (no Logger set) by
	// mis-attributing an empty buffer to "no gate log fired" instead of
	// "the capture never saw this component at all".
	logs := captureLog(t, func() {
		c, err := core.New(core.Options{DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("core.New: %v", err)
		}
		api := New(c, WithSettingsStore(newTestStore(t)))

		registry := api.Builtins()
		if registry == nil {
			t.Fatal("builtins registry is nil — construction broke")
		}
		tool, ok := registry.Lookup(corebash.Name)
		if !ok {
			t.Fatalf("%s is not registered in the production wiring path", corebash.Name)
		}

		// default_bash_policy.cedar is header-only by design
		// (NotApplicable for every command); an un-cancelled context
		// would block on the interactive prompt registry for its
		// 5-minute timeout with no resolver attached. An already-
		// cancelled context makes RequestInteractive return
		// immediately via its ctx.Done() arm
		// (core/policy/cedar/prompt.go:958-971), which bash.go's
		// cedarGate logs as "bash.gate.prompt_err" — still one of the
		// 18 previously-dead log sites this WP wires, so it still
		// proves the fix.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = tool.Call(ctx, json.RawMessage(`{"command":"echo hi"}`))
	})

	var sawGateLog bool
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Msg string `json:"msg"`
		}
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		if strings.HasPrefix(rec.Msg, "bash.gate.") {
			sawGateLog = true
			break
		}
	}
	if !sawGateLog {
		t.Errorf("no bash.gate.* log line was captured for a real dispatch — "+
			"corebash.Options.Logger is not wired in the production path; captured logs: %s", logs)
	}
}

// dangerousOpsStore overrides exactly the one accessor under test.
// Embedding the interface keeps this from drifting as SettingsStore grows.
type dangerousOpsStore struct {
	settings.SettingsStore
	enabled bool
	err     error
}

func (s *dangerousOpsStore) LoadPermissionCacheDangerousOps() (bool, error) {
	return s.enabled, s.err
}

// TestDangerousOpsCacheLookup is the unwired-sweep pin (2026-08-14) for
// Settings.PermissionCacheDangerousOps. The dial was persisted, bound,
// and rendered — BashPermissionModal.vue offers "Allow always" from it —
// but registerBuiltinTools never passed it to corebash.New, so the tool
// saw the zero value forever and demoted every dangerous AllowAlways
// grant back to AllowOnce. The user granted a standing permission and
// got re-prompted on the next identical command with no explanation.
//
// The lookup must also read LIVE: a construction-time snapshot would
// leave the toggle inert until the next app restart while the modal
// keeps rendering from the current value.
func TestDangerousOpsCacheLookup(t *testing.T) {
	t.Run("nil store fails closed", func(t *testing.T) {
		if dangerousOpsCacheLookup(nil)() {
			t.Error("nil store resolved the dangerous-ops cache override to ON")
		}
	})

	t.Run("read failure fails closed", func(t *testing.T) {
		store := &dangerousOpsStore{enabled: true, err: errors.New("simulated read failure")}
		if dangerousOpsCacheLookup(store)() {
			t.Error("a settings read failure resolved the dangerous-ops cache override to ON")
		}
	})

	t.Run("reads the live value, not a construction-time snapshot", func(t *testing.T) {
		store := &dangerousOpsStore{enabled: false}
		lookup := dangerousOpsCacheLookup(store)
		if lookup() {
			t.Fatal("lookup returned true while the dial was off")
		}
		store.enabled = true
		if !lookup() {
			t.Error("lookup did not observe the dial being turned on — it snapshotted at construction, so the Settings toggle stays inert until restart")
		}
	})
}

// TestAskUserQuestionDelegateIsWired pins that kenaz__ask_user_question is
// registered in the PRODUCTION wiring path with a real, non-nil Delegate.
//
// This is the anti-regression pin for the stale comment that sat above the
// registration in builtins_wiring.go until 2026-08-14: it asserted "the
// Delegate is nil until WP04 wires the elicit RPC bridge", long after WP04
// had landed. Read literally, it said the tool was an inert stub that
// returned errKindNotWired. In fact the delegate was live, so calling the
// tool PARKED the turn for ten minutes waiting on a dialog the frontend
// never mounted.
//
// The discriminator is the tool's own two failure shapes. With a nil
// delegate the tool short-circuits to {"error":"not_wired"} without ever
// touching the elicit API. With a live delegate it calls OpenDialog, which
// honours the caller's context — so an already-cancelled context comes back
// as a well-formed AskResult with cancelled=true. Only a wired delegate can
// produce the second shape.
func TestAskUserQuestionDelegateIsWired(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	// WithSettingsStore(newTestStore(t)) (upgrade-path-coverage-01PMUG01
	// FR-4a): otherwise rpc.New builds its settings store via
	// settings.NewFileStoreFromEnv() and opens the developer's real
	// settings.json.
	api := New(c, WithSettingsStore(newTestStore(t)))

	registry := api.Builtins()
	if registry == nil {
		t.Fatal("builtins registry is nil — construction broke")
	}
	tool, ok := registry.Lookup(coreaskuser.ToolName)
	if !ok {
		t.Fatalf("%s is not registered in the production wiring path", coreaskuser.ToolName)
	}

	// An already-cancelled context makes the parked OpenDialog return
	// immediately, so this test never waits on the 10-minute dialog
	// deadline.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	raw, callErr := tool.Call(ctx, json.RawMessage(`{"question":"Ship it?","kind":"radio","options":[{"value":"y","label":"Yes"},{"value":"n","label":"No"}]}`))
	if callErr != nil {
		t.Fatalf("tool.Call returned a hard error: %v", callErr)
	}

	var errResult struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if jsonErr := json.Unmarshal(raw, &errResult); jsonErr == nil && errResult.Error != "" {
		t.Fatalf("%s reported %q (%s) — its Delegate is nil in the production "+
			"wiring path, so the model is offered a tool that cannot ask anything",
			coreaskuser.ToolName, errResult.Error, errResult.Message)
	}

	var result struct {
		Answer    json.RawMessage `json:"answer"`
		Cancelled bool            `json:"cancelled"`
	}
	if jsonErr := json.Unmarshal(raw, &result); jsonErr != nil {
		t.Fatalf("decode AskResult from %s: %v (raw: %s)", coreaskuser.ToolName, jsonErr, raw)
	}
	if !result.Cancelled {
		t.Errorf("expected cancelled=true from a cancelled context; got %s", raw)
	}
}
