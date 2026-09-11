// Command checknilopts is the Go half of
// scripts/ci/check-nil-optional-deps.sh — the nil-optional-dependency
// gate.
//
// THE DEFECT CLASS
// -----------------
// An interface-typed field on a struct, documented by its own author as
// optional ("nil is allowed" / "nil causes …" / "nil disables …" / "nil
// falls back …" / "when nil" / "if nil"), that no production wiring site
// ever assigns. The type system says the capability exists; the doc
// comment even says what "off" looks like; nothing ever turns it on.
// Six confirmed instances shipped as real defects before this gate
// existed (docs/unwired-ledger.md, 2026-08-20 entry): ChatRunDispatcher,
// wfsched.Dispatcher, registry.Options.Cost, core/policy/cedar's nil
// cedarPolicyAPI, the fs.Prompter that denied every kenaz__write_file
// call, and HV-03's registry.Options.Policy in cmd/harness-vm.
//
// PR #332's review round found that the gate's ORIGINAL 3-phrase trigger
// ("nil is allowed" / "nil causes" / "nil disables") could only see ONE
// of those six (scheduledchat.Config.Dispatcher, via "nil causes") — the
// gate that exists BECAUSE of these six P0s was blind to 5 of them, a
// self-refuting state for a PR whose header cited them as motivating
// history. Fixed same-commit: the trigger widened to also match "nil
// falls back", "when nil", "if nil" (below), which alone closes the gap
// for fs.GateOptions.Prompter ("Defaults to NoOpPrompter when nil") and
// core/rpc/api.go's cedarPolicyAPI ("nil falls back to the
// cedarpolicy.NewAPI(nil) graceful-empty surface") — both already
// correctly wired in production, now visible AND reported clean instead
// of invisible. registry.Options.Cost and .Policy needed a different
// fix: neither carried ANY doc comment (see "WHAT THIS GATE CANNOT SEE"
// below) — true doc comments were added to both, verified against
// registry.go's New() and audited_stream.go's cost-derivation switch
// before writing, closing the last 2 of the six. HV-03's
// registry.Options.Policy in cmd/harness-vm is the same registry.Options
// type; the same fix covers it.
//
// PR #332's SECOND review round (same date, 2026-09-10) found the first
// round's own Set*/With* fix was incomplete: clause 3 of markAssignments
// (the plain-assignment scan, below) still marked a field "wired" the
// instant its setter's OWN BODY assigned it from the setter's own
// parameter — `func (e *Engine) SetDispatcher(d ChatRunDispatcher) {
// e.dispatch = d }` — regardless of whether anything ever CALLED
// SetDispatcher. The reviewer proved this with a planted field whose
// setter has zero call sites and was still reported wired. Fixed
// same-commit: clause 3 now excludes an assignment when the enclosing
// function is a method on the field's own owner type and the RHS is
// that method's own parameter (see markAssignments' doc comment for the
// exact exclusion and its residual limitation). Re-running the gate
// after the fix surfaced two false "unwired" fields
// (core/fleet/context_graph_sync.go's ContextGraphSyncer.auditEmitter,
// core/rpc/views/agentgraph/chat/llm_provider_adapter.go's
// LLMProviderAdapter.attachments) caused by an INDEPENDENT, pre-existing
// gap the clause-3 bug had been masking: clause 2's Set*/With*-call
// detection derives the field name by stripping the method's own prefix
// (WithAuditEmitter -> "AuditEmitter"), which never matched either
// field's actual unexported camelCase name ("auditEmitter",
// "attachments") — both are genuinely wired at real call sites
// (core/rpc/api.go:3420, chat_runner.go:995) that clause 2 simply could
// not see under the old name-derivation. Fixed same-commit: clause 2
// also tries the lowercase-first-letter form of the derived name. Net
// result: scanned/wired/not-wired/allowlisted/unlisted counts are
// UNCHANGED from before this second round (60/53/7/7/0) — the fix closes
// a real detection hole without changing today's verdicts, because no
// field in this tree currently depends on an uncalled setter to look
// wired. See gates_can_fail_test.go's
// TestNilOptionalDepsGate_PlantedUnwiredFieldFires/setter-defined-but-
// never-called-still-fires for the committed planted-violation proof
// (reproduces core/scheduler/chat_cron_engine.go:150's SetDispatcher
// shape) and its companion .../setter-called-with-real-value-still-wires
// for the true-negative half.
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
// WHAT THIS GATE CANNOT SEE
// ---------------------------
// Two structural blind spots, both discovered against live code (not
// hypothetical) during the PR #332 review round and its follow-up
// calibration:
//
//  1. A field with NO doc comment at all is invisible to a doc-comment
//     trigger BY CONSTRUCTION — there is no text to match against.
//     registry.Options.Cost and .Policy shipped with zero doc comment
//     right up until this commit (see above); they were real,
//     genuinely-wired production fields the gate could not have
//     verified either way, because it never knew they existed. Adding a
//     true doc comment (only where verified true — see the field itself
//     for what "true" meant here) closes individual cases one at a
//     time, but does not close the CLASS: any future interface field
//     that is optional-by-doc-comment convention but whose author
//     forgets the comment is invisible to this gate the same way. This
//     is exactly the gap Z505 §7 G-1's type+location design would have
//     closed — "any interface-typed field on a Config/Options struct"
//     doesn't need a comment to be found, because it doesn't trigger on
//     comments at all. This gate deliberately does not implement that
//     design (see "THREE MISSIONS" above for the precision-over-recall
//     reasoning); the trade-off is real and this paragraph is its
//     receipt, not a hedge.
//
//  2. scanPatterns is `./core/...` and `./cmd/...` — the module root
//     (main.go) matches NEITHER pattern, and even a hypothetical `.`
//     pattern would fail modulePrefix's own filter (main.go's package
//     path has no trailing "/", modulePrefix requires one). Both field
//     discovery and assignment search are blind to main.go. Found
//     2026-09-10 chasing down core/update/bootswap.Config.Relauncher:
//     its one real production call site is main.go's
//     MaybeSwapAndRelaunch call, which the gate cannot see either way —
//     the finding happened to be correct only because main.go's own
//     adjacent comment independently confirms the same nil-by-design
//     fact the gate inferred from bare absence. A field whose ONLY
//     production wiring lived in main.go would be misreported as
//     unwired by this gate, with no way to tell the two cases apart
//     from the gate's own output. Not fixed in this commit — scanPatterns
//     could add "." to cover it, but main.go is a single ~200-line file
//     with a handful of composite literals in it; the fix is cheap
//     enough that it should happen alongside the next real finding that
//     needs it, not speculatively here.
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

// triggerPhrase matches the doc-comment idioms this codebase already
// uses to mark an interface field as an intentionally optional
// collaborator. Case-insensitive: both "nil disables" and "Nil
// disables" appear in production doc comments (sentence-initial after a
// period).
//
// Widened in the PR #332 review round from the original 3 phrases ("nil
// is allowed" / "nil causes" / "nil disables") after the reviewer showed
// those 3 alone missed 5 of the 6 P0s cited as this gate's own
// motivating history (see the package doc comment above). "nil falls
// back" / "when nil" / "if nil" are the natural-prose variants actually
// found in this tree by grepping every interface-typed struct field's
// doc comment for the word "nil" and reading each one (not guessed):
// calibration went from 33 scanned fields to 60, all newly-visible
// fields still gated on the SAME downstream check (must be a non-empty
// interface type), so the false-positive risk from widening a doc-text
// match is bounded by that type filter — see
// scripts/ci/allowlists/i18-nil-optional-deps.txt's header for the 6
// genuinely-unwired fields this widening surfaced and how each was
// dispositioned. Candidate phrases considered and REJECTED, run and
// measured (not guessed): a bare "optional" was tried and reverted —
// 33→92 scanned fields (vs. 33→60 for the phrase set actually shipped),
// producing 4 additional unlisted violations on top of the 6 this
// commit already dispositions. Spot-checking two of them
// (core/hooks/runner.go:231's Config.MCP, "optional — nil means kind=mcp
// hooks are skipped with a warning"; chat_runner.go:481's
// Config.SecretAuditEmitter, "SecretAuditEmitter optionally receives …
// nil is a no-op") shows "optional" pulls in a broader, adjacent idiom
// — "nil silently skips/no-ops this specific feature" — that is a real
// and arguably related pattern, but expanding scope AND triaging a
// second wave of findings in the same PR that exists to fix a
// mistrusted gate risks re-committing the same "claims more than it
// delivers" failure this PR is closing. Left for deliberate follow-up
// scoping, not silently dropped: this paragraph is that follow-up's
// starting point. "may be nil" / "can be nil" / "left nil" were also
// tried and reverted for the same reason — 33→71 scanned, 2 additional
// unlisted violations beyond this commit's 6 — a real, live-matching
// idiom in this tree, not a hypothetical one, but a second wave of
// findings this PR's scope does not cover triaging.
var triggerPhrase = regexp.MustCompile(`(?i)nil is allowed|nil causes|nil disables|nil falls back|when nil|if nil`)

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
//     type (pointer-stripped) is the field's owner type and the single
//     argument is not a bare `nil` identifier — `x.SetDispatcher(nil)`
//     does NOT count as wiring (PR #332 review nit: this clause used to
//     mark the field assigned on the call alone, without checking the
//     argument, the one detection path that didn't apply isBareNil
//     while the other two already did). A call with zero or multiple
//     arguments is left best-effort assigned=true — same "called at
//     all" leniency I13 accepts for its own With* clause, kept only for
//     the shapes isBareNil cannot unambiguously judge.
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
//
//     EXCEPTION, added in the PR #332 second review round: clause 3 does
//     NOT count an assignment `recv.FieldName = p` when the enclosing
//     function is itself a method on the field's owner type AND `p` is
//     literally that method's own parameter. Before this exception,
//     `func (e *Engine) SetDispatcher(d ChatRunDispatcher) { e.dispatch =
//     d }` was marked "wired" the instant the method was DECLARED,
//     because clause 3 walked every AssignStmt in the tree with no
//     notion of which function it sat inside, and `d` (a parameter, not
//     a literal `nil`) passed isBareNil — regardless of whether
//     SetDispatcher was ever CALLED. The reviewer proved this with a
//     planted field whose setter has zero call sites and is still
//     reported "wired" (see gates_can_fail_test.go's
//     TestNilOptionalDepsGate_PlantedUnwiredFieldFires/setter-defined-
//     but-never-called-still-fires). core/scheduler/chat_cron_engine.go:
//     150's SetDispatcher is exactly this shape; it happens to be
//     genuinely wired today (called with a real value at
//     core/rpc/api.go:3158, still detected via clause 2), but the
//     mechanism generalises to any field with an uncalled setter, which
//     defeats the gate's own purpose. A field excluded here falls
//     through to clauses 1-2, which already verify an actual
//     composite-literal or call-site value — see assignVisitor.Visit's
//     *ast.FuncDecl/*ast.FuncLit cases for how the enclosing-method
//     context is tracked. This is the "acceptable alternative" from the
//     review (skip a full call-graph hop; let the setter's own body not
//     count, and require clause 2's real call-site check to do the
//     work) rather than a second call-graph hop from the setter to ITS
//     callers — the latter was judged disproportionate for a gate this
//     narrowly scoped. Residual limitation: a field wired exclusively
//     through a real call to a helper method that is NOT named Set*/
//     With* (so clause 2 cannot recognise the call) and that assigns the
//     field from its own parameter (so this exception excludes clause 3
//     too) would now be misreported as unwired. No such case exists in
//     this tree today (calibration re-run below); if one appears, the
//     fix is either renaming the method to the Set*/With* idiom clause 2
//     already recognises, or a documented allowlist entry naming this
//     paragraph as the blocker.
//
//     PR #332's FOURTH review round closed a DIFFERENT escape from this
//     same exception: assignVisitor.Visit's *ast.FuncLit case used to
//     reset self to nil for ANY closure, so the exact never-called-
//     setter shape above still counted as "wired" the instant it was
//     moved one syntactic layer down into a nested closure —
//     `func (c *Config) SetX(d T) { helper := func() { c.Field = d };
//     helper() }`, zero call sites for SetX anywhere. Reproduced as
//     "scanned 61: 54 wired ... clean" against the round-2/3 binary
//     (gates_can_fail_test.go's
//     TestNilOptionalDepsGate_PlantedUnwiredFieldFires/nested-closure-
//     setter-still-fires is the committed proof). FIXED: self now
//     threads through *ast.FuncLit boundaries unchanged instead of
//     resetting — see that case's own doc comment for why this leaves
//     the WithSessionHookRunner functional-option idiom (a plain,
//     receiver-less function) untouched, and why the fix generalises to
//     ARBITRARY closure nesting depth inside the setter's own body (Go
//     has no nested func DECLARATIONS, only func LITERALS, so every
//     nesting level is an *ast.FuncLit and every one of them now
//     inherits self the same way).
//
//     A THIRD gap SURVIVES the fourth round's fix — not because the fix
//     missed it, but because it sits entirely outside what the fix
//     touches: a plain, receiver-less top-level function that takes the
//     owner type as an explicit pointer PARAMETER (rather than a
//     receiver) and assigns the field from its own second parameter,
//     called once from inside a setter that itself has zero call sites —
//
//     func (c *Config) SetX(d T) { assignX(c, d) }
//     func assignX(cfg *Config, val T) { cfg.Field = val }
//
//     — reproduces "wired" with zero call sites for SetX anywhere
//     (verified overlay-only against the post-fourth-round binary: a
//     planted ZzGateProbeHelperFn field/setter of exactly this shape
//     still reports "scanned 61: 54 wired ... clean"). The reason: this
//     has nothing to do with *ast.FuncLit threading — assignX is an
//     *ast.FuncDecl, and methodSelfCtx recomputes self from EVERY
//     FuncDecl's OWN receiver regardless of caller context. assignX has
//     no receiver, so self is nil for its entire body BY THE SAME RULE
//     that gives WithSessionHookRunner's plain FuncDecl a nil self — the
//     rule the round-2 exception was built on top of, not one round 4
//     touched. Clause 2 cannot see the call either: `assignX(c, d)` is a
//     free-function call (`*ast.Ident` Fun), not `x.SetFieldName(...)`
//     (`*ast.SelectorExpr` Fun), so clause 2's own match never engages.
//     Net effect: clause 3 has never distinguished "a plain function
//     that's a real, externally-called wiring helper" from "a plain
//     function that exists solely to launder an uncalled setter's own
//     parameter one hop sideways" — both get the same self=nil leniency,
//     because BOTH shapes are, syntactically, indistinguishable at the
//     point of the assignment itself.
//
//     This is not hypothetical scaffolding — it is the SAME mechanism
//     already load-bearing for real fields in this tree today.
//     core/rpc/views/sessions/impl.go's WithResumeStarter,
//     WithTitleGeneratorOpt and WithExportOpts, and
//     core/rpc/views/sessions/autonomy.go's WithAutonomyContext, are all
//     free functions of exactly this shape (`func WithX(api SessionsAPI,
//     ...) SessionsAPI { if m, ok := api.(*managerAPI); ok { m.field =
//     ... } ...}` — a type-asserted parameter standing in for a
//     receiver) whose ONLY detection path is this clause-3 leniency:
//     clause 2 cannot match their free-function call syntax, so
//     managerAPI.resumeStarter/.titleGen/.cedarGate/.autonomyCtx are
//     reported "wired" purely because their WithX function is DECLARED,
//     not because anything verifies WithX is ever CALLED. All four do
//     have real call sites today (core/rpc/api.go:2295, :2386, :2423,
//     :2351 — checked by hand, not assumed), so the current 53-wired
//     verdict is not a false positive; the point is that checknilopts's
//     verdict for these four fields would be UNCHANGED if those four
//     call sites in api.go were deleted tomorrow. Closing this would
//     need a real call-graph hop from a self=nil plain function to ITS
//     OWN callers — the same escalation the round-2 exception's design
//     note (above) already rejected as disproportionate for the
//     receiver-method case, for the identical reason. No allowlist
//     action needed today (all known instances trace to real callers);
//     if a future plain-function helper of this shape does NOT have a
//     real caller, this paragraph names the blocker.
func markAssignments(pkgs []*packages.Package, fields []*triggerField) {
	// Index fields by owner type identity + name for O(1) lookup during
	// the walk. types.Named objects are shared across one packages.Load
	// call, so comparing *types.TypeName pointers (Obj()) is sound.
	index := make(map[fieldKey]*triggerField, len(fields))
	for _, f := range fields {
		index[fieldKey{f.owner.Obj(), f.fieldName}] = f
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
			ast.Walk(&assignVisitor{p: p, index: index}, file)
		}
	})
}

// fieldKey identifies a triggerField by its owner struct's identity plus
// the field name — shared between markAssignments' index construction
// and assignVisitor's lookups.
type fieldKey struct {
	owner *types.TypeName
	field string
}

// selfCtx captures the receiver-parameter context of the innermost
// enclosing *method* (has a receiver) — see markAssignments' clause-3
// doc comment above for why this exists. nil whenever the walk is not
// inside a method's own body at all — a plain (receiver-less)
// *ast.FuncDecl, or any nesting entirely outside one.
//
// A nested *ast.FuncLit no longer resets this to nil (round 4 of PR
// #332's review — see the *ast.FuncLit case in assignVisitor.Visit):
// self now threads through closure boundaries unchanged, the same way
// it already threads through everything else. A FuncLit still can never
// itself BE a method (it has no receiver), so it never SETS self — it
// only preserves whatever self it was declared inside of.
type selfCtx struct {
	owner  *types.TypeName
	params map[string]bool
}

// assignVisitor implements ast.Visitor for markAssignments' single walk
// per file. It threads self (the innermost enclosing method's
// receiver+params, or nil) through *ast.FuncDecl boundaries by
// returning a NEW visitor value on descent, re-derived from that decl —
// ast.Walk's contract (Visit returns the Visitor to use for a node's
// children) restores the parent visitor, and hence its self,
// automatically once that subtree's traversal finishes; no manual stack
// push/pop is needed. *ast.FuncLit boundaries are NOT a self reset
// point (see selfCtx's doc comment and the *ast.FuncLit case below).
type assignVisitor struct {
	p     *packages.Package
	index map[fieldKey]*triggerField
	self  *selfCtx
}

func (v *assignVisitor) Visit(n ast.Node) ast.Visitor {
	switch node := n.(type) {
	case *ast.FuncDecl:
		return &assignVisitor{p: v.p, index: v.index, self: methodSelfCtx(v.p, node)}
	case *ast.FuncLit:
		// Round 4 of PR #332's review: a closure is never itself a
		// method, but that used to mean unconditionally RESETTING self
		// to nil on every *ast.FuncLit — which made the clause-3
		// exception one syntactic layer removable. A reviewer proved it
		// with an overlay-only plant:
		//
		//	func (c *Config) SetZzGateProbeNested(d ZzGateProbeNestedDispatcher) {
		//		helper := func() {
		//			c.ZzGateProbeNested = d
		//		}
		//		helper()
		//	}
		//
		// With zero call sites for SetZzGateProbeNested anywhere, the old
		// code reset self to nil the moment the walk descended into
		// `helper`'s body, so `c.ZzGateProbeNested = d` was judged by
		// clause 3 with self == nil — the exception's own guard
		// (`v.self != nil && v.self.owner == named.Obj()`) never had a
		// chance to fire, and the assignment counted as wiring even
		// though the enclosing setter is never invoked. Reported:
		// "scanned 61: 54 wired ... clean".
		//
		// Fixed by NOT resetting self here — it threads through
		// unchanged, same as every other node kind the switch doesn't
		// name explicitly (see the default `return v` at the bottom).
		// Now `c.ZzGateProbeNested = d` inside `helper` is evaluated
		// with self still pointing at SetZzGateProbeNested's own
		// receiver+params, the exception's guard matches (owner ==
		// Config, "d" is the setter's own parameter), and the assignment
		// is correctly excluded — the field falls through to clauses 1-2
		// exactly as it would if the closure didn't exist.
		//
		// This does NOT reopen the WithSessionHookRunner functional-
		// option idiom (core/session/manager.go:185-192): that pattern
		// is a PLAIN function — `func WithSessionHookRunner(h
		// SessionHookRunner) ManagerOption { return func(m *Manager) {
		// if h != nil { m.hooks = h } } }` — with no receiver, so
		// methodSelfCtx already returns nil for its *ast.FuncDecl before
		// the walk ever reaches the FuncLit. Inheriting "nil" through a
		// FuncLit that was already nil changes nothing. More generally,
		// the exception can only ever engage when self.owner equals the
		// assignment target's OWN receiver type (assignVisitor's
		// *ast.AssignStmt case, `v.self.owner == named.Obj()`) — a
		// closure inside one method that happens to assign a DIFFERENT
		// struct's field, or a closure inside a non-method function, is
		// unaffected either way, self-reset or not.
		return v
	case *ast.CompositeLit:
		t := v.p.TypesInfo.TypeOf(node)
		named, ok := t.(*types.Named)
		if !ok {
			return v
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
			tf, ok := v.index[fieldKey{named.Obj(), keyIdent.Name}]
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
			return v
		}
		name := sel.Sel.Name
		var fieldName string
		switch {
		case strings.HasPrefix(name, "Set") && len(name) > 3:
			fieldName = name[3:]
		case strings.HasPrefix(name, "With") && len(name) > 4:
			fieldName = name[4:]
		default:
			return v
		}
		recvType := v.p.TypesInfo.TypeOf(sel.X)
		if recvType == nil {
			return v
		}
		if ptr, ok := recvType.(*types.Pointer); ok {
			recvType = ptr.Elem()
		}
		named, ok := recvType.(*types.Named)
		if !ok {
			return v
		}
		// The derived fieldName is PascalCase (stripped straight off the
		// exported Set*/With* method name), but the field it wires is
		// routinely unexported camelCase — WithAuditEmitter wires
		// auditEmitter (core/fleet/context_graph_sync.go), WithAttachments
		// wires attachments (llm_provider_adapter.go). Try the exact name
		// first, then the lowercase-first-letter form. Found 2026-09-10
		// while re-measuring after the clause-3 exception above: both
		// fields' ONLY detection path used to be clause 3's unconditional
		// setter-body match (which doesn't care about names at all,
		// selectors carry the real field name verbatim) — once clause 3
		// stopped counting an uncalled setter's own body, these two
		// genuinely-wired fields (real non-nil call sites: core/rpc/api.go:
		// 3420 WithAuditEmitter, chat_runner.go:995 WithAttachments) came
		// up as false "unwired" purely because clause 2 could never match
		// their case-mismatched name. This is a real, independent gap in
		// clause 2 — not something to allowlist as a genuine finding, since
		// both fields ARE wired.
		tf, ok := v.index[fieldKey{named.Obj(), fieldName}]
		if !ok {
			if lowered := lowerFirst(fieldName); lowered != fieldName {
				tf, ok = v.index[fieldKey{named.Obj(), lowered}]
			}
		}
		if ok {
			// isBareNil applies here for the same reason it applies to
			// the composite-literal and plain-assignment clauses:
			// `x.SetDispatcher(nil)` must not count as wiring. Only the
			// unambiguous single-argument case is checked — a call with
			// zero or multiple arguments can't be mapped to "the
			// field's value" without guessing which argument is the
			// field, so those are left as best-effort assigned=true
			// (same leniency the multi-hop EnvDeps case documents).
			if len(node.Args) == 1 && isBareNil(node.Args[0]) {
				return v
			}
			tf.assigned = true
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
			if !ok {
				continue
			}
			tf, ok := v.index[fieldKey{named.Obj(), sel.Sel.Name}]
			if !ok {
				continue
			}
			if isBareNil(node.Rhs[i]) {
				continue
			}
			if v.self != nil && v.self.owner == named.Obj() {
				if rhsIdent, ok := node.Rhs[i].(*ast.Ident); ok && v.self.params[rhsIdent.Name] {
					// The clause-3 exception: this assignment sits
					// directly inside a method on the field's own owner
					// type, and the RHS is literally that method's own
					// parameter — i.e. this is the setter's body, not
					// proof the setter was ever called. Leave unassigned
					// here; clause 2 (a real Set*/With* call site with a
					// non-nil argument) or clause 1 (a composite literal)
					// must supply the actual evidence.
					continue
				}
			}
			tf.assigned = true
		}
	}
	return v
}

// methodSelfCtx returns the selfCtx for decl when it is a method (has a
// single receiver), or nil when decl is a plain function.
func methodSelfCtx(p *packages.Package, decl *ast.FuncDecl) *selfCtx {
	if decl.Recv == nil || len(decl.Recv.List) != 1 {
		return nil
	}
	recvType := p.TypesInfo.TypeOf(decl.Recv.List[0].Type)
	if recvType == nil {
		return nil
	}
	if ptr, ok := recvType.(*types.Pointer); ok {
		recvType = ptr.Elem()
	}
	named, ok := recvType.(*types.Named)
	if !ok {
		return nil
	}
	params := map[string]bool{}
	if decl.Type.Params != nil {
		for _, field := range decl.Type.Params.List {
			for _, name := range field.Names {
				params[name.Name] = true
			}
		}
	}
	return &selfCtx{owner: named.Obj(), params: params}
}

func isBareNil(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// lowerFirst returns s with its first byte lowercased — enough to turn a
// Set*/With* method's stripped PascalCase suffix ("AuditEmitter") into
// the unexported camelCase field name it conventionally wires
// ("auditEmitter"). ASCII-only (byte, not rune) because every field name
// in this codebase is ASCII; ASCII lowercasing an ASCII byte is safe and
// avoids importing unicode for ~a dozen call sites.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	b := s[0]
	if b >= 'A' && b <= 'Z' {
		b += 'a' - 'A'
	}
	return string(b) + s[1:]
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
