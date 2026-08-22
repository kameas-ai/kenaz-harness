package serve_test

import (
	"strings"
	"testing"
)

// wp04_chat_affordances_test.go — served-mode-is-a-real-mode-01PMZ707 WP04.
//
// AC-710: for every affordance WP04 marks "port", a served client call must
// reach the dispatch case and return a REAL result — driven through the
// actual HTTP transport against a real core (newChatHarness), never a fake.
// The falsification each test names is: remove the dispatch `case` (or
// revert methods.go's entry) and the call regresses to the generic
// unknownMethodError ("... is not ported to served mode ..."), which none
// of the assertions below would tolerate.

// TestServedChat_ResolveAutonomy_Ported pins Sessions_ResolveAutonomy: a
// read on session state the served build already owns (WP04 "port the
// read"). *Falsify*: remove the "Sessions_ResolveAutonomy" case from
// server.go's dispatch switch → this call returns the generic "is not
// ported to served mode" error instead of a real ResolvedAutonomy payload.
func TestServedChat_ResolveAutonomy_Ported(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()
	_ = api

	var created struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": "wp04"}, &created)
	if created.ID == "" {
		t.Fatal("Sessions_Create returned no session id")
	}

	var resolved struct {
		Resolved struct {
			Tier string `json:"tier"`
		} `json:"resolved"`
	}
	if errStr := rpcCallErr(t, baseURL, "Sessions_ResolveAutonomy", map[string]any{"id": created.ID}, &resolved); errStr != "" {
		t.Fatalf("Sessions_ResolveAutonomy returned an error, want a real ResolvedAutonomy payload: %s", errStr)
	}
	if resolved.Resolved.Tier == "" {
		t.Fatal("Sessions_ResolveAutonomy: resolved.tier is empty — the dispatch case did not reach the real ResolveAutonomy business logic")
	}
}

// TestServedChat_SuggestTitle_Ported pins Sessions_SuggestTitle: it runs a
// model call the served build can already make (LLM_StartStream is
// served), so the whole flow completes inside the VM (WP04 "port").
//
// newChatHarness's core wires a real autotitle.Generator (RAN: it is not
// nil — the call reaches "autotitle: title too short" on an empty
// transcript, not sessions.ErrTitleGeneratorNotConfigured, correcting this
// test's own first draft) but has no LLM provider configured, so a
// populated transcript fails one step further in, at the LLM call. Either
// failure is the METHOD's OWN business logic, reachable only once dispatch
// has routed the call into the real SuggestTitle implementation.
// *Falsify*: remove the dispatch case → the error text changes to the
// generic "is not ported to served mode" refusal instead of either of
// these.
func TestServedChat_SuggestTitle_Ported(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()
	_ = api

	var created struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": "wp04"}, &created)
	if created.ID == "" {
		t.Fatal("Sessions_Create returned no session id")
	}
	rpcCall(t, baseURL, "Sessions_AppendMessage", map[string]any{
		"id":      created.ID,
		"role":    "user",
		"content": "Please help me refactor the storage layer to use a connection pool instead of a single shared handle.",
	}, nil)

	errStr := rpcCallErr(t, baseURL, "Sessions_SuggestTitle", map[string]any{"id": created.ID}, nil)
	if errStr == "" {
		t.Fatal("Sessions_SuggestTitle: expected an error (no LLM provider configured in the test harness), got none")
	}
	if strings.Contains(errStr, "not ported to served mode") || strings.Contains(errStr, "unknown method") {
		t.Fatalf("Sessions_SuggestTitle: got the generic not-ported refusal, want the method's own autotitle failure — the dispatch case is missing: %s", errStr)
	}
	if !strings.Contains(errStr, "autotitle") && !strings.Contains(errStr, "title generator") {
		t.Fatalf("Sessions_SuggestTitle: unexpected error shape, want an autotitle-package failure: %s", errStr)
	}
}

// TestServedChat_ConfigGetFlags_Ported pins Config_GetFlags: a
// side-effect-free read (env-derived flags only) whose absence made every
// flag read in a served workbench silently fall back to a hardcoded
// default (WP04 "port"). *Falsify*: remove the dispatch case → the call
// returns the generic not-ported error instead of the real flag list.
func TestServedChat_ConfigGetFlags_Ported(t *testing.T) {
	_, baseURL, cancel := newChatHarness(t)
	defer cancel()

	var flags []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		EnvVar  string `json:"envVar"`
	}
	if errStr := rpcCallErr(t, baseURL, "Config_GetFlags", map[string]any{}, &flags); errStr != "" {
		t.Fatalf("Config_GetFlags returned an error, want the real flag list: %s", errStr)
	}
	if len(flags) == 0 {
		t.Fatal("Config_GetFlags: empty result — the dispatch case did not reach rpc.ComputeFeatureFlags()")
	}
	for _, f := range flags {
		if f.Name == "" || f.EnvVar == "" {
			t.Fatalf("Config_GetFlags: malformed entry %+v", f)
		}
	}
}
