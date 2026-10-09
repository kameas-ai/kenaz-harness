// Command checktoolexposure is the structural half of
// scripts/ci/check-tool-exposure-gate.sh (tool-context-budget-01TCBUD01
// WP08, spec FR-E1 / acceptance criterion 6).
//
// THE DEFECT CLASS
// ----------------
// Before the mission every model call carried every installed tool's
// full schema — 143 tools, ~220k prompt tokens on a one-word turn —
// because the request builder copied the whole discovered catalog into
// `llm.GenerationRequest.Tools`. The mission put a partition in between
// (toolexposure.ResolvedCatalog → chat.exposureTurn.selectTools → hot /
// pinned / activated segments → `GenerationRequest.SetTools`). The
// regression this gate exists for is the next request builder that
// skips the partition: a new `GenerationRequest{Tools: catalog}` or a
// `req.Tools = discovered` that sends summary-tier schemas without
// activation. Every test of the partition keeps passing in that state,
// because they all drive the partition.
//
// WHAT THIS CHECKS
// ----------------
// Every non-test .go file under core/ is parsed (go/parser, no type
// checking — fast, and needs no build). A WRITER of
// GenerationRequest.Tools is any of:
//
//	literal   a composite literal of type GenerationRequest (bare or
//	          package-qualified, with or without &) with a `Tools:` key,
//	          or an unkeyed (positional) GenerationRequest literal;
//	settools  a call to `<x>.SetTools(...)` — the canonical setter,
//	          core/llm/prompt_cache.go, the only method of that name;
//	assign    `<x>.Tools = …` / `<x>.Tools, … = …` where <x> is an
//	          identifier the same function visibly declares as a
//	          GenerationRequest: a parameter or receiver of type
//	          (*)GenerationRequest, `var x GenerationRequest`, or
//	          `x := (&)GenerationRequest{…}`.
//
// Each writer is keyed `<repo-relative file>|<function>|<kind>` —
// line-number free, so unrelated edits do not churn the allowlist.
// The body of (*GenerationRequest).SetTools itself is the definition of
// the setter, not a writer, and is skipped.
//
// Every writer must be listed in
// scripts/ci/allowlists/tool-exposure-writers.txt with a justification
// naming why it may write tools: either it IS the exposure-partition
// path, or it is a dated, owned gap. An unlisted writer fails (exit 2).
// A listed key that is no longer a writer is STALE and fails (exit 2):
// the allowlist shrinks monotonically.
//
// Discovery floors (a gate that inspected nothing must not pass): at
// least one GenerationRequest composite literal and at least one
// SetTools call must be found, or exit 1.
//
// LIMITS (honest): no type checking, so a writer through an alias type,
// a field of another struct holding a GenerationRequest
// (`s.req.Tools = …`), reflection, or a value returned from a helper is
// invisible here. The runtime half of the gate — the shell script runs
// TestRequestBuilder_NeverSendsSummaryToolUnlessActivated and the
// production-wiring first-turn test — covers the one path that writes
// today. The parse is deliberately syntactic so the gate runs in a
// second on the self-hosted runner.
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	scanRoot      = "core"
	allowlistPath = "scripts/ci/allowlists/tool-exposure-writers.txt"
	typeName      = "GenerationRequest"
	setterName    = "SetTools"
	toolsField    = "Tools"
)

func repoRoot() string {
	if out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, _ := os.Getwd()
	return wd
}

// isGenReqType reports whether expr names GenerationRequest or
// *GenerationRequest, bare or package-qualified.
func isGenReqType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return isGenReqType(e.X)
	case *ast.Ident:
		return e.Name == typeName
	case *ast.SelectorExpr:
		return e.Sel.Name == typeName
	case *ast.ParenExpr:
		return isGenReqType(e.X)
	}
	return false
}

// genReqLit returns the GenerationRequest composite literal expr is
// (possibly behind &), or nil.
func genReqLit(expr ast.Expr) *ast.CompositeLit {
	if u, ok := expr.(*ast.UnaryExpr); ok && u.Op == token.AND {
		expr = u.X
	}
	if cl, ok := expr.(*ast.CompositeLit); ok && cl.Type != nil && isGenReqType(cl.Type) {
		return cl
	}
	return nil
}

func funcLabel(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return "(" + exprString(fd.Recv.List[0].Type) + ")." + fd.Name.Name
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.IndexExpr:
		return exprString(x.X)
	case *ast.IndexListExpr:
		return exprString(x.X)
	}
	return "?"
}

type scan struct {
	writers  map[string]bool
	literals int
	setCalls int
}

// scanFunc scans one function body (or file-scope declaration) for
// writers, labelled label.
func (s *scan) scanNode(rel, label string, recv *ast.FieldList, typ *ast.FuncType, body ast.Node) {
	if body == nil {
		return
	}
	// The setter's own definition is not a writer.
	if label == "(*"+typeName+")."+setterName && strings.HasPrefix(rel, "core/llm/") {
		return
	}
	declared := map[string]bool{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			if !isGenReqType(f.Type) {
				continue
			}
			for _, n := range f.Names {
				declared[n.Name] = true
			}
		}
	}
	addFields(recv)
	if typ != nil {
		addFields(typ.Params)
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncLit:
			addFields(x.Type.Params)
		case *ast.ValueSpec:
			if x.Type != nil && isGenReqType(x.Type) {
				for _, nm := range x.Names {
					declared[nm.Name] = true
				}
			}
			for i, v := range x.Values {
				if genReqLit(v) != nil && i < len(x.Names) {
					declared[x.Names[i].Name] = true
				}
			}
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for i, rhs := range x.Rhs {
					if genReqLit(rhs) == nil || i >= len(x.Lhs) {
						continue
					}
					if id, ok := x.Lhs[i].(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if x.Type == nil || !isGenReqType(x.Type) {
				return true
			}
			s.literals++
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					s.writers[rel+"|"+label+"|literal"] = true
					break
				}
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == toolsField {
					s.writers[rel+"|"+label+"|literal"] = true
				}
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == setterName {
				s.setCalls++
				s.writers[rel+"|"+label+"|settools"] = true
			}
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != toolsField {
					continue
				}
				if id, ok := sel.X.(*ast.Ident); ok && declared[id.Name] {
					s.writers[rel+"|"+label+"|assign"] = true
				}
			}
		}
		return true
	})
}

func loadAllowlist(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		out[line] = true
	}
	return out, sc.Err()
}

func main() {
	root := repoRoot()
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] FAIL: chdir %s: %v\n", root, err)
		os.Exit(1)
	}
	if fi, err := os.Stat(scanRoot); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] FAIL: scan root %q missing under %s\n", scanRoot, root)
		os.Exit(1)
	}
	s := &scan{writers: map[string]bool{}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		rel := filepath.ToSlash(path)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				s.scanNode(rel, funcLabel(d), d.Recv, d.Type, d.Body)
			case *ast.GenDecl:
				s.scanNode(rel, "<file-scope>", nil, nil, d)
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] FAIL: %v\n", err)
		os.Exit(1)
	}
	if s.literals == 0 || s.setCalls == 0 {
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] FAIL: discovery floor — found %d %s literals and %d %s calls under %s.\n",
			s.literals, typeName, s.setCalls, setterName, scanRoot)
		fmt.Fprintln(os.Stderr, "[tool-exposure-gate] A gate that found nothing to inspect cannot pass; the type or setter was renamed — update this checker in the same commit.")
		os.Exit(1)
	}
	allow, err := loadAllowlist(allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] FAIL: reading %s: %v\n", allowlistPath, err)
		os.Exit(1)
	}

	var writers, unlisted, stale []string
	for w := range s.writers {
		writers = append(writers, w)
		if !allow[w] {
			unlisted = append(unlisted, w)
		}
	}
	for a := range allow {
		if !s.writers[a] {
			stale = append(stale, a)
		}
	}
	sort.Strings(writers)
	sort.Strings(unlisted)
	sort.Strings(stale)

	fmt.Printf("[tool-exposure-gate] scanned %s: %d %s literals, %d %s calls, %d writer(s) of %s.%s:\n",
		scanRoot, s.literals, typeName, s.setCalls, setterName, len(writers), typeName, toolsField)
	for _, w := range writers {
		fmt.Printf("    %s\n", w)
	}
	fail := false
	if len(unlisted) > 0 {
		fail = true
		fmt.Fprintf(os.Stderr, "\n[tool-exposure-gate] FAIL: unlisted writer(s) of %s.%s — a tool schema can reach the model without the exposure partition:\n", typeName, toolsField)
		for _, w := range unlisted {
			fmt.Fprintf(os.Stderr, "    %s\n", w)
		}
		fmt.Fprintln(os.Stderr, "[tool-exposure-gate] Route the tools through chat.exposureTurn.selectTools (toolexposure.ResolvedCatalog.Sendable) and GenerationRequest.SetTools,")
		fmt.Fprintf(os.Stderr, "[tool-exposure-gate] or add the key to %s with a DATED justification naming the blocker and owner.\n", allowlistPath)
	}
	if len(stale) > 0 {
		fail = true
		fmt.Fprintf(os.Stderr, "\n[tool-exposure-gate] FAIL: STALE entries in %s (no longer a writer — delete the line; allowlists shrink monotonically):\n", allowlistPath)
		for _, w := range stale {
			fmt.Fprintf(os.Stderr, "    %s\n", w)
		}
	}
	if fail {
		os.Exit(2)
	}
	fmt.Printf("[tool-exposure-gate] structural check clean — every writer is listed in %s.\n", allowlistPath)
}
