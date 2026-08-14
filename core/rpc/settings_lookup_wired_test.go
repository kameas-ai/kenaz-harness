package rpc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// wiringFile is the single site where builtin tools are constructed.
const wiringFile = "builtins_wiring.go"

// TestSettingsLookupHelpersAreActuallyPassed closes the vacuity hole the
// 2026-08-14 unwired sweep left behind in its own fix.
//
// The sweep found Settings.PermissionCacheDangerousOps stored, bound and
// rendered but never passed to corebash.New, and fixed it by adding
// `PermissionCacheDangerousOps: dangerousOpsCacheLookup(store)` to the
// options literal. It also added TestDangerousOpsCacheLookup — but that
// test exercises the HELPER in isolation. Deleting the options-literal
// line leaves the helper, its test, and the whole `go test ./core/...`
// suite green while the dial goes inert again (verified by mutation:
// removing the line failed nothing).
//
// That is the same shape as the vacuous pass the sweep closed in
// check-knob-coverage.sh: a check whose success is indistinguishable
// from "the thing under test is not wired at all".
//
// So: every unexported `<x>Lookup(store)`-shaped settings helper defined
// in builtins_wiring.go must appear as a VALUE in a composite literal —
// i.e. actually be handed to a tool's Options. A helper that is only
// called from its own unit test is a lookup nothing looks up.
func TestSettingsLookupHelpersAreActuallyPassed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, wiringFile, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", wiringFile, err)
	}

	// 1. Collect the helper declarations: unexported, name ends in
	//    "Lookup", returns a single `func() bool`.
	declared := map[string]token.Pos{}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		if ast.IsExported(name) || !strings.HasSuffix(name, "Lookup") {
			continue
		}
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			continue
		}
		ft, ok := fn.Type.Results.List[0].Type.(*ast.FuncType)
		if !ok || ft.Params != nil && len(ft.Params.List) != 0 {
			continue
		}
		declared[name] = fn.Pos()
	}

	// A scan that finds nothing is not a pass. If the naming convention
	// changes, this test must be updated in the same commit rather than
	// quietly asserting on an empty set.
	if len(declared) == 0 {
		t.Fatalf("found no unexported *Lookup() func() bool helpers in %s — "+
			"the naming convention this test depends on has changed; update the "+
			"matcher in the same commit rather than letting the check go vacuous", wiringFile)
	}

	// 2. Collect helper names called from inside a composite literal —
	//    i.e. passed as an Options field value.
	passed := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			ast.Inspect(elt, func(m ast.Node) bool {
				call, ok := m.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					if _, tracked := declared[id.Name]; tracked {
						passed[id.Name] = true
					}
				}
				return true
			})
		}
		return true
	})

	for name := range declared {
		if !passed[name] {
			t.Errorf("%s defines %s but never passes its result into a tool Options literal in the same file.\n"+
				"A settings lookup that is constructed and not handed to a consumer is an inert dial: "+
				"the value is persisted, bound and rendered in the UI, and no code branches on it.\n"+
				"Wire it at the construction site, or delete the helper and the Settings field with it.",
				wiringFile, name)
		}
	}
}
