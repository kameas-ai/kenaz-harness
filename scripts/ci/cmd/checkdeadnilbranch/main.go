// Command checkdeadnilbranch is the Go half of
// scripts/ci/check-dead-nil-branch.sh — the statically-dead-nil-branch
// gate.
//
// THE DEFECT CLASS
// -----------------
// `var x T` (a zero-value declaration, no initializer) followed, in the
// SAME lexical block, by `if x != nil { ... }` with NO assignment to x
// anywhere between the two — so x is provably nil at the check and the
// if-block is unreachable dead code. The class is not hypothetical: it
// shipped in production. core/rpc/builtins_wiring.go carried
//
//	var subagentSeam agentgraph.BranchSeam // nil — no child-run spawner yet
//	if subagentSeam != nil {
//	    registerSubagentDispatchTool(...)
//	}
//
// which hid the ENTIRE kenaz__subagent_dispatch registration block from
// every existing gate: check-builtin-tool-registration.sh looks for an
// *import* of core/tools/subagentdispatch, which was present (the
// package was imported to construct the tool inside the dead branch),
// so the tool never landed on any "registered but unreachable"
// allowlist despite being unreachable in every build until UNIT-6 of
// subagent-control-and-background-tasks-01PMZB11 replaced the dead
// variable with a real spawner-armed seam. No existing gate in this
// directory can see this class:
//   - check-nil-optional-deps.sh (I18) looks for a STRUCT FIELD
//     documented optional by doc-comment phrase, never a local
//     variable, and never requires a doc comment at all — the
//     defect above had a comment ("nil — no child-run spawner yet")
//     that i18's trigger-phrase regex does not match.
//   - check-no-unwired-gates.sh / check-nil-optional-deps.sh's own
//     "assignment anywhere in core/+cmd/" search is a whole-program
//     question about a STRUCT field; this is a single-function,
//     single-block dataflow question about a LOCAL variable that
//     never escapes its declaring block.
//
// THE CHECK, PRECISELY
// ---------------------
// For every *ast.BlockStmt found anywhere under core/+cmd/ (function
// bodies and every nested block — the class can occur at either level):
//
//  1. Find every `var x T` statement in the block's OWN statement list
//     (not nested) with no initializer, where T's underlying type
//     permits a nil value (interface, pointer, map, slice, chan, or
//     func).
//  2. Find every `if x != nil { ... }` (or `if nil != x`) statement
//     later in the SAME block's statement list, where the `x` on both
//     sides of the comparison resolves (via go/types object identity,
//     not name matching) to the SAME declared object from step 1.
//  3. For each (decl, check) pair, walk every statement strictly
//     between them — recursively, into every nested block, closure,
//     and control-flow arm — looking for ANY assignment (`=` or `:=`)
//     whose LHS resolves to the same object. If none exists, the
//     branch is statically dead: report it.
//
// WHY THIS DOES NOT FIRE ON LEGITIMATE NIL-TOLERANT OPTIONAL DEPS
// -------------------------------------------------------------------
// `if opts.Tasks != nil` and `if posture != nil` (a function parameter)
// are both true negatives BY CONSTRUCTION, not by a special-cased
// exclusion: opts.Tasks is a *ast.SelectorExpr, never a `var`-declared
// identifier in the checked block, and a function parameter is
// declared in the *ast.FuncType's field list, never by a `var` GenDecl
// inside the body — step 1 above only ever matches a local zero-value
// `var` statement, so neither shape is ever a candidate to begin with.
// The distinguishing feature this gate keys on is exactly what the
// mission brief names: a nil INITIALIZATION in the same lexical block
// as the test, not the test itself — a bare `if x != nil` with x
// declared anywhere else (a parameter, a field, an outer function's
// var later reassigned outside this block) never matches step 1/2
// together and is invisible to this gate, correctly.
//
// WHAT THIS GATE CANNOT SEE
// ----------------------------
// A decl and its dead check in DIFFERENT blocks (e.g. decl in the
// function body, check nested three ifs deep with no assignment on
// that path, but a DIFFERENT path through the function does assign it
// before some other, unrelated check) — this gate only pairs a decl
// with a check in the exact same block's statement list, matching the
// reported defect's own shape ("beside its own nil test") rather than
// attempting whole-function control-flow-graph reachability, which
// would need a far heavier analysis for a defect class this narrow.
// Precision over recall, same posture as check-nil-optional-deps.sh.
//
// Violations must appear in
// scripts/ci/allowlists/i19-dead-nil-branch.txt with a DATED
// justification naming the blocker and owner — same contract as every
// other gate in this directory. Allowlists shrink monotonically.
//
// Exit codes:
//
//	0 — every candidate (decl, check) pair either has an intervening
//	    assignment or is allowlisted
//	1 — the checker itself failed to build, load packages, or read the
//	    allowlist
//	2 — at least one statically-dead nil-check is neither disproven nor
//	    allowlisted, or the allowlist is stale
//
// Usage: bash scripts/ci/check-dead-nil-branch.sh (from anywhere).
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

var scanPatterns = []string{"./core/...", "./cmd/..."}

// modulePrefix bounds findCandidates/the discovery-floor count to
// packages this module owns. packages.Load's NeedDeps pulls the
// entire transitive dependency graph (stdlib + every third-party
// module) into the returned graph regardless of scanPatterns —
// packages.Visit walks all of it, not just the roots matched by
// scanPatterns — so without this filter the gate scans (and, before
// the address-of / if-init fixes below, false-positived against)
// hundreds of stdlib and vendor packages this repo does not own and
// could never allowlist sensibly. Confirmed empirically: an early,
// unfiltered run of this checker found 53 candidates, all but 6 in
// go/pkg/mod or the Go toolchain's own src/ tree, all real instances
// of two idioms this codebase uses constantly (database/sql's
// Scan(&ptr) and `if x, err = f(); err != nil`) that are NOT the
// defect class — fixed by writesTo's address-of case and the
// if-Init check, not by this filter, but scoping the scan to our own
// module keeps runtime down and keeps every reported violation
// something this repo can actually act on.
const modulePrefix = "github.com/kameas-ai/kenaz-harness"

const allowlistPath = "scripts/ci/allowlists/i19-dead-nil-branch.txt"

// overlayEnvVar names the environment variable
// gates_can_fail_test.go's planted-violation proof uses to redirect
// one source file's content at packages.Load time without ever
// writing to the real path — same technique
// checknilopts/main.go's overlayEnvVar uses, for the same reason (a
// hard kill under -timeout must never leave a tracked file mutated).
const overlayEnvVar = "DEAD_NIL_BRANCH_OVERLAY"

func loadOverlay() (map[string][]byte, error) {
	path := os.Getenv(overlayEnvVar)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // test-controlled path via env var, not user input
	if err != nil {
		return nil, fmt.Errorf("read overlay %s (%s): %w", overlayEnvVar, path, err)
	}
	var parsed struct {
		Replace map[string]string
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse overlay %s: %w", path, err)
	}
	overlay := make(map[string][]byte, len(parsed.Replace))
	for realPath, scratchPath := range parsed.Replace {
		content, err := os.ReadFile(scratchPath) //nolint:gosec // test-controlled scratch path
		if err != nil {
			return nil, fmt.Errorf("read overlay replacement %s: %w", scratchPath, err)
		}
		overlay[realPath] = content
	}
	return overlay, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "checkdeadnilbranch:", err)
		os.Exit(1)
	}
}

func run() error {
	overlay, err := loadOverlay()
	if err != nil {
		return err
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedFiles | packages.NeedImports | packages.NeedDeps,
		Overlay: overlay,
	}
	pkgs, err := packages.Load(cfg, scanPatterns...)
	if err != nil {
		return fmt.Errorf("load packages: %w", err)
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return fmt.Errorf("package load errors:\n%s", strings.Join(loadErrs, "\n"))
	}

	candidates, examined, err := findCandidates(pkgs)
	if err != nil {
		return err
	}

	// Discovery floor, part 1: zero packages scanned (a bad
	// scanPatterns, a broken go.mod, an empty overlay-only load) must
	// fail loudly, not report a suspiciously-empty clean.
	scannedPkgs := 0
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if len(p.Syntax) > 0 && strings.HasPrefix(p.PkgPath, modulePrefix) {
			scannedPkgs++
		}
	})
	if scannedPkgs == 0 {
		return fmt.Errorf("scanned zero packages with syntax under %v — this is almost certainly "+
			"a broken scanPatterns or a packages.Load failure, not a repository with no Go code",
			scanPatterns)
	}

	// Discovery floor, part 2: zero (decl, check) CANDIDATE PAIRS
	// examined — a `var x T` immediately followed by a same-object
	// `if x != nil` in the same block, independent of whether an
	// intervening assignment cleared it. This codebase is known to
	// contain hundreds of these (the `var err error; ...; if err !=
	// nil` idiom alone appears throughout core/), so finding zero means
	// nilCheckTarget / isZeroValueVarDecl / the object-identity match
	// is broken, not that the idiom vanished. This is the literal
	// "zero candidates found must FAIL loudly" floor UNIT-12 requires —
	// distinct from "zero VIOLATIONS", which a clean tree legitimately
	// produces every day.
	if examined == 0 {
		return fmt.Errorf("examined zero (var-decl, nil-check) candidate pairs across %d scanned "+
			"package(s) under core/+cmd/ — this codebase is known to contain hundreds of the "+
			"`var x T; ...; if x != nil` idiom (e.g. any `var err error` followed by an `if err "+
			"!= nil` in the same block), so finding none is almost certainly a broken matcher in "+
			"checkdeadnilbranch, not a codebase that stopped using the idiom", scannedPkgs)
	}

	var violations []string
	for _, c := range candidates {
		violations = append(violations, c.violationString())
	}
	sort.Strings(violations)

	allow, err := loadAllowlist(allowlistPath)
	if err != nil {
		return err
	}

	unlisted := diff(violations, allow)
	stale := diff(allow, violations)

	fmt.Printf("[dead-nil-branch] scanned %d package(s) under core/+cmd/, examined %d (var-decl, "+
		"nil-check) candidate pair(s): %d statically-dead (%d allowlisted, %d unlisted).\n",
		scannedPkgs, examined, len(violations), len(violations)-len(unlisted), len(unlisted))

	fail := false
	if len(unlisted) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[dead-nil-branch] FAIL: a nil-checked variable is provably nil at the check "+
			"(zero-value var declaration, no assignment before the check, same block), not in "+allowlistPath+":")
		for _, v := range unlisted {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[dead-nil-branch] Either assign the variable a real value before the check, or")
		fmt.Fprintln(os.Stderr, "[dead-nil-branch] add a DATED line to "+allowlistPath+" naming the blocker and owner.")
		fail = true
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[dead-nil-branch] FAIL: STALE entries in "+allowlistPath+" — no longer a violation:")
		for _, v := range stale {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr, "[dead-nil-branch] Delete the line(s) — allowlists shrink monotonically.")
		fail = true
	}

	if fail {
		os.Exit(2)
	}

	fmt.Println("[dead-nil-branch] clean.")
	return nil
}

// candidate is one statically-dead (decl, check) pair.
type candidate struct {
	varName   string
	declFile  string
	declLine  int
	checkFile string
	checkLine int
}

func (c *candidate) violationString() string {
	return fmt.Sprintf("%s:%d: %s is declared with its zero value here and checked `!= nil` at %s:%d "+
		"with no intervening assignment — that branch is statically dead",
		c.declFile, c.declLine, c.varName, c.checkFile, c.checkLine)
}

// findCandidates walks every block statement in every loaded package's
// syntax and returns every statically-dead (decl, check) pair found,
// plus the total count of (decl, check) pairs EXAMINED regardless of
// verdict (assigned or not) — the discovery-floor signal: this
// codebase is known to contain hundreds of `var x T` / `if x != nil`
// pairs (the `var err error; ...; if err != nil` idiom alone), so a
// run that examines zero pairs almost certainly has a broken matcher
// (nilCheckTarget, isZeroValueVarDecl, or the object-identity
// comparison silently never matching), not a codebase that stopped
// using this idiom.
func findCandidates(pkgs []*packages.Package) ([]candidate, int, error) {
	var out []candidate
	examined := 0
	var walkErr error
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if walkErr != nil || p.TypesInfo == nil || !strings.HasPrefix(p.PkgPath, modulePrefix) {
			return
		}
		for _, file := range p.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				block, ok := n.(*ast.BlockStmt)
				if !ok {
					return true
				}
				violations, blockExamined := findInBlock(block, p, p.Fset)
				out = append(out, violations...)
				examined += blockExamined
				return true
			})
		}
	})
	if walkErr != nil {
		return nil, 0, walkErr
	}
	return out, examined, nil
}

// findInBlock finds every statically-dead (decl, check) pair whose
// decl and check statements are BOTH direct members of block's own
// statement list (not nested) — the exact "beside its own nil test"
// shape this gate targets. Also returns the count of (decl, check)
// pairs examined in this block, independent of verdict — see
// findCandidates' doc for why that count backs the discovery floor.
func findInBlock(block *ast.BlockStmt, p *packages.Package, fset *token.FileSet) ([]candidate, int) {
	type decl struct {
		obj   types.Object
		name  string
		index int
		pos   token.Pos
	}
	var decls []decl

	for i, stmt := range block.List {
		gd, ok := isZeroValueVarDecl(stmt, p.TypesInfo)
		if !ok {
			continue
		}
		for _, d := range gd {
			decls = append(decls, decl{obj: d.obj, name: d.name, index: i, pos: d.pos})
		}
	}
	if len(decls) == 0 {
		return nil, 0
	}

	var out []candidate
	examined := 0
	for i, stmt := range block.List {
		ifStmt, ok := stmt.(*ast.IfStmt)
		if !ok {
			continue
		}
		obj, checkPos, ok := nilCheckTarget(ifStmt, p.TypesInfo)
		if !ok {
			continue
		}
		for _, d := range decls {
			if d.obj != obj || d.index >= i {
				continue
			}
			examined++
			// Scan every statement strictly between the decl and this
			// check, recursively, for a write to the same object.
			assigned := false
			for j := d.index + 1; j < i && !assigned; j++ {
				if writesTo(block.List[j], obj, p.TypesInfo) {
					assigned = true
				}
			}
			// The if statement's OWN init clause counts too — the
			// extremely common `if x, err = f(); err != nil` shape
			// (crypto/x509.MarshalPKIXPublicKey is exactly this)
			// assigns in the SAME statement as the check, not a prior
			// one, and is obviously not the dead-branch defect class.
			if !assigned && ifStmt.Init != nil && writesTo(ifStmt.Init, obj, p.TypesInfo) {
				assigned = true
			}
			if assigned {
				continue
			}
			declPos := fset.Position(d.pos)
			chkPos := fset.Position(checkPos)
			out = append(out, candidate{
				varName:   d.name,
				declFile:  relToRepoRoot(declPos.Filename),
				declLine:  declPos.Line,
				checkFile: relToRepoRoot(chkPos.Filename),
				checkLine: chkPos.Line,
			})
		}
	}
	return out, examined
}

type zeroValueDecl struct {
	obj  types.Object
	name string
	pos  token.Pos
}

// isZeroValueVarDecl reports the zero-value `var x T` declarations
// (no initializer, T nilable) directly represented by stmt, if any.
// A single `var` statement can declare several names
// (`var a, b T`), hence the slice return.
func isZeroValueVarDecl(stmt ast.Stmt, info *types.Info) ([]zeroValueDecl, bool) {
	declStmt, ok := stmt.(*ast.DeclStmt)
	if !ok {
		return nil, false
	}
	gd, ok := declStmt.Decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return nil, false
	}
	var out []zeroValueDecl
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || vs.Type == nil || len(vs.Values) != 0 {
			// Values != 0 means it has an initializer — not a
			// zero-value declaration, out of scope for this gate
			// regardless of what the initializer is.
			continue
		}
		if !isNilable(info.TypeOf(vs.Type)) {
			continue
		}
		for _, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			obj := info.Defs[name]
			if obj == nil {
				continue
			}
			out = append(out, zeroValueDecl{obj: obj, name: name.Name, pos: name.Pos()})
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// isNilable reports whether t's underlying type can hold a literal nil.
func isNilable(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Interface, *types.Pointer, *types.Map, *types.Slice, *types.Chan, *types.Signature:
		return true
	default:
		return false
	}
}

// nilCheckTarget reports the object being nil-checked by `if x != nil`
// or `if nil != x` — the top-level condition ONLY (not e.g. `if x !=
// nil && y`), matching the reported defect's own bare-comparison shape
// and keeping this gate conservative.
func nilCheckTarget(ifStmt *ast.IfStmt, info *types.Info) (types.Object, token.Pos, bool) {
	bin, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return nil, 0, false
	}
	var identExpr ast.Expr
	switch {
	case isBareNil(bin.X):
		identExpr = bin.Y
	case isBareNil(bin.Y):
		identExpr = bin.X
	default:
		return nil, 0, false
	}
	ident, ok := identExpr.(*ast.Ident)
	if !ok {
		return nil, 0, false
	}
	obj := info.Uses[ident]
	if obj == nil {
		return nil, 0, false
	}
	return obj, ifStmt.Cond.Pos(), true
}

func isBareNil(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// writesTo reports whether node (recursively — into every nested
// block, closure, and control-flow arm) either (a) assigns obj
// directly (`x = ...` / `x, y := ...`) or (b) takes obj's address
// (`&x`) at all. (b) is deliberately broad: `var p *string; ...
// rows.Scan(&p); if p != nil` is the single most common shape a `var`
// + `!= nil` pair takes in this codebase (database/sql's Scan-into-
// pointer idiom, core/slashcmd/store_user.go and
// core/workflows/scheduler/storage.go among many), and Scan mutates
// through that pointer via reflection — a syntactic assignment this
// gate can never see directly. Treating ANY address-of as "might be
// written" trades a few missed true positives (a `&x` that merely
// aliases without writing) for zero false positives on this idiom —
// the same precision-over-recall posture as check-nil-optional-deps.sh.
func writesTo(node ast.Node, obj types.Object, info *types.Info) bool {
	if node == nil {
		return false
	}
	found := false
	matches := func(e ast.Expr) bool {
		ident, ok := e.(*ast.Ident)
		if !ok {
			return false
		}
		return info.Defs[ident] == obj || info.Uses[ident] == obj
	}
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if matches(lhs) {
					found = true
					return false
				}
			}
		case *ast.UnaryExpr:
			if v.Op == token.AND && matches(v.X) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func relToRepoRoot(abs string) string {
	root, err := os.Getwd()
	if err == nil {
		if rel, err2 := filepath.Rel(root, abs); err2 == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(abs)
}

func loadAllowlist(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative path, not user input
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out, nil
}

// diff returns elements of a not present in b (set difference), sorted.
func diff(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
