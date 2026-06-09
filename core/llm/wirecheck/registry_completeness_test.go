package wirecheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck"
)

// inScopeAdapters is the set of adapter kinds the completeness check
// enforces. Every (struct, exported field, adapter) triple must have
// either a tests: entry or an unsupported: reason in the registry.
var inScopeAdapters = []string{"anthropic", "openai", "openrouter", "bedrock"}

// inScopeStructs are the three connector types the registry covers.
var inScopeStructs = map[string]reflect.Type{
	"GenerationRequest": reflect.TypeOf(llm.GenerationRequest{}),
	"Response":          reflect.TypeOf(llm.Response{}),
	"StreamEvent":       reflect.TypeOf(llm.StreamEvent{}),
}

// TestRegistryCompleteness walks every exported field of the three
// in-scope structs and asserts that for every (struct, field, adapter)
// triple the registry contains at least one matching entry — either a
// real test function reference or an explicit unsupported: reason.
//
// Failure messages name the exact missing triple so the contributor
// knows precisely what to add to coverage_registry.yaml.
func TestRegistryCompleteness(t *testing.T) {
	reg := loadRegistry(t)
	fns := collectTestFunctions(t)

	// Walk struct fields × adapters.
	for structName, typ := range inScopeStructs {
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			// Fields tagged wirecheck:"-" are intentionally excluded.
			if f.Tag.Get("wirecheck") == "-" {
				continue
			}
			// Fields tagged wirecheck:"derived" are populated server-side
			// and don't go on the wire; skip them.
			if f.Tag.Get("wirecheck") == "derived" {
				continue
			}
			fieldName := f.Name

			for _, adapter := range inScopeAdapters {
				if !reg.HasCoverage(structName, fieldName, adapter) {
					t.Errorf(
						"coverage_registry.yaml: missing entry for (%s, %s, %s) — "+
							"add either a tests: list or an unsupported: reason",
						structName, fieldName, adapter,
					)
				}
			}
		}
	}

	// Walk every named test function in the registry and verify it exists.
	for _, fn := range reg.TestNames() {
		if _, ok := fns[fn]; !ok {
			t.Errorf(
				"coverage_registry.yaml: test function %q is listed but not found in adapter test files — "+
					"rename the entry to match the actual function name",
				fn,
			)
		}
	}
}

// loadRegistry loads coverage_registry.yaml relative to the wirecheck
// package directory.
func loadRegistry(t *testing.T) *wirecheck.Registry {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("wirecheck: cannot determine caller location")
	}
	dir := filepath.Dir(thisFile)
	// registry lives two levels up: core/llm/coverage_registry.yaml
	path := filepath.Join(dir, "..", "coverage_registry.yaml")
	reg, err := wirecheck.LoadRegistryFromPath(path)
	if err != nil {
		t.Fatalf("wirecheck: load registry: %v", err)
	}
	return reg
}

// collectTestFunctions walks the adapter test directories and collects
// every func Test* name using go/parser so we don't need runtime
// reflection (test functions aren't reachable that way).
//
// Returns a set of unqualified function names.
func collectTestFunctions(t *testing.T) map[string]struct{} {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("wirecheck: cannot determine caller location")
	}
	// wirecheck/ lives under core/llm/wirecheck; adapters are siblings.
	llmDir := filepath.Join(filepath.Dir(thisFile), "..")

	adapterDirs := []string{
		filepath.Join(llmDir, "anthropic"),
		filepath.Join(llmDir, "openai"),
		filepath.Join(llmDir, "openrouter"),
		filepath.Join(llmDir, "bedrock"),
	}

	fns := make(map[string]struct{})
	fset := token.NewFileSet()

	for _, dir := range adapterDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// If the directory doesn't exist that's fine; adapters may not
			// all be present in every worktree state.
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			full := filepath.Join(dir, entry.Name())
			f, err := parser.ParseFile(fset, full, nil, 0)
			if err != nil {
				t.Logf("wirecheck: parse %s: %v (skipping)", full, err)
				continue
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					continue
				}
				if strings.HasPrefix(fn.Name.Name, "Test") {
					fns[fn.Name.Name] = struct{}{}
				}
			}
		}
	}
	return fns
}
