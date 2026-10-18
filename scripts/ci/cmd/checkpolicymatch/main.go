// Command checkpolicymatch is the Go half of
// scripts/ci/check-shipped-policy-matchable.sh — the G-4 gate from
// kitty-specs/trust-surfaces-that-fire-01PMZ202 (spec.md §G-4, WP18):
// "a shipped policy must be able to match."
//
// THE DEFECT CLASS (F24)
// -----------------------
// filesystem-full-recommended.cedar shipped, was offered by name in the
// recipe-install UI, and told the user "the model CANNOT touch your
// secrets, credentials, or .git internals through this policy" — while
// every one of its forbid rules was structurally unmatchable, for THREE
// INDEPENDENT reasons at once (spec.md WP12 / F24):
//
//  1. Action mismatch: the rules named `Action::"file_read"` /
//     `"file_write"`, which are evaluated only by the agentgraph state
//     executor (core/agentgraph/exec_state.go's CheckFileRead/
//     CheckFileWrite gate-hook calls). The filesystem TOOLING the recipe
//     is actually about evaluates `read_filesystem`/`write_filesystem`
//     (core/tools/fs/gate.go).
//  2. Resource-type mismatch: the rules said `resource is FilesystemOp`.
//     CheckFileRead/CheckFileWrite build a `Filesystem::"<path>"` UID
//     (cedar.FilesystemUID), entity type `Filesystem` — not
//     `FilesystemOp`.
//  3. Context mismatch: every `when` clause read
//     `context.canonical_path`. CheckFileRead/CheckFileWrite pass a nil
//     context-attrs map — nothing ever populates that key for those two
//     actions.
//
// Any ONE of these three independently would have made every rule in
// the file inert — sensitive-path restrictions silently never fired,
// with a shipped, user-facing promise that they did. This gate is the
// one member of the trust-surfaces-that-fire gate set that can see this
// class, because it is the only one that checks all three legs against
// the SAME policy rule at once.
//
// THE THREE LEGS (spec.md §G-4, verbatim)
// -----------------------------------------
// For every `.cedar` file under core/policy/cedar/policies/, for every
// rule's action(s) X, assert against the Go side:
//
//	(a) X has an evaluator (a non-test call site that both references
//	    the Action* constant and invokes .Evaluate(...)).
//	(b) some production UID builder emits the rule's declared resource
//	    entity type <T> specifically FOR that evaluator (not merely
//	    "emits <T> somewhere in the tree" — see "WHY FUNCTION-LEVEL
//	    GRANULARITY, NOT FILE-LEVEL" below for why this distinction is
//	    load-bearing).
//	(c) the evaluator populates every context key the rule's `when`/
//	    `unless` clause reads, for that same action.
//
// DESIGN CHOICE: CONSTANTS, NOT CALL-SITE ENUMERATION, FOR THE ACTION
// SET — BUT FUNCTION-LEVEL GRANULARITY FOR LEGS (b)/(c)
// ----------------------------------------------------------------------
// The task brief that produced this gate asked explicitly: compare
// against the Action*/EntityType* CONSTANTS in types.go, or against
// actual evaluation call sites? This tool does BOTH, at different
// layers, because the two questions it answers are different:
//
//   - "Does Action::"X" in a shipped policy correspond to a string this
//     engine can ever produce at all?" is answered against the
//     constants (types.go's Action*/EntityType* declarations) — cheap,
//     complete (every action string the engine will ever ask Cedar to
//     evaluate has a constant; there is no dynamic action-string
//     construction anywhere in this codebase), and exactly what leg (a)
//     needs as its universe.
//   - "Does the evaluator for X actually reach a .Evaluate(...) call,
//     build a <T>-typed resource, and populate <key> in its context
//     map?" cannot be answered from constants alone — it is a claim
//     about a CALL SITE's actual behaviour, which is truer but more
//     expensive. This tool answers it by locating the non-test function
//     body (or bodies) that reference the action constant, and checking
//     THAT function's own text for the .Evaluate( call, the UID-builder
//     call, and the context-key literals — see below for why this must
//     be function-scoped rather than file-scoped.
//
// The constants are the right choice for leg (a)'s universe because
// they are complete and cheap; call-site inspection is the right (and
// only sound) choice for legs (b)/(c) because those legs are
// fundamentally about what a specific call site does, and the
// historical defect (F24) was invisible to any check that only asked
// "does the string exist" — file_read and read_filesystem BOTH exist as
// real, valid Action* constants; the defect was that the POLICY named
// the wrong one for the tooling it was written for.
//
// WHY FUNCTION-LEVEL GRANULARITY, NOT FILE-LEVEL
// -------------------------------------------------
// core/policy/cedar/hooks.go is a single ~1200-line file containing
// TWO DOZEN independent CheckX/GateX gate-hook helpers, each pairing
// its own action constant with its own resource-UID builder and its own
// context-attribute map. A file-level "does this file contain both the
// action name and a matching UID-builder call anywhere" check would
// have reported F24 as CLEAN: hooks.go contains BOTH ActionFileRead
// (via CheckFileRead) AND FilesystemOpUID (via a DIFFERENT gate-hook
// helper's unrelated resource family), so a file-level check cannot
// distinguish "this action's OWN evaluator builds this resource type"
// from "some other, unrelated evaluator elsewhere in this large file
// happens to build this resource type." This tool resolves each
// Action* reference to the enclosing *ast.FuncDecl (via go/parser,
// positionally — no type-checking needed, since this is a textual
// co-occurrence question within a known function body, not a type
// question) and scopes every leg-(b)/(c) check to that function's own
// source text. Verified against the real pre-WP12 defect shape: with
// this granularity, CheckFileRead's body contains ActionFileRead +
// FilesystemUID (entity type Filesystem) + a nil context map — leg (b)
// correctly fails FilesystemOp (the policy's declared type) is not in
// {Filesystem}, and leg (c) correctly fails canonical_path is not in
// the empty context-key set for that function.
//
// NOT A go/packages TOOL
// ------------------------
// Unlike checkseams/checknilopts/checkconfig, this tool does not need
// go/types — every question it asks ("does this function's source text
// contain X", "what Cedar JSON shape does this policy file parse to")
// is textual or handled by the cedar-go library's own parser, not a
// Go-type-system question. Using go/parser (syntax only, no
// packages.Load/NeedDeps) makes this the fastest of the four Go-backed
// gates in this directory.
//
// A DELIBERATELY TINY ALLOWLIST — NOT A CASUAL ESCAPE HATCH
// -------------------------------------------------------------
// The spec text for G-4 names no allowlist, and this gate's first
// instinct (an earlier revision of this file) was to ship with none at
// all: an unmatchable SHIPPED policy is not a style nit to defer, it is
// a silent, user-facing security promise that does not hold — the exact
// shape F24 was — and a casual escape hatch risks a second F24 shipping
// "clean" the same way the first one did.
//
// Running the finished gate against this tree surfaced a SECOND, live
// instance of the class it exists to catch: default_acp_policy.cedar's
// `acp_receive` permit/forbid rules (including "forbid ... revoked
// peers") have NO evaluator anywhere — `ActionACPReceive` has zero
// non-test references outside its own declaration in types.go, and no
// `ACP_Receive` function exists at all (only comments describing one:
// core/rpc/api.go:3372, core/rpc/views/acp/acp.go:654). Inbound ACP
// envelope handling is not gated by Cedar today, full stop; the
// forbid-revoked-peer rule is decorative. Building the real receive-side
// gate call is a genuine feature/security task this gate-authoring work
// does not do — it requires understanding the ACP receive pipeline this
// tool did not otherwise touch — so the honest choices were (a) block
// this gate's own introduction on fixing an unrelated pre-existing gap,
// or (b) the same dated, owner-named, monotonically-shrinking allowlist
// every sibling gate in this directory already uses for exactly this
// tension (checkseams/checknilopts/checkconfig all carry one). This file
// takes (b): scripts/ci/allowlists/i-shipped-policy-matchable.txt, seeded
// with ONLY this one real, already-investigated finding — not a general
// license to defer future violations. A NEW unmatchable rule introduced
// after this commit gets no such grace.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
)

const (
	typesGoPath  = "core/policy/cedar/types.go"
	engineGoPath = "core/policy/cedar/engine.go"
	policiesDir  = "core/policy/cedar/policies"
)

// scanDirs bounds the Go-source half of legs (a)/(b)/(c) to core/ and
// cmd/ — the same pair checkseams/checknilopts use, for the same
// reason (a gate-hook call site living in cmd/harness-vm is not
// hypothetical in this tree; see checknilopts's own header for the
// HV-03 precedent).
var scanDirs = []string{"core", "cmd"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "checkpolicymatch:", err)
		os.Exit(1)
	}
}

func run() error {
	typesSrc, err := os.ReadFile(typesGoPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", typesGoPath, err)
	}
	engineSrc, err := os.ReadFile(engineGoPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", engineGoPath, err)
	}

	actionByString := parseConstBlock(string(typesSrc), `Action\w+`)
	entityByString := parseConstBlock(string(typesSrc), `EntityType\w+`)
	if len(actionByString) == 0 || len(entityByString) == 0 {
		return fmt.Errorf("parsed zero Action*/EntityType* constants from %s — this is almost "+
			"certainly a checkpolicymatch bug (the file is known to declare dozens of each), not "+
			"an empty types.go", typesGoPath)
	}
	uidEntityForFunc, err := parseUIDBuilders(string(typesSrc), entityByString)
	if err != nil {
		return err
	}
	if len(uidEntityForFunc) == 0 {
		return fmt.Errorf("found zero *UID(...) builder functions in %s — this is almost certainly "+
			"a checkpolicymatch bug (FilesystemOpUID, ToolUID, etc. are known to exist), not a "+
			"gate malfunction", typesGoPath)
	}

	// ctxValueByGoName is the goName->value direction, parsed directly
	// from source rather than by inverting ctxKeyByGoName (which is
	// keyed by VALUE and therefore silently drops one side of any two Go
	// constants that happen to share the same Cedar string value — e.g.
	// CtxKeyToolName and CtxKeySecretToolName both equal "tool_name",
	// core/policy/cedar/engine.go:581,608. Inverting the value-keyed map
	// collapses that pair to whichever name the source-order regex scan
	// visited last (CtxKeySecretToolName), so a later lookup for
	// "CtxKeyToolName" — the name populateFamilyContext's ActionUseTool
	// case actually calls ensure() with — silently misses, and this gate
	// reports a real, populated context key (tool_name, for use_tool
	// dispatch) as unmatched. Go const names within one block are always
	// unique (a duplicate is a compile error), so keying by goName here
	// is collision-free regardless of how many names share a value.
	ctxValueByGoName := parseConstBlockGoNameToValue(string(engineSrc), `CtxKey\w+`)
	familyEnsuredKeys, err := parseFamilyContext(string(engineSrc), ctxValueByGoName)
	if err != nil {
		return err
	}

	deriv, err := scanGoSources(scanDirs, uidEntityForFunc, ctxValueByGoName)
	if err != nil {
		return err
	}
	// Family-ensured keys apply on top of whatever a family action's own
	// evaluator (populateFamilyContext's caller) directly sets — merge
	// rather than replace.
	for actionGoName, keys := range familyEnsuredKeys {
		if deriv.contextKeys[actionGoName] == nil {
			deriv.contextKeys[actionGoName] = map[string]bool{}
		}
		for k := range keys {
			deriv.contextKeys[actionGoName][k] = true
		}
	}

	cedarFiles, err := filepath.Glob(filepath.Join(policiesDir, "*.cedar"))
	if err != nil {
		return fmt.Errorf("glob %s: %w", policiesDir, err)
	}
	if len(cedarFiles) == 0 {
		return fmt.Errorf("found zero .cedar files under %s — this is almost certainly a "+
			"checkpolicymatch bug (default_policy.cedar etc. are known to exist), not a policy-free "+
			"tree", policiesDir)
	}
	sort.Strings(cedarFiles)

	var violations []string
	statementCount := 0
	for _, f := range cedarFiles {
		stmts, err := parseCedarFile(f)
		if err != nil {
			return fmt.Errorf("parse %s: %w", f, err)
		}
		for _, st := range stmts {
			if len(st.actions) == 0 {
				continue // unconstrained ("All") action scope — nothing to match against
			}
			statementCount++
			for _, actionStr := range st.actions {
				violations = append(violations, checkStatement(f, actionStr, st, actionByString, entityByString, deriv)...)
			}
		}
	}
	if statementCount == 0 {
		return fmt.Errorf("parsed %d .cedar file(s) but derived zero action-scoped rule statements — "+
			"this is almost certainly a checkpolicymatch bug (every shipped policy is known to name "+
			"at least one action), not an empty policy set", len(cedarFiles))
	}

	sort.Strings(violations)

	allow, err := loadAllowlist(allowlistPath)
	if err != nil {
		return err
	}
	unlisted := diff(violations, allow)
	stale := diff(allow, violations)

	fmt.Printf("[shipped-policy-matchable] checked %d action-scoped rule statement(s) across %d "+
		".cedar file(s) against %d Action* / %d EntityType* constants: %d violation(s) (%d "+
		"allowlisted, %d unlisted).\n",
		statementCount, len(cedarFiles), len(actionByString), len(entityByString),
		len(violations), len(violations)-len(unlisted), len(unlisted))

	fail := false
	if len(unlisted) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] FAIL: shipped .cedar rule(s) that cannot match, not in "+allowlistPath+":")
		for _, v := range unlisted {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] Retarget the rule onto an action/resource-type/context-key")
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] the real evaluator produces, or add a DATED line to")
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] "+allowlistPath+" naming the blocker and owner — see that")
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] file's header for why this gate's allowlist stays minimal.")
		fail = true
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] FAIL: STALE entries in "+allowlistPath+" — no longer a violation:")
		for _, v := range stale {
			fmt.Fprintln(os.Stderr, "    "+v)
		}
		fmt.Fprintln(os.Stderr, "[shipped-policy-matchable] Delete the line(s) — allowlists shrink monotonically.")
		fail = true
	}
	if fail {
		os.Exit(2)
	}
	fmt.Println("[shipped-policy-matchable] clean.")
	return nil
}

const allowlistPath = "scripts/ci/allowlists/i-shipped-policy-matchable.txt"

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

// ---------------------------------------------------------------------
// types.go / engine.go constant + UID-builder + context extraction
// ---------------------------------------------------------------------

// parseConstBlock extracts `<namePattern> = "<value>"` assignments from
// Go source text — used for Action*/EntityType* (types.go) and CtxKey*
// (engine.go). Textual, not AST-based: these are always simple untyped
// string const declarations, one per line, in this codebase's own
// established style (verified by inspection of every const block this
// tool depends on).
func parseConstBlock(src string, namePattern string) map[string]string {
	re := regexp.MustCompile(`(?m)^\s*(` + namePattern + `)\s*=\s*"([^"]*)"`)
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[2]] = m[1] // value -> Go const name
	}
	return out
}

// parseConstBlockGoNameToValue is parseConstBlock's mirror, keyed by the
// Go constant NAME rather than by its Cedar string VALUE. Go identifiers
// within one const block are always unique (a duplicate name is a
// compile error), so this mapping never loses information the way
// inverting parseConstBlock's value-keyed map would when two constants
// happen to share the same string value — e.g. core/policy/cedar/
// engine.go's CtxKeyToolName and CtxKeySecretToolName both equal
// "tool_name" (lines 581 and 608): a value-keyed map can only remember
// ONE of the two Go names for that shared value, silently dropping
// whichever one the source-order scan visited first. Every caller that
// needs "given this Go name, what Cedar string does it hold" (as
// opposed to "given this Cedar string, some Go name that holds it")
// must use this function, not invert parseConstBlock's map.
func parseConstBlockGoNameToValue(src string, namePattern string) map[string]string {
	re := regexp.MustCompile(`(?m)^\s*(` + namePattern + `)\s*=\s*"([^"]*)"`)
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2] // Go const name -> value
	}
	return out
}

// parseUIDBuilders finds every `func <Name>UID(...) cedar.EntityUID { ...
// }` in types.go and records which EntityType* constant its body passes
// to NewEntityUID. Brace-counted rather than regexp-bounded because
// function bodies contain arbitrarily nested braces (switch/if blocks —
// see FilesystemOpUID's own validateFamilyID branch).
func parseUIDBuilders(src string, entityByString map[string]string) (map[string]string, error) {
	// entityGoNameSet is just the set of valid EntityType* Go names, for
	// a plain membership test against whatever parseConstBlock's reverse
	// index would give us (only the go name is needed, not the string).
	entityGoNames := map[string]bool{}
	for _, goName := range entityByString {
		entityGoNames[goName] = true
	}

	headerRe := regexp.MustCompile(`func (\w+UID)\(`)
	out := map[string]string{}
	for _, loc := range headerRe.FindAllStringSubmatchIndex(src, -1) {
		funcName := src[loc[2]:loc[3]]
		// Find the opening brace of the function body starting from the
		// header match, then count braces to the matching close.
		braceStart := strings.Index(src[loc[1]:], "{")
		if braceStart < 0 {
			continue
		}
		bodyStart := loc[1] + braceStart
		depth := 0
		end := -1
		for i := bodyStart; i < len(src); i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end != -1 {
				break
			}
		}
		if end == -1 {
			return nil, fmt.Errorf("unbalanced braces scanning %s's body starting at byte %d", funcName, bodyStart)
		}
		body := src[bodyStart : end+1]
		entRe := regexp.MustCompile(`NewEntityUID\(\s*(EntityType\w+)`)
		m := entRe.FindStringSubmatch(body)
		if m == nil {
			continue // not every *UID func necessarily builds via NewEntityUID directly (none known, but non-fatal if so)
		}
		if !entityGoNames[m[1]] {
			continue
		}
		out[funcName] = m[1]
	}
	return out, nil
}

// parseFamilyContext extracts populateFamilyContext's per-action ensured
// context keys: for each `case Action<A>, Action<B>:` block (up to the
// next `case`/`default`/closing brace), every `ensure(CtxKey<K>` /
// `ensureBool(CtxKey<K>` reference contributes CtxKey<K>'s string value
// to every action named in that case's label list.
func parseFamilyContext(engineSrc string, ctxValueByGoName map[string]string) (map[string]map[string]bool, error) {
	const marker = "func populateFamilyContext("
	idx := strings.Index(engineSrc, marker)
	if idx < 0 {
		return nil, fmt.Errorf("populateFamilyContext not found in %s — the function may have been "+
			"renamed; update this tool and the gate together", engineGoPath)
	}
	braceStart := strings.Index(engineSrc[idx:], "{")
	if braceStart < 0 {
		return nil, fmt.Errorf("populateFamilyContext has no body in %s", engineGoPath)
	}
	bodyStart := idx + braceStart
	depth := 0
	end := -1
	for i := bodyStart; i < len(engineSrc); i++ {
		switch engineSrc[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("unbalanced braces scanning populateFamilyContext's body")
	}
	body := engineSrc[bodyStart : end+1]

	caseRe := regexp.MustCompile(`case ((?:Action\w+,?\s*)+):`)
	locs := caseRe.FindAllStringSubmatchIndex(body, -1)
	actionNameRe := regexp.MustCompile(`Action\w+`)
	ensureRe := regexp.MustCompile(`ensure(?:Bool)?\(\s*(CtxKey\w+)`)

	out := map[string]map[string]bool{}
	for i, loc := range locs {
		actions := actionNameRe.FindAllString(body[loc[2]:loc[3]], -1)
		blockEnd := len(body)
		if i+1 < len(locs) {
			blockEnd = locs[i+1][0]
		}
		block := body[loc[1]:blockEnd]
		for _, m := range ensureRe.FindAllStringSubmatch(block, -1) {
			ctxValue, ok := ctxValueByGoName[m[1]]
			if !ok {
				continue
			}
			for _, a := range actions {
				if out[a] == nil {
					out[a] = map[string]bool{}
				}
				out[a][ctxValue] = true
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------
// Go-source scan: per-function derivation of evaluator/entity/context
// ---------------------------------------------------------------------

type derived struct {
	hasEvaluator map[string]bool            // actionGoName -> true
	builtEntity  map[string]map[string]bool // actionGoName -> set of EntityType* go names
	contextKeys  map[string]map[string]bool // actionGoName -> set of raw context-key strings
}

// funcInfo is one non-test top-level function/method's textual
// derivation, collected in pass 1 of scanGoSources before any
// per-action attribution happens.
type funcInfo struct {
	name        string          // bare identifier (receiver stripped) — e.g. "Check", "CheckFileRead"
	actionsHere []string        // Action* identifiers referenced in this function's own body
	hasEvaluate bool            // body contains ".Evaluate("
	builtHere   map[string]bool // EntityType* go names built by a *UID( call in this body
	keysHere    map[string]bool // raw context-key strings found in this body
	calledNames map[string]bool // bare callee identifiers found in this body (both `x.Name(` and `Name(`)
}

// scanGoSources derives, per Action* Go constant, whether it has a
// reachable evaluator, which EntityType* values its evaluator's UID
// builder(s) produce, and which raw context-key strings its evaluator
// populates.
//
// TWO PASSES, not one, because of a real indirection shape this tool's
// own calibration run found: core/rpc/views/acp/acp.go's ACP_Dispatch
// references cedar.ActionACPSend but calls a.cedarEng.Check(...), not
// .Evaluate(...) directly — EngineAdapter.Check (same file) is the
// function that actually calls .Evaluate( and builds the
// ACPEnvelope-typed resource UID (cedar.ACPEnvelopeUID), and Check's
// own body contains NO Action* literal at all (the action is a runtime
// string parameter). A single-pass, per-function-only scan reported
// ActionACPSend as having no evaluator and no UID builder — a FALSE
// POSITIVE this tool's own negative-control obligation (CLAUDE.md
// non-negotiable #3) surfaced before this file shipped: acp_send is
// genuinely wired end to end and denies a revoked peer today.
//
// Pass 1 collects one funcInfo per non-test top-level function/method,
// independent of whether it references any action. Pass 2 identifies
// "generic evaluators" — functions with hasEvaluate=true and ZERO
// action references of their own (the EngineAdapter.Check shape) — and
// unions their built-entity sets under bareName. Pass 3 attributes each
// action-referencing function's OWN direct evaluate/build/context
// signal, WIDENED one hop: if the function doesn't call .Evaluate(
// itself but DOES call something named like a known generic evaluator,
// it inherits that generic evaluator's hasEvaluate=true and built-entity
// set. Matched by bare callee name only (no type resolution — this
// stays a textual tool, not a go/types one); calibrated to be safe in
// practice: of five distinct `Check` methods under core/ today, only
// EngineAdapter.Check's own body calls .Evaluate(, so only it is ever
// treated as generic — see the allowlist-free design note in this
// file's package doc for why a heuristic this loose is acceptable here
// (a false negative just means a real gap goes unreported until the
// next calibration; a false positive would mean reporting an F24-shaped
// bug as clean, which this widening is calibrated against, not toward).
//
// This one-hop delegation widening is NOT applied to context keys: a
// generic Check-shaped function receives its context map from the
// CALLER as an opaque map[string]interface{}/map[cedar.String]cedar.Value
// (see EngineAdapter.Check's own ctxMap conversion loop, which touches
// no key name literally), so the real key literals live in the CALLING
// function's own body — exactly where pass 1 already looks. No widening
// is needed for that leg, and none is applied.
func scanGoSources(dirs []string, uidEntityForFunc map[string]string, ctxValueByGoName map[string]string) (*derived, error) {
	actionIdentRe := regexp.MustCompile(`\bAction\w+\b`)
	ctxKeyIdentRe := regexp.MustCompile(`\bCtxKey\w+\b`)
	// Two map-key literal shapes coexist in this codebase:
	//   - bare string key:      "peer_trust_tier": meta.trustTier,
	//     (core/rpc/views/acp/acp.go:404)
	//   - cedar.String(...)-wrapped key: cedar.String("created_by"): ...,
	//     (core/policy/cedar/hooks.go's GateScheduledChatExecute et al.)
	// A single regex requiring the colon to immediately follow the
	// closing quote (mapKeyLiteralRe alone) misses the second shape
	// entirely — the closing paren sits between the quote and the
	// colon. Both are needed.
	mapKeyLiteralRe := regexp.MustCompile(`"([a-z][a-z0-9_]*)"\s*:`)
	wrappedKeyLiteralRe := regexp.MustCompile(`cedar\.String\(\s*"([a-z][a-z0-9_]*)"\s*\)\s*:`)
	funcCallRe := regexp.MustCompile(`(\w+)\(`)
	calleeRe := regexp.MustCompile(`\.(\w+)\(|(?:^|[^.\w])(\w+)\(`)

	var infos []*funcInfo
	fset := token.NewFileSet()
	for _, dir := range dirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if info.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative walk, not user input
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				// A file that fails to parse is a build break the Go
				// compiler will already have caught elsewhere in CI;
				// don't mask it as a policy-match failure.
				return fmt.Errorf("parse %s: %w", path, err)
			}
			for _, decl := range file.Decls {
				fdecl, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				start := fset.Position(fdecl.Pos()).Offset
				end := fset.Position(fdecl.End()).Offset
				if start < 0 || end > len(src) || start >= end {
					continue
				}
				body := string(src[start:end])

				fi := &funcInfo{
					name:        fdecl.Name.Name,
					actionsHere: uniqueStrings(actionIdentRe.FindAllString(body, -1)),
					hasEvaluate: strings.Contains(body, ".Evaluate("),
					builtHere:   map[string]bool{},
					keysHere:    map[string]bool{},
					calledNames: map[string]bool{},
				}
				for _, m := range funcCallRe.FindAllStringSubmatch(body, -1) {
					if ent, ok := uidEntityForFunc[m[1]]; ok {
						fi.builtHere[ent] = true
					}
				}
				for _, m := range mapKeyLiteralRe.FindAllStringSubmatch(body, -1) {
					fi.keysHere[m[1]] = true
				}
				for _, m := range wrappedKeyLiteralRe.FindAllStringSubmatch(body, -1) {
					fi.keysHere[m[1]] = true
				}
				for _, m := range ctxKeyIdentRe.FindAllString(body, -1) {
					if v, ok := ctxValueByGoName[m]; ok {
						fi.keysHere[v] = true
					}
				}
				for _, m := range calleeRe.FindAllStringSubmatch(body, -1) {
					if m[1] != "" {
						fi.calledNames[m[1]] = true
					}
					if m[2] != "" {
						fi.calledNames[m[2]] = true
					}
				}
				infos = append(infos, fi)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Pass 2: generic evaluators — a function with no action reference
	// of its own whose body directly calls .Evaluate(.
	genericBuilt := map[string]map[string]bool{}
	for _, fi := range infos {
		if len(fi.actionsHere) > 0 || !fi.hasEvaluate {
			continue
		}
		if genericBuilt[fi.name] == nil {
			genericBuilt[fi.name] = map[string]bool{}
		}
		for e := range fi.builtHere {
			genericBuilt[fi.name][e] = true
		}
	}

	// Pass 3: attribute, widened one hop through a generic evaluator
	// when the function itself doesn't call .Evaluate(.
	out := &derived{
		hasEvaluator: map[string]bool{},
		builtEntity:  map[string]map[string]bool{},
		contextKeys:  map[string]map[string]bool{},
	}
	for _, fi := range infos {
		if len(fi.actionsHere) == 0 {
			continue
		}
		hasEvaluate := fi.hasEvaluate
		built := map[string]bool{}
		for e := range fi.builtHere {
			built[e] = true
		}
		if !hasEvaluate {
			for called := range fi.calledNames {
				builtByCalled, isGeneric := genericBuilt[called]
				if !isGeneric {
					continue
				}
				hasEvaluate = true
				for e := range builtByCalled {
					built[e] = true
				}
			}
		}

		for _, a := range fi.actionsHere {
			if hasEvaluate {
				out.hasEvaluator[a] = true
			}
			if len(built) > 0 {
				if out.builtEntity[a] == nil {
					out.builtEntity[a] = map[string]bool{}
				}
				for e := range built {
					out.builtEntity[a][e] = true
				}
			}
			if len(fi.keysHere) > 0 {
				if out.contextKeys[a] == nil {
					out.contextKeys[a] = map[string]bool{}
				}
				for k := range fi.keysHere {
					out.contextKeys[a][k] = true
				}
			}
		}
	}
	return out, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Cedar policy file parsing (via the real cedar-go parser/JSON encoder
// — the SAME library core/policy/cedar/engine.go uses to load these
// exact files in production, not a hand-rolled regex parser)
// ---------------------------------------------------------------------

type cedarStatement struct {
	file          string
	effect        string
	actions       []string // action ID strings; empty means unconstrained ("All")
	resourceTypes []string // entity type strings referenced by the resource scope; empty means unconstrained
	contextKeys   []string // raw context.<key> attribute names referenced anywhere in when/unless bodies
}

type entityUIDJSON struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type scopeJSON struct {
	Op         string          `json:"op"`
	Entity     *entityUIDJSON  `json:"entity,omitempty"`
	Entities   []entityUIDJSON `json:"entities,omitempty"`
	EntityType string          `json:"entity_type,omitempty"`
}

type conditionJSON struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

type policyJSON struct {
	Effect     string          `json:"effect"`
	Principal  scopeJSON       `json:"principal"`
	Action     scopeJSON       `json:"action"`
	Resource   scopeJSON       `json:"resource"`
	Conditions []conditionJSON `json:"conditions,omitempty"`
}

type policySetJSON struct {
	StaticPolicies map[string]policyJSON `json:"staticPolicies"`
}

func parseCedarFile(path string) ([]cedarStatement, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative path under scripts/ci control
	if err != nil {
		return nil, err
	}
	ps, err := cedar.NewPolicySetFromBytes(path, data)
	if err != nil {
		return nil, fmt.Errorf("cedar parse: %w", err)
	}
	raw, err := ps.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("cedar json encode: %w", err)
	}
	var parsed policySetJSON
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}

	// Deterministic order: staticPolicies is a Go map keyed "policy0",
	// "policy1", ... — sort so output/violation ordering doesn't depend
	// on map iteration order.
	ids := make([]string, 0, len(parsed.StaticPolicies))
	for id := range parsed.StaticPolicies {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []cedarStatement
	for _, id := range ids {
		p := parsed.StaticPolicies[id]
		st := cedarStatement{file: path, effect: p.Effect}
		switch p.Action.Op {
		case "==":
			if p.Action.Entity != nil {
				st.actions = []string{p.Action.Entity.ID}
			}
		case "in":
			if len(p.Action.Entities) > 0 {
				for _, e := range p.Action.Entities {
					st.actions = append(st.actions, e.ID)
				}
			} else if p.Action.Entity != nil {
				st.actions = []string{p.Action.Entity.ID}
			}
		} // "All" (or anything else): leave st.actions empty — unconstrained

		switch p.Resource.Op {
		case "is":
			if p.Resource.EntityType != "" {
				st.resourceTypes = []string{p.Resource.EntityType}
			}
		case "==":
			if p.Resource.Entity != nil {
				st.resourceTypes = []string{p.Resource.Entity.Type}
			}
		case "in":
			for _, e := range p.Resource.Entities {
				st.resourceTypes = append(st.resourceTypes, e.Type)
			}
			if p.Resource.Entity != nil {
				st.resourceTypes = append(st.resourceTypes, p.Resource.Entity.Type)
			}
		} // "All": unconstrained resource — nothing to check for leg (b)

		keySet := map[string]bool{}
		for _, c := range p.Conditions {
			collectContextAttrs(c.Body, keySet)
		}
		for k := range keySet {
			st.contextKeys = append(st.contextKeys, k)
		}
		sort.Strings(st.contextKeys)
		st.resourceTypes = uniqueStrings(st.resourceTypes)

		out = append(out, st)
	}
	return out, nil
}

// collectContextAttrs recursively walks a Cedar JSON expression node
// (decoded as a generic interface{} tree — see the package's own use of
// the official Cedar JSON policy format, whose node shapes are a fixed,
// documented set: https://docs.cedarpolicy.com/policies/json-format.html)
// looking for the `.` (attribute-access) operator applied to `context`:
// `{"." : {"left": {"Var": "context"}, "attr": "<key>"}}`. Recurses into
// every object/array value regardless of which operator it belongs to
// (&&, ||, like, is, if-then-else, ...) so a context reference nested
// arbitrarily deep in the condition body is still found — the exact
// shape filesystem-full-recommended.cedar's `context.canonical_path like
// "*/.ssh/*" || context.canonical_path like "*/.ssh"` requires (two
// separate context accesses, each nested inside a `like` inside an
// `||`).
func collectContextAttrs(raw json.RawMessage, out map[string]bool) {
	if len(raw) == 0 {
		return
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return
	}
	walkContextAttrs(generic, out)
}

func walkContextAttrs(node interface{}, out map[string]bool) {
	switch v := node.(type) {
	case map[string]interface{}:
		if access, ok := v["."]; ok {
			if accessMap, ok := access.(map[string]interface{}); ok {
				if left, ok := accessMap["left"].(map[string]interface{}); ok {
					if varName, ok := left["Var"].(string); ok && varName == "context" {
						if attr, ok := accessMap["attr"].(string); ok {
							out[attr] = true
						}
					}
				}
			}
		}
		for _, child := range v {
			walkContextAttrs(child, out)
		}
	case []interface{}:
		for _, child := range v {
			walkContextAttrs(child, out)
		}
	}
}

// ---------------------------------------------------------------------
// per-statement/action leg checking
// ---------------------------------------------------------------------

func checkStatement(file, actionStr string, st cedarStatement, actionByString, entityByString map[string]string, deriv *derived) []string {
	var out []string
	prefix := fmt.Sprintf("%s (%s Action::%q)", file, st.effect, actionStr)

	actionGoName, ok := actionByString[actionStr]
	if !ok {
		out = append(out, fmt.Sprintf(
			"%s: no Action* constant in %s has value %q — this action string does not correspond "+
				"to anything the engine can ever evaluate", prefix, typesGoPath, actionStr))
		return out // legs (b)/(c) are meaningless against an action the engine cannot recognise at all
	}

	// Leg (a): the action has an evaluator.
	if !deriv.hasEvaluator[actionGoName] {
		out = append(out, fmt.Sprintf(
			"%s: %s has no non-test evaluator (no .Evaluate( call site anywhere under core/ or cmd/ "+
				"references it) — this rule can never be reached", prefix, actionGoName))
	}

	// Leg (b): a production UID builder emits the declared resource type
	// FOR this action's own evaluator.
	for _, rt := range st.resourceTypes {
		entityGoName, ok := entityByString[rt]
		if !ok {
			out = append(out, fmt.Sprintf(
				"%s: resource type %q has no EntityType* constant in %s — Cedar's `is`/`==` clause "+
					"can never match a real resource UID", prefix, rt, typesGoPath))
			continue
		}
		if !deriv.builtEntity[actionGoName][entityGoName] {
			out = append(out, fmt.Sprintf(
				"%s: no production UID builder called from %s's own evaluator emits resource type "+
					"%s — the policy's `resource is/== %s` clause can never match", prefix, actionGoName, rt, rt))
		}
	}

	// Leg (c): the evaluator populates every context key the rule reads.
	for _, key := range st.contextKeys {
		if !deriv.contextKeys[actionGoName][key] {
			out = append(out, fmt.Sprintf(
				"%s: context.%s is read by a when/unless clause but nothing in %s's own evaluator "+
					"(or populateFamilyContext, for family actions) ever sets it — Cedar treats a "+
					"missing context attribute as an evaluation error, which the engine maps to Deny, "+
					"not to this rule matching", prefix, key, actionGoName))
		}
	}

	return out
}
