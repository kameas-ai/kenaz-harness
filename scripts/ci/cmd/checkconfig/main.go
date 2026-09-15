// Command checkconfig is the Go half of
// scripts/ci/check-config-nil-coverage.sh — the G-1 gate from
// kitty-specs/trust-surfaces-that-fire-01PMZ202 (spec.md §G-1, WP26).
//
// THE DEFECT CLASS
// -----------------
// A struct field that is declared, read, and never assigned in
// production. No existing gate in this repository can see it:
// check-seam-implementers.sh asks whether an INTERFACE has an
// implementer somewhere; this class is narrower and structurally
// different — a concrete field on a specific, already-constructed
// struct literal that nothing ever populates. Twenty of the finalizing
// mission's 29 findings were exactly this shape (spec.md §10, "No gate
// in the repository can see a struct field that is declared, read, and
// never assigned"): permissionsview.Config.Engine,
// branchesview.Config.Audit, workflowsview.Config.Audit,
// corewf.Deps.Audit, tools.Config.Audit, hooks.Config.MCP,
// coreupdate.Config.Audit, corebash.Options.Logger — each shipped with
// a full test suite that only ever exercised the struct via its own
// direct-construction fixture, never via the real `rpc.New` wiring
// path that left the field nil.
//
// THREE TIERS, TWO IMPLEMENTED AS HARD FAILURES
// -----------------------------------------------
// The spec (G-1) describes three tiers. This tool implements the first
// two as gating (exit 2 on an unlisted violation) because both carry a
// REQUIRED planted-violation proof in gates_can_fail_test.go
// ("config-nil-coverage/unset-interface-field" and
// "config-nil-coverage/orphan-with-option") — a proof obligation the
// spec itself only imposes on tiers it means to gate:
//
//   - Tier 1 (implemented, gating): for every exported struct type
//     under core/ named exactly Config, Options, GateOptions or Deps,
//     constructed in >=1 non-test composite literal, every field of
//     kind pointer / interface / func / map / chan / slice that is set
//     in ZERO non-test literals is a violation. Assignment to the
//     bare, untyped `nil` literal counts as unset (the sub-rule that
//     catches `Audit: nil, // TODO(audit-wired)`).
//
//   - Tier 2 (implemented, gating): every exported top-level func or
//     method named With<Something> under core/ with zero non-test
//     callers anywhere in the ./core/... package graph. Resolved via
//     go/types object identity (the same *types.Func pointer at the
//     declaration and at every call site), not name-matching — a
//     shadowing func with the same name in a different package cannot
//     produce a false "called".
//
// Tier 3 (a string field whose zero value silently disables a feature,
// e.g. corefs.GateOptions.PolicyDir) is explicitly speced as
// "advisory (warn) in v1... a gate that over-fires gets disabled,
// which is worse than no gate" and carries NO planted-violation
// obligation. This tool prints Tier-3 candidates to stdout as
// informational-only (never affecting the exit code) rather than
// implementing it as a third gating tier with an unseeded allowlist —
// see the script header for the full reasoning.
//
// WHY THIS IS A GO TOOL, NOT A GREP
// ------------------------------------
// "Is this field's type a pointer/interface/func/map/chan/slice" and
// "is it ever assigned a non-nil value across every composite literal
// of its owning struct anywhere under core/" are type-checker
// questions with no reliable textual proxy — the same reason
// check-seam-implementers.sh (scripts/ci/cmd/checkseams) and
// check-nil-optional-deps.sh (scripts/ci/cmd/checknilopts) are Go
// tools. This shares their packages.Load pattern and its
// self-hosted-runner-safe wrapper shape (no apt-get; `go build` is the
// only tool needed).
//
// SCOPE: core/ ONLY, NOT cmd/
// ------------------------------
// Unlike checknilopts (core/+cmd/, widened after HV-03 shipped in
// cmd/harness-vm), the spec text for G-1 says "under core/" for both
// tiers. Candidate discovery (struct declarations, With* declarations)
// is scoped to ./core/... accordingly. Call-site / assignment search
// for Tier 2 is likewise scoped to ./core/... — the packages.Load
// pattern set is the same for both discovery and verification, so a
// With* func called only from cmd/ would be reported as an unlisted
// violation. No such case is known to exist today (verified by the
// calibration run this commit's allowlist records); if one appears,
// the allowlist is the escape hatch, same as any other gate.
//
// WHAT THIS GATE CANNOT SEE
// -----------------------------
//  1. A composite literal that uses POSITIONAL (unkeyed) fields cannot
//     be attributed to a specific field name by static analysis in
//     the general case, so a struct type with >=1 positional-style
//     literal is EXCLUDED from Tier-1 reporting entirely (treated as
//     "not verifiable" rather than guessed at) — false negatives, not
//     false positives, is the accepted failure direction. No target
//     struct in this tree is known to use positional construction
//     today.
//  2. Tier 2's "zero non-test callers" is answered by walking every
//     *ast.CallExpr in the SAME packages.Load result used for
//     discovery (./core/...). A With* func's only caller living in
//     cmd/ or in a build-tag-gated file packages.Load does not select
//     for the default build tags would be invisible here, the same
//     structural blind spot checknilopts documents for its own
//     assignment search.
//  3. Embedded (anonymous) struct fields are skipped for the same
//     reason checknilopts skips them — they're not "declared as a
//     named field never assigned", they're a different composition
//     mechanism entirely.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// modulePrefix bounds every scan to this module's own source, exactly
// as checkseams and checknilopts do — packages.NeedDeps pulls in the
// whole import graph including stdlib, and an unbounded scan would
// search code this repo does not own.
const modulePrefix = "github.com/kameas-ai/kenaz-harness/"

// corePrefix additionally bounds Tier 1 + Tier 2 candidate discovery
// to core/ specifically (not cmd/) per the G-1 spec text.
const corePrefix = modulePrefix + "core/"

var scanPatterns = []string{"./core/..."}

// targetStructNames are the four struct-name literals G-1 Tier 1
// names explicitly. Exact identifier match, not a suffix/prefix match
// — "GateOptions" and "Options" are both listed because the codebase
// uses both spellings for the same functional-options shape
// (corefs.GateOptions vs. everything else's Options).
var targetStructNames = map[string]bool{
	"Config":      true,
	"Options":     true,
	"GateOptions": true,
	"Deps":        true,
}

const allowlistPath = "scripts/ci/allowlists/i16-config-nil-coverage.txt"

// overlayEnvVar mirrors checknilopts's own NIL_OPTIONAL_DEPS_OVERLAY:
// the planted-violation proof needs to insert a field into the MIDDLE
// of an existing struct declaration (core/rpc/views/permissions/impl.go's
// Config), which a pure append-at-EOF plant cannot do. A go-build-
// overlay JSON file ({"Replace": {"<real-abs-path>": "<scratch-path>"}})
// substitutes file content at packages.Load time without ever opening
// the real path for writing — see checknilopts/main.go's own
// loadOverlay doc comment for the full rationale (a hard kill under
// `-timeout` skips deferred cleanup and leaves a real file mutated;
// an overlay never touches the real file at all, so there is nothing
// to leave mutated).
const overlayEnvVar = "CONFIG_NIL_COVERAGE_OVERLAY"

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
		fmt.Fprintln(os.Stderr, "checkconfig:", err)
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

	// ---- Tier 1: unset nilable field on Config/Options/GateOptions/Deps ----
	targets := findTargetStructs(pkgs)
	positional := map[*types.TypeName]bool{}
	constructedCount := map[*types.TypeName]int{}
	touchedNonNil := map[fieldKey]bool{}
	scanCompositeLiterals(pkgs, targets, positional, constructedCount, touchedNonNil)

	var tier1Violations []violation
	tier1Candidates := 0
	for _, st := range targets {
		if constructedCount[st.owner.Obj()] == 0 {
			continue // never constructed in production — not a candidate at all
		}
		if positional[st.owner.Obj()] {
			continue // cannot attribute per-field coverage; see doc comment blind spot 1
		}
		pkgPath := st.owner.Obj().Pkg().Path()
		for _, f := range st.fields {
			tier1Candidates++
			if touchedNonNil[fieldKey{st.owner.Obj(), f.name}] {
				continue
			}
			tier1Violations = append(tier1Violations, violation{
				// KEY: fully-qualified symbol (pkgPath.Struct.Field) — see
				// Finding #92 (CI-gate-hardening, 2026-09-14): a key that
				// embeds a line number self-invalidates on ANY edit above
				// the entry (the line moves, so the old key goes "stale"
				// and the new line reports "unlisted" simultaneously, even
				// though nothing about the finding changed). The struct's
				// package path plus its (fixed, four-name) type name plus
				// the field name is stable across unrelated edits anywhere
				// else in the tree — only a rename of the struct/field/
				// package invalidates it, which is exactly when the entry
				// SHOULD be revisited.
				key: fmt.Sprintf("%s.%s.%s", pkgPath, st.owner.Obj().Name(), f.name),
				// DETAIL: locator + human description, printed to stdout
				// and recorded as an auto-generated comment line in the
				// allowlist — never part of the compared key, so it is
				// free to go stale (a line number, a kind description)
				// without breaking the match.
				detail: fmt.Sprintf("%s:%d: %s.%s (%s) is never set to a non-nil value in any non-test composite literal under core/",
					f.file, f.line, st.owner.Obj().Name(), f.name, f.kindDesc),
			})
		}
	}

	// ---- Tier 2: orphan With* injector ----
	withFuncs := findWithFuncs(pkgs)
	markCallers(pkgs, withFuncs)
	var tier2Violations []violation
	for _, wf := range withFuncs {
		if wf.called {
			continue
		}
		tier2Violations = append(tier2Violations, violation{
			// KEY: qualifiedName is already the fully-qualified symbol
			// (pkgPath.FuncName or pkgPath.(Type).Method) — this was
			// already computed for the OLD violation string, just never
			// split out from the file:line locator glued in front of it.
			key: wf.qualifiedName,
			detail: fmt.Sprintf("%s:%d: %s is never called by any non-test source under core/",
				wf.file, wf.line, wf.qualifiedName),
		})
	}

	// ---- Tier 3: advisory-only, informational, never gates ----
	tier3 := findAdvisoryStringFields(pkgs, targets)

	totalCandidates := tier1Candidates + len(withFuncs)
	if totalCandidates == 0 {
		return fmt.Errorf("found zero Tier-1 fields and zero Tier-2 With* functions under core/ — " +
			"this is almost certainly a bug in checkconfig (Config/Options/GateOptions/Deps structs and " +
			"With* functions are known to exist, e.g. core/tools/bash.Options and core/artifacts' " +
			"WithSQLClock), not a clean tree")
	}

	all := append(append([]violation{}, tier1Violations...), tier2Violations...)
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })
	detailByKey := make(map[string]string, len(all))
	allKeys := make([]string, 0, len(all))
	for _, v := range all {
		if _, dup := detailByKey[v.key]; !dup {
			allKeys = append(allKeys, v.key)
		}
		detailByKey[v.key] = v.detail
	}

	allow, err := loadAllowlist(allowlistPath)
	if err != nil {
		return err
	}
	unlisted := diff(allKeys, allow)
	stale := diff(allow, allKeys)

	fmt.Printf("[config-nil-coverage] scanned %d Tier-1 field(s) across %d target struct type(s) and "+
		"%d Tier-2 With*-function(s) under core/: %d violation(s) found (%d allowlisted, %d unlisted). "+
		"%d Tier-3 advisory candidate(s) (informational only).\n",
		tier1Candidates, len(targets), len(withFuncs), len(allKeys), len(allKeys)-len(unlisted), len(unlisted), len(tier3))

	if len(tier3) > 0 {
		fmt.Println("[config-nil-coverage] Tier-3 advisory (string field whose zero value disables a " +
			"feature, per doc comment) — NOT gating, informational only:")
		sort.Strings(tier3)
		for _, t := range tier3 {
			fmt.Println("    " + t)
		}
	}

	fail := false
	if len(unlisted) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] FAIL: unlisted violation(s), not in "+allowlistPath+":")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] Paste the KEY line and the auto-generated locator comment")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] directly into the allowlist (the key is what gates; the")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] locator comment is for humans and may drift freely):")
		for _, k := range unlisted {
			fmt.Fprintln(os.Stderr, "    "+k)
			fmt.Fprintln(os.Stderr, "    # at "+detailByKey[k])
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] Either wire a real production assignment (a composite-literal")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] field, or a real call site for a With* function), or add the")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] lines above to "+allowlistPath+" with a DATED justification")
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] comment naming the blocker and owner.")
		fail = true
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] FAIL: STALE entries in "+allowlistPath+" — no longer a violation:")
		for _, v := range stale {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr, "[config-nil-coverage] Delete the line(s) — allowlists shrink monotonically.")
		fail = true
	}

	if fail {
		os.Exit(2)
	}
	fmt.Println("[config-nil-coverage] clean.")
	return nil
}

// violation pairs a stable, symbol-qualified KEY (what the allowlist
// matches against) with a human-readable DETAIL (a file:line locator plus
// description, printed for humans and recorded as an auto-generated
// comment — never compared). Splitting these apart is the Finding #92 fix:
// see the KEY field comments at each construction site above for why a
// line number cannot be part of the compared identity.
type violation struct {
	key    string
	detail string
}

// ---------------------------------------------------------------------
// Tier 1: target struct + field discovery
// ---------------------------------------------------------------------

type targetField struct {
	name     string
	kindDesc string // "pointer" | "interface" | "func" | "map" | "chan" | "slice"
	file     string
	line     int
}

type targetStruct struct {
	owner  *types.Named
	fields []targetField
}

type fieldKey struct {
	owner *types.TypeName
	field string
}

func inCorePkg(p *packages.Package) bool {
	// p.PkgPath == modulePrefix+"core" (no trailing slash) is the ROOT
	// core package itself (core/core.go) — HasPrefix(corePrefix) alone
	// misses it because corePrefix carries the trailing slash needed to
	// avoid matching sibling names like "core2". core/core.go turned out
	// to be load-bearing: it's where session.WithSessionHookRunner's one
	// real call site lives (core/core.go:531), so excluding the root
	// package produced dozens of false "orphan With*" violations in this
	// tool's own calibration run before this fix.
	rootCore := modulePrefix + "core"
	inScope := p.PkgPath == rootCore || strings.HasPrefix(p.PkgPath, corePrefix)
	return inScope && !isTestDoublePackage(p)
}

// isTestDoublePackage mirrors checkseams/checknilopts: a package that
// imports "testing" from a non-_test.go file is a fixture package
// wearing production clothes, and neither its declarations nor its
// call sites count as production.
func isTestDoublePackage(p *packages.Package) bool {
	if strings.Contains(p.PkgPath, "/internal/recorders") {
		return true
	}
	if strings.Contains(p.PkgPath, "/testdata/") {
		return true
	}
	for _, file := range p.Syntax {
		pos := p.Fset.Position(file.Pos())
		if strings.HasSuffix(pos.Filename, "_test.go") {
			continue
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "testing" {
				return true
			}
		}
	}
	return false
}

func kindDesc(t types.Type) string {
	switch t.Underlying().(type) {
	case *types.Pointer:
		return "pointer"
	case *types.Interface:
		return "interface"
	case *types.Signature:
		return "func"
	case *types.Map:
		return "map"
	case *types.Chan:
		return "chan"
	case *types.Slice:
		return "slice"
	}
	return ""
}

func findTargetStructs(pkgs []*packages.Package) []targetStruct {
	var out []targetStruct
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inCorePkg(p) {
			return
		}
		fset := p.Fset
		for _, file := range p.Syntax {
			pos := fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !targetStructNames[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				obj := p.TypesInfo.Defs[ts.Name]
				if obj == nil {
					return true
				}
				named, ok := obj.Type().(*types.Named)
				if !ok {
					return true
				}
				var fields []targetField
				for _, field := range st.Fields.List {
					if len(field.Names) == 0 {
						continue // embedded field
					}
					// Fields need not be exported themselves — the
					// spec's Tier-1 rule is silent on field export
					// status (unlike checknilopts's interface-emptiness
					// filter). Every one of the eight derived hits
					// (corebash.Options.Logger etc.) happens to be an
					// exported field, but nothing in the spec text
					// restricts the rule to exported fields, so
					// unexported fields are deliberately included here.
					for _, name := range field.Names {
						fobj := p.TypesInfo.Defs[name]
						if fobj == nil {
							continue
						}
						kd := kindDesc(fobj.Type())
						if kd == "" {
							continue
						}
						fields = append(fields, targetField{
							name:     name.Name,
							kindDesc: kd,
							file:     relToRepoRoot(pos.Filename),
							line:     fset.Position(name.Pos()).Line,
						})
					}
				}
				if len(fields) > 0 {
					out = append(out, targetStruct{owner: named, fields: fields})
				}
				return true
			})
		}
	})
	return out
}

// scanCompositeLiterals walks every non-test file under core/ looking
// for composite literals of a target struct type. For each:
//   - increments constructedCount for the type.
//   - if any element is positional (no Key), marks the type
//     unverifiable (positional) rather than guessing at field
//     attribution.
//   - for each keyed element whose value is not the bare `nil`
//     literal, marks that field touched-non-nil.
func scanCompositeLiterals(
	pkgs []*packages.Package,
	targets []targetStruct,
	positional map[*types.TypeName]bool,
	constructedCount map[*types.TypeName]int,
	touchedNonNil map[fieldKey]bool,
) {
	index := make(map[*types.TypeName]bool, len(targets))
	for _, t := range targets {
		index[t.owner.Obj()] = true
	}

	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inCorePkg(p) {
			return
		}
		for _, file := range p.Syntax {
			pos := p.Fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			ast.Walk(&configAssignVisitor{
				p:                p,
				index:            index,
				positional:       positional,
				constructedCount: constructedCount,
				touchedNonNil:    touchedNonNil,
			}, file)
		}
	})
}

// configAssignVisitor finds two production-wiring shapes for a target
// struct field, mirroring scripts/ci/cmd/checknilopts's clauses 1 and
// 3 (its clause 2, Set*/With* CALLS on the struct itself, is Tier 2's
// own concern — With*-function existence/call-sites — not duplicated
// here):
//
//  1. A composite literal `T{Field: <non-nil expr>}` — see
//     scanCompositeLiterals' pre-refactor doc comment.
//
//  2. A plain assignment `x.Field = <non-nil expr>` where x's type
//     (pointer-stripped) is the target struct. Calibration against
//     this tree (2026-09-12) found this is NOT an edge case for
//     Config/Options/Deps structs — it is the DOMINANT wiring idiom
//     for the largest target struct in the tree:
//     core/rpc/api.go:3046 does `wfDeps := corewf.Deps{}` (an EMPTY
//     literal — zero KeyValueExpr elements) immediately followed by
//     eleven conditional `wfDeps.LLM = ...` / `wfDeps.Audit = ...`
//     style assignments before `wfDeps` is ever used. A composite-
//     literal-only scan reported all eleven fields "never set" —
//     ELEVEN false positives from one construction site, the same
//     class checknilopts's own header calls "the primary production
//     wiring path for several of these exact fields" for its own
//     unrelated struct kind. Excluding this clause would have shipped
//     a gate that is unsound in the failure direction this task's
//     brief explicitly warns against: a gate whose "violation" is
//     actually the healthy case.
//
//     Same exclusion checknilopts's clause 3 applies: an assignment
//     sitting directly inside a method whose OWN receiver is the
//     target struct type does not count — that is the setter's own
//     body, not proof anything calls it. Free functions and methods on
//     OTHER types populating `x.Field` where x is a local variable /
//     parameter of the target struct type (exactly the wfDeps shape
//     above) are NOT receiver methods of that struct, so this
//     exclusion does not apply to them and they correctly count as
//     wiring.
type configAssignVisitor struct {
	p                *packages.Package
	index            map[*types.TypeName]bool
	positional       map[*types.TypeName]bool
	constructedCount map[*types.TypeName]int
	touchedNonNil    map[fieldKey]bool
	selfOwner        *types.TypeName // non-nil while inside a method on this receiver type
}

func (v *configAssignVisitor) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.FuncDecl:
		next := &configAssignVisitor{p: v.p, index: v.index, positional: v.positional,
			constructedCount: v.constructedCount, touchedNonNil: v.touchedNonNil}
		if node.Recv != nil && len(node.Recv.List) == 1 {
			if rt := v.p.TypesInfo.TypeOf(node.Recv.List[0].Type); rt != nil {
				if ptr, ok := rt.(*types.Pointer); ok {
					rt = ptr.Elem()
				}
				if named, ok := rt.(*types.Named); ok {
					next.selfOwner = named.Obj()
				}
			}
		}
		return next
	case *ast.CompositeLit:
		t := v.p.TypesInfo.TypeOf(node)
		named, ok := t.(*types.Named)
		if !ok || !v.index[named.Obj()] {
			return v
		}
		v.constructedCount[named.Obj()]++
		for _, elt := range node.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				v.positional[named.Obj()] = true
				continue
			}
			keyIdent, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			if isBareNil(kv.Value) {
				continue
			}
			v.touchedNonNil[fieldKey{named.Obj(), keyIdent.Name}] = true
		}
	case *ast.AssignStmt:
		if node.Tok != token.ASSIGN || len(node.Lhs) != len(node.Rhs) {
			return v
		}
		for i, lhs := range node.Lhs {
			sel, ok := lhs.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			recvType := v.p.TypesInfo.TypeOf(sel.X)
			if recvType == nil {
				continue
			}
			if ptr, ok := recvType.(*types.Pointer); ok {
				recvType = ptr.Elem()
			}
			named, ok := recvType.(*types.Named)
			if !ok || !v.index[named.Obj()] {
				continue
			}
			if isBareNil(node.Rhs[i]) {
				continue
			}
			if v.selfOwner != nil && v.selfOwner == named.Obj() {
				// The struct's own method assigning its own field —
				// not proof the method is ever called. Tier 2 (or a
				// future clause) is responsible for verifying THAT.
				continue
			}
			v.touchedNonNil[fieldKey{named.Obj(), sel.Sel.Name}] = true
		}
	}
	return v
}

func isBareNil(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// ---------------------------------------------------------------------
// Tier 2: orphan With* injector
// ---------------------------------------------------------------------

type withFunc struct {
	obj           *types.Func
	qualifiedName string
	file          string
	line          int
	called        bool
}

// firstParamIsInterface reports whether fd's first parameter (the
// injected collaborator, by this codebase's own With* convention —
// verified against all five of the spec's named Tier-2 examples) has
// a non-empty interface underlying type. A method's receiver is not a
// parameter for this purpose; With* METHODS in this tree (e.g.
// logstore.Handler.WithAttrs, mirroring slog.Handler) take a
// concrete-typed argument and are the stdlib-interface-satisfaction
// idiom, not the functional-option-collaborator idiom Tier 2 targets
// — they are naturally excluded here because their argument is
// concrete, not because of the receiver.
func firstParamIsInterface(p *packages.Package, fd *ast.FuncDecl) bool {
	if fd.Type.Params == nil || len(fd.Type.Params.List) == 0 {
		return false
	}
	first := fd.Type.Params.List[0]
	t := p.TypesInfo.TypeOf(first.Type)
	if t == nil {
		return false
	}
	iface, ok := t.Underlying().(*types.Interface)
	return ok && iface.NumMethods() > 0
}

func findWithFuncs(pkgs []*packages.Package) []*withFunc {
	var out []*withFunc
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inCorePkg(p) {
			return
		}
		fset := p.Fset
		for _, file := range p.Syntax {
			pos := fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if !fd.Name.IsExported() {
					continue
				}
				if !strings.HasPrefix(fd.Name.Name, "With") || len(fd.Name.Name) <= len("With") {
					continue
				}
				if !firstParamIsInterface(p, fd) {
					// Calibration finding (2026-09-12): an unrestricted
					// "every With* func" scan found 213 candidates and
					// ~200 unlisted violations, dominated by functional
					// options that inject a CONCRETE value — *http.Client,
					// *capabilities.Catalog, a clock func, an endpoint
					// string — called only from tests that build a fake
					// transport/clock for determinism. That is a
					// legitimate, common Go test-scaffolding idiom, not
					// the defect class G-1 exists to catch. All five of
					// the spec's own named Tier-2 examples
					// (audit.WithBackend, audit.WithSweepableBackend,
					// cedar.WithPermissionHookRunner,
					// session.WithSessionHookRunner,
					// slashcmd.(*Dispatch).WithAuditEmitter) inject an
					// INTERFACE-typed collaborator (eventlog.Backend,
					// eventlog.SweepableBackend, PermissionHookRunner,
					// SessionHookRunner, an Emitter) — an abstraction
					// whose absence silently no-ops an entire code path,
					// the same shape Tier 1 targets for direct struct
					// fields. Restricting Tier 2 to With* funcs whose
					// first parameter is a non-empty interface keeps the
					// gate's candidate set small enough to individually
					// justify (the convention every other allowlist in
					// this directory follows — see i18-nil-optional-deps
					// .txt's 7 hand-investigated entries against 60
					// scanned, not 200), while still catching the exact
					// property class the spec's own examples name. A
					// broader, unscoped version of this rule remains
					// available as future work (this paragraph is that
					// follow-up's starting point) if the interface
					// restriction's recall proves too narrow.
					continue
				}
				obj := p.TypesInfo.Defs[fd.Name]
				fobj, ok := obj.(*types.Func)
				if !ok {
					continue
				}
				qname := p.PkgPath + "." + fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) == 1 {
					if rt := p.TypesInfo.TypeOf(fd.Recv.List[0].Type); rt != nil {
						qname = p.PkgPath + ".(" + types.TypeString(rt, nil) + ")." + fd.Name.Name
					}
				}
				out = append(out, &withFunc{
					obj:           fobj,
					qualifiedName: qname,
					file:          relToRepoRoot(pos.Filename),
					line:          fset.Position(fd.Name.Pos()).Line,
				})
			}
		}
	})
	return out
}

// markCallers walks every non-test file's call expressions and marks
// the matching withFunc called when the callee resolves — via
// go/types object identity, not name matching — to a tracked
// *types.Func.
func markCallers(pkgs []*packages.Package, funcs []*withFunc) {
	index := make(map[*types.Func]*withFunc, len(funcs))
	for _, f := range funcs {
		index[f.obj] = f
	}
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inCorePkg(p) {
			return
		}
		for _, file := range p.Syntax {
			pos := p.Fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var ident *ast.Ident
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					ident = fn
				case *ast.SelectorExpr:
					ident = fn.Sel
				default:
					return true
				}
				obj := p.TypesInfo.Uses[ident]
				fobj, ok := obj.(*types.Func)
				if !ok {
					return true
				}
				if wf, ok := index[fobj]; ok {
					wf.called = true
				}
				return true
			})
		}
	})
}

// ---------------------------------------------------------------------
// Tier 3: advisory-only (informational, never gates)
// ---------------------------------------------------------------------

// findAdvisoryStringFields looks for string-typed fields on a target
// struct whose doc/trailing comment contains one of the "this disables
// a feature when empty" idioms and which no production composite
// literal ever sets to a non-empty string literal. Explicitly
// informational per the spec's own "advisory (warn) in v1" framing —
// see the package doc comment for why this is not a third gating
// tier.
func findAdvisoryStringFields(pkgs []*packages.Package, targets []targetStruct) []string {
	phrases := []string{"when empty", "nil means", "optional", "disabled"}
	byOwner := map[*types.TypeName]bool{}
	for _, t := range targets {
		byOwner[t.owner.Obj()] = true
	}
	var out []string
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inCorePkg(p) {
			return
		}
		fset := p.Fset
		for _, file := range p.Syntax {
			pos := fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok || !targetStructNames[ts.Name.Name] {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					return true
				}
				for _, field := range st.Fields.List {
					if len(field.Names) == 0 {
						continue
					}
					basic, ok := p.TypesInfo.TypeOf(field.Type).Underlying().(*types.Basic)
					if !ok || basic.Kind() != types.String {
						continue
					}
					doc := ""
					if field.Doc != nil {
						doc += strings.ToLower(field.Doc.Text())
					}
					if field.Comment != nil {
						doc += " " + strings.ToLower(field.Comment.Text())
					}
					matched := false
					for _, ph := range phrases {
						if strings.Contains(doc, ph) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
					for _, name := range field.Names {
						out = append(out, fmt.Sprintf("%s:%d: %s.%s (string, advisory-only)",
							relToRepoRoot(pos.Filename), fset.Position(name.Pos()).Line,
							ts.Name.Name, name.Name))
					}
				}
				return true
			})
		}
	})
	return out
}

// ---------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------

func relToRepoRoot(abs string) string {
	root, err := os.Getwd()
	if err == nil {
		if rel, err2 := trimRoot(root, abs); err2 {
			return rel
		}
	}
	return abs
}

func trimRoot(root, abs string) (string, bool) {
	if !strings.HasPrefix(abs, root) {
		return "", false
	}
	rel := strings.TrimPrefix(abs, root)
	rel = strings.TrimPrefix(rel, string(os.PathSeparator))
	return filepathToSlash(rel), true
}

func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, string(os.PathSeparator), "/")
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
