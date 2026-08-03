package serve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestServedMethodsMatchDispatchSwitch is the drift guard.
//
// servedMethods / servedStreamMethods in methods.go mirror the switches in
// server.go. A mirror that can rot silently is worse than no mirror: a method
// added to dispatch but missing from servedMethods would be reported by
// Serve_ListMethods as absent, and a caller who spelled it correctly would
// still be told it might be unported — which is the exact failure mode this
// package was changed to eliminate.
//
// So the test does not hardcode a list. It parses server.go, pulls the case
// labels straight out of the two switch statements, and demands equality.
func TestServedMethodsMatchDispatchSwitch(t *testing.T) {
	rpcCases, streamCases := parseServerSwitches(t)

	assertSetsEqual(t, "POST /rpc dispatch", rpcCases, servedMethods,
		"add or remove the name in servedMethods (core/serve/methods.go)")
	assertSetsEqual(t, "GET /ws stream", streamCases, servedStreamMethods,
		"add or remove the name in servedStreamMethods (core/serve/methods.go)")
}

// parseServerSwitches returns the case-label strings of the dispatch switch
// (POST /rpc) and the WebSocket stream switch (GET /ws) in server.go.
func parseServerSwitches(t *testing.T) (rpcCases, streamCases []string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Tag == nil {
			return true
		}
		labels := switchCaseStrings(sw)
		if len(labels) == 0 {
			return true
		}
		switch tag := sw.Tag.(type) {
		case *ast.Ident:
			// switch method { ... } inside Server.dispatch.
			if tag.Name == "method" {
				rpcCases = append(rpcCases, labels...)
			}
		case *ast.SelectorExpr:
			// switch req.Method { ... } inside the /ws handler.
			if tag.Sel != nil && tag.Sel.Name == "Method" {
				streamCases = append(streamCases, labels...)
			}
		}
		return true
	})

	if len(rpcCases) == 0 {
		t.Fatal("found no case labels in the dispatch switch — has server.go been restructured? " +
			"This test must be updated alongside it, not deleted.")
	}
	if len(streamCases) == 0 {
		t.Fatal("found no case labels in the /ws stream switch — has server.go been restructured?")
	}
	return rpcCases, streamCases
}

// switchCaseStrings collects the string-literal case labels of a switch.
func switchCaseStrings(sw *ast.SwitchStmt) []string {
	var out []string
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue // default:
		}
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

func assertSetsEqual(t *testing.T, what string, fromSource, fromList []string, fixHint string) {
	t.Helper()

	src := map[string]bool{}
	for _, s := range fromSource {
		src[s] = true
	}
	list := map[string]bool{}
	for _, s := range fromList {
		list[s] = true
	}

	var missing, extra []string
	for s := range src {
		if !list[s] {
			missing = append(missing, s)
		}
	}
	for s := range list {
		if !src[s] {
			extra = append(extra, s)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("%s switch handles %v, but they are absent from the mirror list.\n"+
			"Serve_ListMethods would under-report the surface and a correctly spelled call "+
			"would still be told it may be unported.\nFix: %s",
			what, missing, fixHint)
	}
	if len(extra) > 0 {
		t.Errorf("%s mirror list claims %v, but the switch does not handle them.\n"+
			"Serve_ListMethods would advertise methods that error out.\nFix: %s",
			what, extra, fixHint)
	}
}

// TestSuggestMethod covers the exact spellings that caused the misdiagnosis
// this package was changed to prevent, plus the boundary where suggesting
// would itself be a lie.
func TestSuggestMethod(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantCanonical string
		wantTransport string
		wantOK        bool
	}{
		// The literal probe that produced a false "stale binary" diagnosis.
		{"dot-camel providers", "llm.listProviders", "LLM_ListProviders", "rpc", true},
		{"dot-camel appinfo", "app.info", "AppInfo", "rpc", true},
		{"lowercase", "llm_listproviders", "LLM_ListProviders", "rpc", true},
		{"kebab", "llm-list-providers", "LLM_ListProviders", "rpc", true},
		{"exact", "LLM_ListProviders", "LLM_ListProviders", "rpc", true},
		{"stream method posted to rpc", "sessions.stream", "Sessions_Stream", "stream", true},
		{"chat start", "llm.startStream", "LLM_StartStream", "rpc", true},

		// No suggestion: these are not the served method in a different style,
		// they are different (or absent) methods. Guessing here would recreate
		// the confident-and-wrong failure mode.
		{"genuinely absent", "Nonexistent", "", "", false},
		{"desktop-only write method", "LLM_AddProvider", "", "", false},
		{"empty", "", "", "", false},
		{"punctuation only", "...", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canonical, transport, ok := suggestMethod(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("suggestMethod(%q) ok = %v, want %v (got %q/%q)",
					tc.input, ok, tc.wantOK, canonical, transport)
			}
			if canonical != tc.wantCanonical {
				t.Errorf("suggestMethod(%q) canonical = %q, want %q",
					tc.input, canonical, tc.wantCanonical)
			}
			if transport != tc.wantTransport {
				t.Errorf("suggestMethod(%q) transport = %q, want %q",
					tc.input, transport, tc.wantTransport)
			}
		})
	}
}

// TestUnknownMethodError_DoesNotBlameTheBuild is the behavioural regression
// test for the misdiagnosis.
//
// A misspelled but present method must NOT be described as unported, and must
// NOT tell the reader to use the desktop app. That advice is unactionable
// inside a VM and — worse — it implies the running binary is older than the
// commit that added the method, which is what sent the investigation chasing
// stale images.
func TestUnknownMethodError_DoesNotBlameTheBuild(t *testing.T) {
	err := unknownMethodError("llm.listProviders")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()

	if !strings.Contains(msg, "LLM_ListProviders") {
		t.Errorf("error must name the correct spelling, got %q", msg)
	}
	for _, forbidden := range []string{"not ported", "desktop app", "file a ticket"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("error for a misspelled-but-present method must not mention %q "+
				"(it implies the binary is stale); got %q", forbidden, msg)
		}
	}
}

// TestUnknownMethodError_AbsentMethodStillSuggestsATicket keeps the honest
// half of the old behaviour: a name that really is not in the surface may
// well be an unported desktop binding, and "file a ticket" is right there.
func TestUnknownMethodError_AbsentMethodStillSuggestsATicket(t *testing.T) {
	msg := unknownMethodError("LLM_AddProvider").Error()

	if !strings.Contains(msg, "not ported to served mode") {
		t.Errorf("absent method should still be described as possibly unported, got %q", msg)
	}
	if !strings.Contains(msg, "Serve_ListMethods") {
		t.Errorf("error should point at the discovery method, got %q", msg)
	}
}

// TestUnknownMethodError_StreamMethodPointsAtWS ensures a caller who knows
// the name but posted it to the wrong endpoint is told which endpoint to use.
func TestUnknownMethodError_StreamMethodPointsAtWS(t *testing.T) {
	msg := unknownMethodError("Sessions_Stream").Error()

	if !strings.Contains(msg, "/ws") {
		t.Errorf("stream method posted to /rpc should point at GET /ws, got %q", msg)
	}
	if strings.Contains(msg, "not ported") {
		t.Errorf("a wired stream method must not be called unported, got %q", msg)
	}
}

// TestMethodListIsSortedAndCopied guards the wire contract: stable order, and
// no aliasing that would let a caller mutate the package-level surface.
func TestMethodListIsSortedAndCopied(t *testing.T) {
	ml := methodList()

	if !sort.StringsAreSorted(ml.RPC) {
		t.Errorf("Serve_ListMethods rpc list is not sorted: %v", ml.RPC)
	}
	if !sort.StringsAreSorted(ml.Stream) {
		t.Errorf("Serve_ListMethods stream list is not sorted: %v", ml.Stream)
	}
	if len(ml.RPC) > 0 {
		ml.RPC[0] = "MUTATED"
		if servedMethods[0] == "MUTATED" {
			t.Error("methodList aliases servedMethods; callers can corrupt the surface")
		}
	}
}
