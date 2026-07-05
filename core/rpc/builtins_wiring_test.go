package rpc

import (
	"testing"

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
