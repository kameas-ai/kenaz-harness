// Command checkseams is the Go half of scripts/ci/check-seam-implementers.sh
// (wiring-integrity-01PMAG04 WP06, spec §3.2 item 4; widened by
// automation-actually-runs-01PMZ404 UNIT-17, G-1a).
//
// It answers a question grep cannot answer reliably: "does any
// non-test type in this module implement interface X?" Go has no
// explicit `implements` declaration — satisfaction is structural — so
// the only sound way to check it is the type checker, not string
// matching. golang.org/x/tools/go/packages (already a module
// dependency via core/secrets/lint) gives us that for free.
//
// Two independent sources feed the same implementer check:
//
//  1. Every interface declared in core/agentgraph/seams.go (the
//     original, hand-curated input set).
//  2. G-1a (DERIVED, UNIT-17): every exported interface declared under
//     core/ that is the type of an exported field on an exported
//     struct whose name ends in "Config", "Options" or "Deps" —
//     automation-actually-runs-01PMZ404's finding was that
//     ArtifactsReadWriter, ToolCaller, NetworkAuthorizer, AuditEmitter,
//     slashcmd.ToolDispatcher, catalog.RecipeRegistry and
//     wfsched.Dispatcher all sat unimplemented on corewf.Deps /
//     similar structs for a release cycle with nothing watching them,
//     because #1's input set was scoped to one file in one package.
//     This source is DERIVED from the struct declarations themselves,
//     not an allowlist — a brand-new *Config/*Options/*Deps struct
//     anywhere under core/ is in scope automatically, with no edit to
//     this file.
//
// An interface with zero implementers (by value or by pointer — most
// seam implementers here use pointer receivers) is reported as a
// violation UNLESS its own declaration carries a //wiring:deferred
// (<reason>) directive on the line immediately above its `type X
// interface {` line (the same directive scripts/ci/check-output-
// ports.sh consults — see docs/wiring-audit.md).
package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// wiringDeferredDirective mirrors core/agentgraph's
// wiring_directive_test.go — POSIX classes (not \s: BSD grep on macOS
// dev boxes doesn't support \s in -E mode either, and we want this
// tool's notion of the grammar to visibly match the shell guards' even
// though Go regexp doesn't share that BSD-grep constraint) and a greedy
// reason group so a reason containing its own parentheses still
// matches through to the line's final closing paren.
var wiringDeferredDirective = regexp.MustCompile(`^[[:space:]]*//[[:space:]]*wiring:deferred\((.+)\)[[:space:]]*$`)

const seamsFileSuffix = "core/agentgraph/seams.go"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "checkseams:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedFiles | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, "./core/...")
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

	// Find core/agentgraph and pull every interface type declared in
	// seams.go specifically (spec/tasks.md scope this guard to that one
	// file — other interfaces elsewhere in the package, e.g.
	// PromptTemplateSource in prompt_render.go or CatalogProvider in
	// validator_manifest.go, are out of scope for THIS guard).
	var agentgraphPkg *packages.Package
	for _, p := range pkgs {
		if p.PkgPath == "github.com/kameas-ai/kenaz-harness/core/agentgraph" {
			agentgraphPkg = p
			break
		}
	}
	if agentgraphPkg == nil {
		return fmt.Errorf("core/agentgraph package not found among loaded packages")
	}

	seamInterfaces, err := seamsInterfaces(agentgraphPkg)
	if err != nil {
		return err
	}
	if len(seamInterfaces) == 0 {
		return fmt.Errorf("found zero interfaces in seams.go — check the file path match, this is almost certainly a bug in checkseams, not an empty seams.go")
	}

	// G-1a (UNIT-17): derive a second input set from every exported
	// *Config/*Options/*Deps struct field under core/, and merge it
	// with the hand-curated seams.go set. Dedup by the interface's
	// fully-qualified type (package path + name) so an interface that
	// happens to satisfy both sources (declared in seams.go AND used on
	// a *Deps struct) isn't reported twice.
	decls := allInterfaceDecls(pkgs)
	derived := derivedInterfaces(pkgs, decls)
	seen := make(map[string]bool, len(seamInterfaces))
	for _, si := range seamInterfaces {
		seen[si.qualifiedName] = true
	}
	allInterfaces := seamInterfaces
	for _, di := range derived {
		if seen[di.qualifiedName] {
			continue
		}
		seen[di.qualifiedName] = true
		allInterfaces = append(allInterfaces, di)
	}

	// Collect every named, non-test type across every loaded package
	// (structs mostly, but any named type with a method set counts).
	candidates := collectCandidateTypes(pkgs)

	var violations []string
	for _, si := range allInterfaces {
		implemented := false
		for _, c := range candidates {
			if types.Implements(c, si.iface) || types.Implements(types.NewPointer(c), si.iface) {
				implemented = true
				break
			}
		}
		if implemented {
			continue
		}
		if si.deferred {
			fmt.Printf("  %-24s no implementer, but wiring:deferred(%s)\n", si.name, si.deferredReason)
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s: no non-test type in ./core/... implements this interface, and it carries no //wiring:deferred directive (%s:%d)",
			si.name, si.file, si.line))
	}

	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "checkseams: FAIL — unimplemented seam(s):")
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Fix: wire a real implementer, or add a //wiring:deferred(<reason>) comment")
		fmt.Fprintln(os.Stderr, "on the line immediately above the interface's `type X interface {` declaration.")
		os.Exit(2)
	}

	fmt.Printf("checkseams: clean — %d seam interfaces (%d from seams.go, %d derived from *Config/*Options/*Deps fields) each have an implementer or a wiring:deferred directive.\n",
		len(allInterfaces), len(seamInterfaces), len(allInterfaces)-len(seamInterfaces))
	return nil
}

type seamInterface struct {
	name           string // display name, e.g. "ToolCaller" or "workflows.ToolCaller"
	qualifiedName  string // dedup key: full package path + type name
	iface          *types.Interface
	file           string
	line           int
	deferred       bool
	deferredReason string
}

// seamsInterfaces walks agentgraphPkg's syntax trees, keeping only the
// file whose path ends in seams.go, and returns every interface type
// declared there plus whether the line immediately above its
// declaration carries a wiring:deferred directive.
func seamsInterfaces(pkg *packages.Package) ([]seamInterface, error) {
	var out []seamInterface
	fset := pkg.Fset
	for _, file := range pkg.Syntax {
		pos := fset.Position(file.Pos())
		if !strings.HasSuffix(filepathToSlash(pos.Filename), seamsFileSuffix) {
			continue
		}
		lines := fileLines(pos.Filename)
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			ifaceType, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				return true
			}
			obj := pkg.TypesInfo.Defs[ts.Name]
			if obj == nil {
				return true
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				return true
			}
			iface, ok := named.Underlying().(*types.Interface)
			if !ok {
				return true
			}
			_ = ifaceType
			declLine := fset.Position(ts.Pos()).Line
			deferred, reason := checkDirectiveAbove(lines, declLine)
			out = append(out, seamInterface{
				name:           ts.Name.Name,
				qualifiedName:  pkg.PkgPath + "." + ts.Name.Name,
				iface:          iface,
				file:           seamsFileSuffix,
				line:           declLine,
				deferred:       deferred,
				deferredReason: reason,
			})
			return true
		})
	}
	return out, nil
}

// ifaceDecl records where an exported interface type is declared and
// whether its declaration carries a wiring:deferred directive —
// looked up once for the whole module (allInterfaceDecls) and reused
// by derivedInterfaces so a *Config/*Options/*Deps field naming an
// interface declared elsewhere in core/ inherits that interface's own
// deferral, not a fabricated one.
type ifaceDecl struct {
	qualifiedName  string
	displayName    string
	file           string
	line           int
	deferred       bool
	deferredReason string
}

// hasConfigOptionsDepsSuffix reports whether name ends in one of the
// three struct-name suffixes G-1a scopes to (spec §8: "exported struct
// named *Config / *Options / *Deps").
func hasConfigOptionsDepsSuffix(name string) bool {
	for _, suffix := range []string{"Config", "Options", "Deps"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// allInterfaceDecls walks every non-test file under core/ and records
// every exported interface type declaration — its file/line and
// whether the line immediately above carries a wiring:deferred
// directive — keyed by fully-qualified name. This is a superset of
// seams.go's interfaces; derivedInterfaces below intersects it against
// what *Config/*Options/*Deps structs actually reference.
func allInterfaceDecls(pkgs []*packages.Package) map[string]ifaceDecl {
	out := map[string]ifaceDecl{}
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !strings.HasPrefix(p.PkgPath, candidatePkgPrefix) && p.PkgPath != candidatePkgExact {
			return
		}
		fset := p.Fset
		for _, file := range p.Syntax {
			pos := fset.Position(file.Pos())
			if strings.HasSuffix(pos.Filename, "_test.go") {
				continue
			}
			lines := fileLines(pos.Filename)
			ast.Inspect(file, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				if !ts.Name.IsExported() {
					return true
				}
				if _, ok := ts.Type.(*ast.InterfaceType); !ok {
					return true
				}
				obj := p.TypesInfo.Defs[ts.Name]
				if obj == nil {
					return true
				}
				qname := p.PkgPath + "." + ts.Name.Name
				declLine := fset.Position(ts.Pos()).Line
				deferred, reason := checkDirectiveAbove(lines, declLine)
				out[qname] = ifaceDecl{
					qualifiedName:  qname,
					displayName:    p.Types.Name() + "." + ts.Name.Name,
					file:           filepathToSlash(pos.Filename),
					line:           declLine,
					deferred:       deferred,
					deferredReason: reason,
				}
				return true
			})
		}
	})
	return out
}

// derivedInterfaces implements G-1a: every exported interface under
// core/ that is the declared type of an exported field on an exported
// struct whose name ends in Config/Options/Deps. decls supplies each
// candidate interface's own declaration site and deferral status
// (looked up by qualified name), so this function only needs to find
// the FIELD REFERENCES, not re-derive declaration metadata.
func derivedInterfaces(pkgs []*packages.Package, decls map[string]ifaceDecl) []seamInterface {
	var out []seamInterface
	seenQName := map[string]bool{}
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !strings.HasPrefix(p.PkgPath, candidatePkgPrefix) && p.PkgPath != candidatePkgExact {
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
				if !ts.Name.IsExported() || !hasConfigOptionsDepsSuffix(ts.Name.Name) {
					return true
				}
				structType, ok := ts.Type.(*ast.StructType)
				if !ok || structType.Fields == nil {
					return true
				}
				for _, field := range structType.Fields.List {
					if len(field.Names) == 0 {
						continue // embedded field — out of scope for this pass
					}
					fieldExported := false
					for _, fn := range field.Names {
						if fn.IsExported() {
							fieldExported = true
						}
					}
					if !fieldExported {
						continue
					}
					fieldType := p.TypesInfo.TypeOf(field.Type)
					if fieldType == nil {
						continue
					}
					named, ok := fieldType.(*types.Named)
					if !ok {
						continue
					}
					if _, isIface := named.Underlying().(*types.Interface); !isIface {
						continue
					}
					obj := named.Obj()
					if obj == nil || obj.Pkg() == nil || !obj.Exported() {
						continue
					}
					qname := obj.Pkg().Path() + "." + obj.Name()
					decl, known := decls[qname]
					if !known {
						// Declared outside core/ (or somehow missed by
						// allInterfaceDecls) — G-1a scopes to interfaces
						// under core/ only.
						continue
					}
					if seenQName[qname] {
						continue
					}
					seenQName[qname] = true
					iface, _ := named.Underlying().(*types.Interface)
					out = append(out, seamInterface{
						name:           decl.displayName,
						qualifiedName:  qname,
						iface:          iface,
						file:           decl.file,
						line:           decl.line,
						deferred:       decl.deferred,
						deferredReason: decl.deferredReason,
					})
				}
				return true
			})
		}
	})
	return out
}

// checkDirectiveAbove looks at the source line immediately preceding
// declLine (1-indexed) for a wiring:deferred directive. Type
// declarations in this file are sometimes preceded by a multi-line doc
// comment block; the directive convention (docs/wiring-audit.md) is a
// single directive line with no blank line before the declaration, so
// we only need to check exactly one line up.
func checkDirectiveAbove(lines []string, declLine int) (bool, string) {
	idx := declLine - 2 // declLine is 1-indexed; line above is declLine-1, 0-indexed idx = declLine-2.
	if idx < 0 || idx >= len(lines) {
		return false, ""
	}
	m := wiringDeferredDirective.FindStringSubmatch(lines[idx])
	if m == nil {
		return false, ""
	}
	return true, strings.TrimSpace(m[1])
}

// candidatePkgPrefix + candidatePkgExact bound candidate collection to the
// module this guard's own failure message claims to check ("no non-test
// type in ./core/... implements this interface", main.go:115/main.go
// wantErr string). Fixes Vacuity B (spec entry-points-and-crash-reporting-
// 01PMZD13 §1.4.1): packages.Load's Mode includes NeedDeps alongside
// NeedSyntax, so packages.Visit's whole-import-graph walk populates Syntax
// for every dependency too — stdlib included. Instrumented and RUN before
// this fix landed: 10,032 candidate named types across 910 distinct
// packages, of which 673 packages sit outside github.com/kameas-ai/
// kenaz-harness/core/ entirely. For the seams this guard checks (Reader,
// Writer, PolicyGate — one and two-method interfaces), that width is not a
// marginal widening, it is the difference between a gate and a tautology:
// almost any small interface is structurally satisfied by SOMETHING in the
// standard library or a third-party dependency.
const (
	candidatePkgPrefix = "github.com/kameas-ai/kenaz-harness/core/"
	// The bare root package (core/core.go) — "./core/..." in Go's own
	// package-pattern semantics includes package "core" itself, not only
	// its subdirectories, so the prefix check above alone would wrongly
	// exclude it.
	candidatePkgExact = "github.com/kameas-ai/kenaz-harness/core"
)

// isTestDoublePackage reports whether a package is production code in name
// only — Vacuity A (spec §1.4.1). core/agentgraph/internal/recorders/ is
// nine non-_test.go files, one per seam, each importing "testing" in
// production position (compactor.go:6) and imported by nothing but
// _test.go files elsewhere. collectCandidateTypes previously only
// excluded _test.go FILES, not test-double PACKAGES, so recorders' fakes
// counted as real implementers of the seams they exist to fake.
//
// The durable rule is "a production package has no business importing
// testing", checked by scanning every non-_test.go file's imports — not a
// path allowlist, so it generalises to the next recorders-shaped package
// without an edit. Belt and braces: also exclude .../internal/recorders
// by path explicitly, since the import-scan rule is the one this comment
// calls load-bearing and a path check is cheap insurance if a future
// recorders-shaped package ever stops importing "testing" directly (e.g.
// via a re-exported helper).
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

// collectCandidateTypes returns every named type declared in a non-
// _test.go file, in a non-test-double package, under
// github.com/kameas-ai/kenaz-harness/core/. types.Implements is checked
// against each (and its pointer) at the call site.
func collectCandidateTypes(pkgs []*packages.Package) []*types.Named {
	var out []*types.Named
	seen := map[*types.Named]bool{}
	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if !strings.HasPrefix(p.PkgPath, candidatePkgPrefix) && p.PkgPath != candidatePkgExact {
			return
		}
		if isTestDoublePackage(p) {
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
				obj := p.TypesInfo.Defs[ts.Name]
				if obj == nil {
					return true
				}
				named, ok := obj.Type().(*types.Named)
				if !ok {
					return true
				}
				// Skip interface types themselves as candidates — we're
				// looking for concrete implementers, and one interface
				// structurally satisfying another (embedding) isn't the
				// "real implementer" signal this guard wants.
				if _, isIface := named.Underlying().(*types.Interface); isIface {
					return true
				}
				if !seen[named] {
					seen[named] = true
					out = append(out, named)
				}
				return true
			})
		}
	})
	return out
}

func fileLines(path string) []string {
	data, err := os.ReadFile(path) //nolint:gosec // fixed path from go/packages Fset, not user input
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// filepathToSlash normalises a possibly-backslash path (won't happen on
// the Linux CI runner this targets, but keeps the suffix check honest
// if ever run on Windows) to forward slashes.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
