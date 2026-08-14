package rpc

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
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
// kenaz__subagent_dispatch is not registered when the BranchSeam is nil,
// so the model's tool catalog omits a permanently-broken tool (FR-007).
func TestSubagentDispatchNotRegisteredWhenSeamNil(t *testing.T) {
	t.Parallel()

	registry := toolloop.NewBuiltinRegistry()
	// Call registerBuiltinTools with nil core and nil settings — the seam
	// will be nil, so the subagent dispatch tool must not appear.
	registerBuiltinTools(
		nil,      // core
		registry,
		nil,      // bashStore
		nil,      // artifactsMgr
		nil,      // store
		nil,      // cedarEngine
		nil,      // promptRegistry
		nil,      // elicitAPI
		nil,      // slashDispatch
		nil,      // exposureIdx
		nil,      // budget
		nil,      // posture
	)

	for _, name := range registry.Names() {
		if name == coresubagent.ToolName {
			t.Errorf("kenaz__subagent_dispatch is registered even though BranchSeam is nil (FR-007)")
		}
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
	api := New(c)

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
