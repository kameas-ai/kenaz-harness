// Command checknilopts is the Go half of
// scripts/ci/check-nil-optional-deps.sh — the nil-optional-dependency
// gate.
//
// THE DEFECT CLASS
// -----------------
// An interface-typed field on a struct, documented by its own author as
// optional ("nil is allowed" / "nil causes …" / "nil disables …"), that
// no production wiring site ever assigns. The type system says the
// capability exists; the doc comment even says what "off" looks like;
// nothing ever turns it on. Six confirmed instances shipped as real
// defects before this gate existed (docs/unwired-ledger.md, 2026-08-20
// entry): ChatRunDispatcher, wfsched.Dispatcher, registry.Options.Cost,
// core/policy/cedar's nil cedarPolicyAPI, the fs.Prompter that denied
// every kenaz__write_file call, and HV-03's registry.Options.Policy in
// cmd/harness-vm.
//
// THREE MISSIONS SPECCED THIS GATE AND NONE BUILT IT
// ----------------------------------------------------
// model-scheduled-jobs-01PMSJ01 (§7 G-1, "the nil-dispatcher class") and
// model-settings-reach-the-model-01PMZ101 (§7 G-2, "nil-optional-dep")
// independently designed the SAME new gate: trigger on a doc-comment
// phrase ("nil is allowed" / "nil causes" / "nil disables"), no
// directory scope restriction beyond "a Config/Options-shaped struct".
// fleet-enforcement-truth-01PMZ505 (§7 G-1) proposed a DIFFERENT
// mechanism instead: widen check-cedar-gate-arguments.sh's existing
// clause 3 from the single type `cedar.Gate` to "any interface-typed
// field on a Config/Options struct under core/rpc/views/ or
// core/fleet/", triggered by TYPE + LOCATION, not by doc comment.
//
// Those two designs disagree on the trigger mechanism (author-opted-in
// doc phrase vs. type-and-location-derived) and on whether this is a new
// gate or an extension of I13. That disagreement — not merely scheduling
// — is a plausible reason all three missions each deferred to "whoever
// lands first" and nobody landed. This tool takes the doc-phrase trigger
// (SJ01/Z101's framing): it is authored opt-in, so it does not require
// guessing which of the hundreds of interface-typed Config/Options
// fields in this codebase are "supposed" to be optional collaborators
// vs. required ones that happen to be interfaces (loggers, stores,
// etc). Precision over recall, per this mission's brief. Z505's
// widen-I13 approach remains available as future work if the doc-phrase
// trigger's recall proves too narrow — the two are not mutually
// exclusive; see the gate's own script header.
//
// WHY NOT A SHELL/GREP GATE
// --------------------------
// "Is this field's type an interface" and "is this field ever assigned
// a non-nil value at ANY composite-literal or Set*/With* call site
// across core/+cmd/" are both type-checker questions with no reliable
// textual proxy — the same reason check-seam-implementers.sh
// (scripts/ci/cmd/checkseams) is a Go tool and not a grep. This shares
// its packages.Load pattern.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// triggerPhrase matches the doc-comment idiom this codebase already uses
// to mark an interface field as an intentionally optional collaborator.
// Case-insensitive: both "nil disables" and "Nil disables" appear in
// production doc comments (sentence-initial after a period).
var triggerPhrase = regexp.MustCompile(`(?i)nil is allowed|nil causes|nil disables`)

// candidatePkgPrefixes bound both field-discovery and assignment-search
// to this module's own source, for the same reason checkseams bounds
// its implementer search: packages.Load's NeedDeps pulls in the entire
// import graph, stdlib included, and an unbounded scan would search
// (and could false-positive-clear against) code this repo does not own.
const modulePrefix = "github.com/kameas-ai/kenaz-harness/"

// scanPatterns is deliberately core/... AND cmd/... — not core/ alone.
// docs/unwired-ledger.md's 2026-08-20 entry (vm-execution-surface-truth-
// 01PMZD14 WP03) is explicit that HV-03, the sixth confirmed instance of
// this exact class, lived in cmd/harness-vm/agentexec.go and that a gate
// scoped to core/ only would have missed it. check-cedar-engine-
// singleton.sh was widened for the same reason; this gate ships that
// lesson from the start instead of needing its own follow-up widening.
var scanPatterns = []string{"./core/...", "./cmd/..."}

const allowlistPath = "scripts/ci/allowlists/i18-nil-optional-deps.txt"

// overlayEnvVar names the environment variable gates_can_fail_test.go's
// planted-violation proof uses to redirect one source file's content at
// LOAD time, without ever writing to the real path — the same overlay
// technique check-structured-output-row-parity.sh's WP09_G3_OVERLAY
// uses for `go test -overlay=`, adapted for a tool that calls
// packages.Load directly rather than shelling out to `go build`/`go
// test`. See gates_can_fail_test.go's TestNilOptionalDepsGate_* for why
// a bare os.WriteFile + defer restore is unsafe under `-timeout`
// (PR #323's review finding, reproduced against
// core/llm/gemini/wire.go): a hard kill skips the defer and leaves a
// tracked file mutated on disk. packages.Config.Overlay never touches
// the real file at all — the real path is never opened for writing at
// any point.
const overlayEnvVar = "NIL_OPTIONAL_DEPS_OVERLAY"

// loadOverlay reads a go-build-overlay-shaped JSON file
// (`{"Replace": {"<real-abs-path>": "<scratch-abs-path>"}}`) named by
// overlayEnvVar and returns it as the in-memory map
// packages.Config.Overlay expects (real path -> replacement content).
// Returns (nil, nil) when the env var is unset — the ordinary,
// non-test invocation path.
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
		fmt.Fprintln(os.Stderr, "checknilopts:", err)
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

	fields := findTriggerFields(pkgs)
	if len(fields) == 0 {
		return fmt.Errorf("found zero interface fields with an optional-by-doc-comment trigger phrase "+
			"(%q) anywhere under core/ or cmd/ — this is almost certainly a bug in checknilopts "+
			"(the idiom is known to exist, e.g. core/rpc/views/scheduledchat/impl.go's Dispatcher "+
			"field), not a clean tree", triggerPhrase.String())
	}

	markAssignments(pkgs, fields)

	var violations []string
	for _, f := range fields {
		if f.assigned {
			continue
		}
		violations = append(violations, f.violationString())
	}
	sort.Strings(violations)

	allow, err := loadAllowlist(allowlistPath)
	if err != nil {
		return err
	}

	unlisted := diff(violations, allow)
	stale := diff(allow, violations)

	fmt.Printf("[nil-optional-deps] scanned %d optional-by-doc-comment interface field(s) under core/+cmd/: "+
		"%d wired in production, %d not wired (%d allowlisted, %d unlisted).\n",
		len(fields), len(fields)-len(violations), len(violations), len(violations)-len(unlisted), len(unlisted))

	fail := false
	if len(unlisted) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] FAIL: interface field documented optional, never assigned in production, not in "+allowlistPath+":")
		for _, v := range unlisted {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] Either wire a real production assignment (a composite-literal")
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] field or a Set*/With* call reachable from main/rpc.New), or add a")
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] DATED line to "+allowlistPath+" naming the blocker and owner.")
		fail = true
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] FAIL: STALE entries in "+allowlistPath+" — no longer a violation:")
		for _, v := range stale {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr, "[nil-optional-deps] Delete the line(s) — allowlists shrink monotonically.")
		fail = true
	}

	if fail {
		os.Exit(2)
	}

	fmt.Println("[nil-optional-deps] clean.")
	return nil
}

// triggerField is a single struct field found under core/+cmd/ whose
// type is a non-empty interface and whose doc/trailing comment matches
// triggerPhrase.
type triggerField struct {
	owner     *types.Named // the enclosing struct's named type
	fieldName string
	fieldType string // display string, for the violation message only
	file      string // relative to repo root
	line      int
	assigned  bool
}

func (f *triggerField) violationString() string {
	return fmt.Sprintf("%s:%d: %s.%s (%s) is documented optional but no production site under core/ or cmd/ assigns it a non-nil value",
		f.file, f.line, f.owner.Obj().Name(), f.fieldName, f.fieldType)
}

// isTestDoublePackage mirrors checkseams: a package that imports
// "testing" from a non-_test.go file is a fixture package wearing
// production clothes (core/agentgraph/internal/recorders is the known
// instance), and neither its field declarations nor its call sites
// count as production.
func isTestDoublePackage(p *packages.Package) bool {
	if strings.Contains(p.PkgPath, "/internal/recorders") {
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

func inScopePkg(p *packages.Package) bool {
	return strings.HasPrefix(p.PkgPath, modulePrefix) && !isTestDoublePackage(p)
}

// findTriggerFields walks every non-test file in every in-scope package
// looking for `type X struct { ... }` blocks and, within them, fields
// whose type is a non-empty interface and whose doc comment (leading
// block or trailing line) matches triggerPhrase.
func findTriggerFields(pkgs []*packages.Package) []*triggerField {
	var out []*triggerField
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inScopePkg(p) {
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
				if !ok {
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
				for _, field := range st.Fields.List {
					if len(field.Names) == 0 {
						continue // embedded field — not this class
					}
					doc := ""
					if field.Doc != nil {
						doc += field.Doc.Text()
					}
					if field.Comment != nil {
						doc += " " + field.Comment.Text()
					}
					if !triggerPhrase.MatchString(doc) {
						continue
					}
					for _, name := range field.Names {
						fobj := p.TypesInfo.Defs[name]
						if fobj == nil {
							continue
						}
						ft := fobj.Type()
						iface, ok := ft.Underlying().(*types.Interface)
						if !ok || iface.NumMethods() == 0 {
							// Not an interface, or the empty interface
							// (any/interface{} — a generic payload slot,
							// not a "collaborator" in this class's sense).
							continue
						}
						relFile := relToRepoRoot(pos.Filename)
						out = append(out, &triggerField{
							owner:     named,
							fieldName: name.Name,
							fieldType: types.TypeString(ft, types.RelativeTo(p.Types)),
							file:      relFile,
							line:      fset.Position(name.Pos()).Line,
						})
					}
				}
				return true
			})
		}
	})
	return out
}

// markAssignments walks every non-test file in every in-scope package
// looking for three production-wiring shapes and marks the matching
// triggerField as assigned when found:
//
//  1. A composite literal of the field's owner type with a
//     `FieldName: <non-nil expr>` element (covers `T{...}` and `&T{...}`
//     — go/types records the composite literal's own type as the
//     struct, not the pointer, so no unwrapping is needed).
//
//  2. A call `x.SetFieldName(...)` or `x.WithFieldName(...)` where x's
//     type (pointer-stripped) is the field's owner type. Best-effort:
//     the argument's own value is not inspected (mirrors I13 clause 4's
//     documented "called at all" leniency) — a setter called with a nil
//     argument would be a false negative here, same limitation I13
//     accepts for its With* clause.
//
//  3. A plain assignment statement `x.FieldName = <non-nil expr>` where
//     x's type (pointer-stripped) is the field's owner type. This is
//     the functional-options idiom this codebase actually uses for most
//     of these fields — e.g. `func WithSessionHookRunner(h
//     SessionHookRunner) ManagerOption { return func(m *Manager) { if h
//     != nil { m.hooks = h } } }` — where the mutation is a bare `=`
//     inside a closure, not a Set*/With*-named method call. Calibration
//     against this tree (2026-09-10) found FOUR real production wiring
//     sites of exactly this shape that clauses 1-2 alone missed
//     (session.Manager.hooks, sessionsview managerAPI.{resumeStarter,
//     titleGen,cedarGate}) and would have produced false-positive
//     findings without it.
//
//     Deliberately NOT distinguishing "RHS is a bare local/parameter"
//     from "RHS is itself another struct's field" (e.g. `env.Corpus =
//     d.Corpus`, agentgraph/env_deps.go's applyTo): that two-layer
//     EnvDeps->Env forwarding idiom is the primary production wiring
//     path for several of these exact fields (verified by hand against
//     core/rpc/api.go:7546 `deps.Corpus =
//     graphview.NewCorpusBackendAdapter(corpusMgr)` feeding
//     env_deps.go's `if d.Corpus != nil { env.Corpus = d.Corpus }`).
//     Excluding selector-expression RHS to be "more precise" would have
//     made that legitimate, working wiring path invisible to this gate
//     and produced a false positive on Env.Corpus. The accepted
//     trade-off, same class of limitation I13 documents for its own
//     clause 2/4: this is single-hop. If EnvDeps.Corpus were itself
//     never assigned anywhere, this gate would not catch that — only
//     Env.Corpus (or whichever field carries EnvDeps.Corpus onward)
//     carrying its own "nil disables" doc comment makes a broken chain
//     visible, and today it does.
func markAssignments(pkgs []*packages.Package, fields []*triggerField) {
	// Index fields by owner type identity + name for O(1) lookup during
	// the walk. types.Named objects are shared across one packages.Load
	// call, so comparing *types.TypeName pointers (Obj()) is sound.
	type key struct {
		owner *types.TypeName
		field string
	}
	index := make(map[key]*triggerField, len(fields))
	for _, f := range fields {
		index[key{f.owner.Obj(), f.fieldName}] = f
	}

	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !inScopePkg(p) {
			return
		}
		for _, file := range p.Syntax {
			pos := p.Fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.CompositeLit:
					t := p.TypesInfo.TypeOf(node)
					named, ok := t.(*types.Named)
					if !ok {
						return true
					}
					for _, elt := range node.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						keyIdent, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						tf, ok := index[key{named.Obj(), keyIdent.Name}]
						if !ok {
							continue
						}
						if isBareNil(kv.Value) {
							continue
						}
						tf.assigned = true
					}
				case *ast.CallExpr:
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					name := sel.Sel.Name
					var fieldName string
					switch {
					case strings.HasPrefix(name, "Set") && len(name) > 3:
						fieldName = name[3:]
					case strings.HasPrefix(name, "With") && len(name) > 4:
						fieldName = name[4:]
					default:
						return true
					}
					recvType := p.TypesInfo.TypeOf(sel.X)
					if recvType == nil {
						return true
					}
					if ptr, ok := recvType.(*types.Pointer); ok {
						recvType = ptr.Elem()
					}
					named, ok := recvType.(*types.Named)
					if !ok {
						return true
					}
					if tf, ok := index[key{named.Obj(), fieldName}]; ok {
						tf.assigned = true
					}
				case *ast.AssignStmt:
					if node.Tok != token.ASSIGN || len(node.Lhs) != len(node.Rhs) {
						return true
					}
					for i, lhs := range node.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						recvType := p.TypesInfo.TypeOf(sel.X)
						if recvType == nil {
							continue
						}
						if ptr, ok := recvType.(*types.Pointer); ok {
							recvType = ptr.Elem()
						}
						named, ok := recvType.(*types.Named)
						if !ok {
							continue
						}
						tf, ok := index[key{named.Obj(), sel.Sel.Name}]
						if !ok {
							continue
						}
						if isBareNil(node.Rhs[i]) {
							continue
						}
						tf.assigned = true
					}
				}
				return true
			})
		}
	})
}

func isBareNil(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
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
