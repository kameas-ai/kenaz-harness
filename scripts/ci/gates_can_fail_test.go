// This file is the regression test for the bug class the 2026-08-08 CI-gate
// sweep found: gates that pass because they inspected nothing.
//
// Four instances turned up in one night — tag-on-merge fire-and-forgetting the
// release workflow, kenaz-fleet's `go vet` on a module with no packages,
// check-bundle-size.mjs resolving dist/assets against the wrong root, and
// check-css-tokens.mjs crashing ENOENT under a `continue-on-error: true` step.
// The shared shape is a gate whose "clean" verdict is indistinguishable from
// "did not look".
//
// Two properties are asserted here, per gate:
//
//  1. CWD-INDEPENDENCE. Every gate must produce the same verdict from the repo
//     root and from an unrelated directory. Six of these scripts used to print
//     "clean" and exit 0 when run from scripts/ci/, because their hardcoded
//     relative paths (core/, frontend/src) resolved to nothing.
//
//  2. IT ACTUALLY FIRES. A planted violation must make the gate exit non-zero.
//     Testing this is the only way to know; every gate in this directory
//     "looked correct" while being incapable of failing.
//
// THIS TEST RUNS IN CI as its own "gate meta-tests" step in pr.yml's
// test-go job (`go test ./scripts/... -count=1`), separated from the
// `-race` suite deliberately: these tests shell out to mutate a scratch
// tree, and running them concurrently with `-race` poisons the Go
// build cache (sources hashed mid-edit — see CLAUDE.md's "Cross-cutting
// risks" note and spec.md §7 for upgrade-path-coverage-01PMUG01, which
// hit this directly while writing its own planted-violation cases).

package ci_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// runGate executes scripts/ci/<script> with the given working directory and
// returns its exit code plus combined output.
func runGate(t *testing.T, script, workdir string) (int, string) {
	t.Helper()
	scriptPath := filepath.Join(repoRoot(t), "scripts", "ci", script)
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running %s: %v (output: %s)", script, err, out)
	return -1, ""
}

// runGateEnv is runGate with extra environment variables layered on top
// of the current process's environment. nil/empty env behaves exactly
// like runGate.
func runGateEnv(t *testing.T, script, workdir string, env map[string]string) (int, string) {
	t.Helper()
	if len(env) == 0 {
		return runGate(t, script, workdir)
	}
	scriptPath := filepath.Join(repoRoot(t), "scripts", "ci", script)
	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running %s: %v (output: %s)", script, err, out)
	return -1, ""
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// cwdSensitiveGates are the gates that scan a hardcoded repo-relative path.
// Each one previously reported success when invoked from the wrong directory.
var cwdSensitiveGates = []string{
	"check-binding-names.sh",
	"check-emitter-isolation.sh",
	"check-wailsjs-isolation.sh",
	"check-single-persistence-file.sh",
	"check-test-only-symbols.sh",
	"check-no-credential-in-ui.sh",
	"check-no-user-content-in-slog.sh",
	"check-serve-dispatch-drift.sh",
	"check-builtin-tool-registration.sh",
	"check-single-move-writer.sh",
	"check-cedar-gate-arguments.sh",
	"check-cedar-engine-singleton.sh",
	"check-broker-topic-consumers.sh",
	"check-listpending-coverage.sh",
	"check-upgrade-snapshots-locked.sh",
	"check-destructive-migration-coverage.sh",
	"check-tool-containment-unconditional.sh",
	"check-upgrade-snapshot-present.sh",
	"check-graph-write-paths.sh",
	"check-entrypoint-coverage.sh",
	"check-installer-payload.sh",
	"check-audit-store-before-retention.sh",
	"check-fts-sync.sh",
	"check-bundle-verify-ordering.sh",
	"check-bundle-channel-kinds-sync.sh",
	"check-serve-gap-classification.sh",
}

// TestGates_VerdictIsIndependentOfWorkingDirectory is the direct regression
// test for the class. A gate invoked from a foreign cwd must reach the same
// conclusion it reaches from the repo root — never a vacuous pass.
func TestGates_VerdictIsIndependentOfWorkingDirectory(t *testing.T) {
	// NOT t.Parallel(). TestGates_PlantedViolationFires mutates the REAL
	// repo tree (it writes and removes probe files under root), and this
	// test reads that tree twice — once from the root, once from a foreign
	// cwd — expecting both reads to see the same thing. Running the two in
	// parallel meant the two reads could straddle a plant: PR #294 failed
	// in CI with the root run reporting clean and the foreign run failing
	// on a topic whose subscriber exists (DeferredAskPill.vue). The gate
	// was innocent; the harness was racing itself.
	root := repoRoot(t)

	// NOT t.Parallel() on the SUBTESTS either, and that is the half the
	// PR #294 fix missed. Dropping it from the parent alone is not enough:
	// a parallel subtest pauses and resumes only after the parent function
	// RETURNS, so these subtests were still in flight when the next
	// top-level test — TestGates_PlantedViolationFires — began writing
	// probe files into the real tree. The same symptom recurred on
	// release/v0.65.0 (run 32279405838): root clean, foreign cwd failing on
	// menu:cmd-palette:open, whose subscriber is right there at
	// frontend/src/App.vue:74. The gate was innocent again.
	//
	// These subtests are cheap (two gate invocations each) and there is no
	// reason to overlap them with a test that mutates the tree they read.
	for _, gate := range cwdSensitiveGates {
		t.Run(gate, func(t *testing.T) {
			foreign := t.TempDir()

			rootCode, rootOut := runGate(t, gate, root)
			awayCode, awayOut := runGate(t, gate, foreign)

			if rootCode != awayCode {
				t.Fatalf("%s reaches different verdicts depending on cwd: "+
					"exit %d from the repo root, exit %d from %s.\n"+
					"A gate whose answer depends on where it was invoked can be "+
					"silenced by moving the call site.\n"+
					"root output:\n%s\nforeign-cwd output:\n%s",
					gate, rootCode, awayCode, foreign, rootOut, awayOut)
			}
		})
	}
}

// TestGates_PlantedViolationFires plants a real violation for each gate and
// asserts the gate rejects it. Reading a gate is not evidence it works; every
// no-op found in this sweep read as correct.
func TestGates_PlantedViolationFires(t *testing.T) {
	root := repoRoot(t)

	cases := []struct {
		name       string
		gate       string
		file       string // repo-relative path to create, or "" to append
		append     string // when file already exists, append this instead
		content    string
		env        map[string]string // extra env vars for this case's runGate call, if any
		wantOutput string            // optional: substring the failure output must contain
		// file2/append2/content2: a SECOND plant, applied after the
		// first and cleaned up before it (LIFO). Some violation classes
		// span two files by construction — e.g. I13 clause 4 needs both
		// a `With<Name>(cedar.Gate)` declaration AND a call site in
		// core/rpc/api.go, and the omitted-argument shape only exists
		// once both are present. Empty file2 means "one plant only",
		// leaving every existing case above untouched.
		file2    string
		append2  string
		content2 string
	}{
		{
			name:    "binding-names/double-underscore",
			gate:    "check-binding-names.sh",
			file:    "core/rpc/bindings.go",
			append:  "\nfunc (bnd *Bindings) Zz__Injected() {}\n",
			content: "",
		},
		{
			name: "emitter-isolation/third-caller",
			gate: "check-emitter-isolation.sh",
			file: "core/rpc/views/zz_gate_probe.go",
			// The forbidden call is assembled at runtime rather than written
			// out. check-emitter-isolation.sh greps the whole repo for the
			// literal, so spelling it here would make this test file itself a
			// violation — and it has no allowlist marker to opt out with.
			// (Confirmed the hard way: the gate flagged this file.)
			content: "package views\n\nfunc zzGateProbe() { runtime." + "EventsEmit(nil, \"x\") }\n",
		},
		{
			name:    "wailsjs-isolation/unauthorized-import",
			gate:    "check-wailsjs-isolation.sh",
			file:    "frontend/src/zz_gate_probe.ts",
			content: "import { X } from '../wailsjs/go/rpc/Bindings';\n",
		},
		{
			name:    "single-persistence-file/second-path",
			gate:    "check-single-persistence-file.sh",
			file:    "core/rpc/views/settings/impl.go",
			append:  "\nconst zzGateProbe = \"shadow-settings.json\"\n",
			content: "",
		},
		{
			name:    "test-only-symbols/exported-fake",
			gate:    "check-test-only-symbols.sh",
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\ntype FakeGateProbe struct{}\n",
		},
		{
			name:    "no-credential-in-ui/value-on-credential-type",
			gate:    "check-no-credential-in-ui.sh",
			file:    "frontend/src/zz_gate_probe.ts",
			content: "export interface ZzGateProbeCredential {\n  value: string;\n}\n",
		},
		{
			name: "slog-privacy/non-slog-receiver",
			gate: "check-no-user-content-in-slog.sh",
			file: "core/rpc/zz_gate_probe.go",
			// `logger.` not `slog.` — the receiver spelling the gate was blind
			// to, and the one 86% of this codebase's log calls actually use.
			content: "package rpc\n\nfunc zzGateProbe(p string) { logger.Info(\"saved\", \"Path\", p) }\n",
		},
		{
			name: "agentgraph-convergence/I5-second-ask-store",
			gate: "check-agentgraph-convergence.sh",
			// A second store forks by declaring the AskBus read half
			// somewhere that does not ride core/elicitation. Planted in
			// an existing package (rather than a new one) so it trips I5
			// specifically and not I7's orphan-package rule.
			file:   "core/rpc/views/sessions/impl.go",
			append: "\ntype zzGateProbeAskStore struct{}\n\nfunc (zzGateProbeAskStore) LookupAnswer(runID, nodeID string) (string, bool) {\n\treturn \"\", false\n}\n",
		},
		{
			name: "builtin-tool-registration/unregistered-tool-package",
			gate: "check-builtin-tool-registration.sh",
			// The kenaz__monitor class: a package under core/tools/ that
			// declares a model-facing tool name and that no wiring site
			// ever imports, so no model can call it. Invisible to the
			// registered-tools tripwire, which only walks the tools that
			// DID register.
			file:    "core/tools/zzgateprobe/tool.go",
			content: "package zzgateprobe\n\nconst ToolName = \"kenaz__zz_gate_probe\"\n",
		},
		{
			name: "builtin-tool-registration/mcp-builtin-unregistered-tool-package",
			gate: "check-builtin-tool-registration.sh",
			// FR-008/AC-009 (mcp-connector-lifecycle-01PMMC01 WP05): before
			// this WP, TOOLS_ROOT pinned the scan to core/tools/, so
			// core/mcp/builtin/ was invisible to I11 — the exact blind
			// spot the harness-self MCP server (core/mcp/builtin/harness)
			// sat in. Plants a sibling package under core/mcp/builtin/
			// that no production file imports.
			file:    "core/mcp/builtin/zzgateprobe/tool.go",
			content: "package zzgateprobe\n\nconst ToolName = \"zz_gate_probe\"\n",
		},
		{
			name: "single-move-writer/second-seam-caller",
			gate: "check-single-move-writer.sh",
			// The convergence violation the transcript-move seam exists
			// to prevent: a second production caller of
			// Manager.AppendTranscriptEntry, i.e. a second path by which
			// move-kind rows reach session_messages. Two writers means
			// two definitions of what a move IS, and the pair that
			// drifts is the one that ships an orphaned tool_use to a
			// provider (the classic 400).
			//
			// Planted in an existing package so it does not also trip
			// I7's orphan-package rule.
			file:   "core/rpc/views/sessions/impl.go",
			append: "\nfunc zzGateProbeSecondMoveWriter(m *session.Manager) {\n\t_, _ = m.AppendTranscriptEntry(nil, \"\", session.TranscriptEntry{})\n}\n",
		},
		{
			name: "single-move-writer/move-field-assigned-outside-the-seam",
			gate: "check-single-move-writer.sh",
			// The in-package half the compiler cannot see: session.Message's
			// move fields are unexported, so only core/session can touch
			// them — and inside core/session, only moves.go may.
			file:   "core/session/manager.go",
			append: "\nfunc zzGateProbeMoveAssign(m *Message) { m.moveKind = MoveKindFinal }\n",
		},
		{
			name: "single-move-writer/inline-struct-literal",
			gate: "check-single-move-writer.sh",
			// The same violation gofmt would actually produce. A short
			// composite literal stays on one line, so the struct-literal
			// key is NOT at the start of its line — the shape the
			// original line-anchored pattern waved through.
			file:   "core/session/manager.go",
			append: "\nfunc zzGateProbeInlineLiteral() Message { return Message{moveKind: MoveKindFinal, moveTurnSpanID: \"t\"} }\n",
		},
		{
			name: "single-move-writer/column-helper-called-elsewhere",
			gate: "check-single-move-writer.sh",
			// Minting move metadata with zero characters matching the
			// assignment pattern: the assignment lives in moves.go, so
			// only the CALL is out of place. A helper any core/session
			// file can reach is the single-writer rule with a hole in it.
			file:   "core/session/manager.go",
			append: "\nfunc zzGateProbeHelperMint(m *Message) {\n\tapplyMoveColumns(m, sql.NullString{String: \"final\", Valid: true}, sql.NullInt64{}, sql.NullString{})\n}\n",
		},
		{
			name: "single-move-writer/direct-sql-on-the-move-columns",
			gate: "check-single-move-writer.sh",
			// The bypass that never touches session.Message at all:
			// unexported fields, the seam and every Go-level clause are
			// blind to an UPDATE that names the columns directly.
			file:    "core/session/zz_gate_probe.go",
			content: "package session\n\nconst zzGateProbeSQL = \"UPDATE session_messages SET move_index = ?, turn_span_id = ?\"\n",
		},
		{
			name: "single-move-writer/sql-with-a-json-tag-in-a-trailing-comment",
			gate: "check-single-move-writer.sh",
			// Clause 2c skips struct-tag lines so the stream event's
			// json:"move_index" tag can mirror the column. The first cut
			// of that skip matched `.*json:"` ANYWHERE on the line, which
			// meant a trailing comment naming the tag switched the clause
			// off for a line of real SQL. The skip is now shape-anchored;
			// this pins that it stays so.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nconst zzGateProbeSQL = \"UPDATE session_messages SET move_index = 1\" // mirrors json:\"move_index\"\n",
		},
		{
			name: "single-move-writer/sql-with-a-json-tag-in-an-sql-comment",
			gate: "check-single-move-writer.sh",
			// The same bypass without needing a Go comment at all: `--`
			// is SQL's comment marker, so this is one valid statement in
			// a raw string that the substring skip waved through.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nconst zzGateProbeSQL = `\nUPDATE session_messages SET turn_span_id = ? -- json:\"turn_span_id\"\n`\n",
		},
		{
			name: "single-move-writer/model-layer-args-assigned-outside-the-seam",
			gate: "check-single-move-writer.sh",
			// model-moves-transcript-01PMCH01 WP03 widened the gate to the
			// MODEL LAYER's raw tool arguments. A second writer for those
			// is worse than one for the display metadata: raw arguments
			// stamped off-seam skip AppendTranscriptEntry's
			// tool_call-only validation, so they can land on a row the
			// display surface renders.
			file:   "core/session/manager.go",
			append: "\nfunc zzGateProbeModelArgsMint(m *Message) {\n\tm.modelToolArgs = map[string]string{\"x\": \"y\"}\n}\n",
		},
		{
			name: "single-move-writer/direct-sql-on-the-model-args-column",
			gate: "check-single-move-writer.sh",
			// The SQL-level bypass for the same field: an UPDATE naming
			// model_tool_args reaches the durable row without touching
			// session.Message at all.
			file:    "core/session/zz_gate_probe.go",
			content: "package session\n\nconst zzGateProbeSQL = \"UPDATE session_messages SET model_tool_args = ?\"\n",
		},
		{
			name: "cedar-gate-arguments/allowall-as-call-argument",
			gate: "check-cedar-gate-arguments.sh",
			// The A1 shape: a gate handed cedar.AllowAll{} at the point
			// of construction, so it can never be replaced by a real
			// engine. Every Gate*/Check* helper still HAS call sites, so
			// I10 sees nothing wrong — the defect is the argument.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nvar zzGateProbe = cedar.NewLLMPolicyGuard(cedar.AllowAll{})\n",
		},
		{
			name: "cedar-gate-arguments/allowall-as-struct-field",
			gate: "check-cedar-gate-arguments.sh",
			// Same defect wearing a composite literal — the exact shape
			// of the memory-write gate before 2026-08-16.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nvar zzGateProbe = memoryGateAdapter{gate: cedar.AllowAll{}}\n",
		},
		{
			name: "cedar-gate-arguments/placeholder-never-replaced",
			gate: "check-cedar-gate-arguments.sh",
			// Clause 1 wearing a variable name. The legitimate idiom
			// (`var g cedar.Gate = AllowAll{}` THEN `if e != nil { g = e }`)
			// is deliberately not flagged, so the gate has to check that
			// the replacement actually exists — otherwise renaming the
			// literal into a variable silences it.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nfunc zzGateProbe() cedar.Gate {\n\tvar g cedar.Gate = cedar.AllowAll{}\n\treturn g\n}\n",
		},
		{
			name: "cedar-gate-arguments/config-gate-field-omitted",
			gate: "check-cedar-gate-arguments.sh",
			// The A2 half, and the reason a string-grep for AllowAll is
			// not enough: an omitted Config field leaves a nil gate,
			// which every helper short-circuits to
			// Allow("no engine wired (default-allow)"). There is no
			// literal anywhere to find — only the absence of one.
			file:    "core/rpc/views/zzgateprobe/impl.go",
			content: "package zzgateprobe\n\nimport \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\ntype Config struct {\n\tCedar cedar.Gate\n}\n",
		},
		{
			name: "cedar-gate-arguments/allowall-as-wrapped-call-argument",
			gate: "check-cedar-gate-arguments.sh",
			// gofmt's own output for a call too long for one line puts the
			// argument on its own line, where there is no '(' to anchor
			// on. The first cut of this gate anchored on '(' or ':' and
			// was therefore silenced by formatting the exact call its
			// header cites as defect A1. Caught by exclusion instead:
			// AllowAll is a violation unless it is an assignment RHS or a
			// return.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nvar zzGateProbe = cedar.NewLLMPolicyGuard(\n\tcedar.AllowAll{},\n)\n",
		},
		{
			name: "cedar-gate-arguments/allowall-in-slice-literal",
			gate: "check-cedar-gate-arguments.sh",
			// A composite-literal ELEMENT rather than a field value: '{'
			// precedes it, not '(' or ':'.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nvar zzGateProbe = []cedar.Gate{cedar.AllowAll{}}\n",
		},
		{
			name: "cedar-gate-arguments/config-gate-field-hidden-by-comment",
			gate: "check-cedar-gate-arguments.sh",
			// Field discovery used to anchor the type at end-of-line, so a
			// single trailing comment on the declaration removed the field
			// from clause 3's view — and adding one is a natural thing to
			// do while introducing the omission. Same for a struct tag.
			file:    "core/rpc/views/zzgateprobe/impl.go",
			content: "package zzgateprobe\n\nimport \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\ntype Config struct {\n\tCedar cedar.Gate // the policy gate\n}\n",
		},
		{
			// I13 clause 4 (model-authored-graphs-01PMGA01 UNIT-8(b)):
			// a cedar.Gate wired through a functional option rather than
			// a Config struct field — the shape clause 3 cannot see
			// because the agentgraph Manager has no Config struct
			// (C-010, manager.go:153's NewManager takes ...ManagerOption).
			// This plants a With<Name>(cedar.Gate) option that api.go
			// never references at all, mirroring the sibling
			// config-gate-field-omitted case one level up (a func
			// instead of a struct field).
			name:       "cedar-gate-arguments/with-option-never-called",
			gate:       "check-cedar-gate-arguments.sh",
			wantOutput: "WithZzGateProbeNeverCalled",
			file:       "core/rpc/views/zzgateprobe2/impl.go",
			content: "package zzgateprobe2\n\n" +
				"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"type Option func()\n\n" +
				"func WithZzGateProbeNeverCalled(g cedar.Gate) Option {\n" +
				"\treturn func() { _ = g }\n" +
				"}\n",
		},
		{
			// The other clause-4 branch: the option IS called at
			// api.go's wiring site, but with a literal nil — the exact
			// mutation tasks.md names ("drop the WithCedarGate(...)
			// argument from api.go"). This is what reverting
			// graphview.WithGraphCedarGate(graphCedarGate) to
			// graphview.WithGraphCedarGate(nil) at api.go:6941 looks
			// like, without hand-editing the real production wiring
			// line (a reversible-by-construction two-file plant
			// instead: file2 declares the option in an EXISTING
			// imported package/alias — graphview — so the api.go append
			// is genuinely valid Go, not merely gate-shaped text).
			name:       "cedar-gate-arguments/with-option-called-with-nil",
			gate:       "check-cedar-gate-arguments.sh",
			wantOutput: "core/rpc/api.go",
			file:       "core/rpc/views/agentgraph/zz_gate_probe_cedar_option.go",
			content: "package agentgraph\n\n" +
				"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"func WithZzGateProbeCalledWithNil(g cedar.Gate) ManagerOption {\n" +
				"\treturn func(m *Manager) { _ = g }\n" +
				"}\n",
			file2:   "core/rpc/api.go",
			append2: "\n\nvar zzGateProbeCalledWithNil = graphview.WithZzGateProbeCalledWithNil(nil)\n",
		},
		{
			name: "cedar-engine-singleton/second-call-site",
			gate: "check-cedar-engine-singleton.sh",
			// The WP05 defect exactly (consent-surfaces-truth-01PMTR01): a
			// second buildCedarGate/buildCedarEngineOrNil call site outside
			// the one construction call in rpc.New produces a SECOND,
			// independent *cedar.Engine with its own private PolicySet —
			// invisible to check-cedar-gate-arguments.sh (I13), which
			// checks the ARGUMENT at a call (is it AllowAll{}/nil?), not
			// the instance count. Thirteen such sites passed I13 clean
			// pre-hoist because every one of them passed a real, non-
			// AllowAll argument.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nvar zzGateProbeSecondEngine = buildCedarGate(\"/tmp/zz-gate-probe\")\n",
			// A non-zero exit alone would also be satisfied by a gate that
			// is permanently broken — the exact weak form the runner's
			// comment below warns about, and which this campaign has
			// already shipped once (check-tests-are-hermetic.sh). Pin the
			// message check 1 produces so the proof is about THIS
			// violation.
			wantOutput: "call sites construct a Cedar engine independently",
		},
		{
			name: "cedar-engine-singleton/direct-newengine-bypass",
			gate: "check-cedar-engine-singleton.sh",
			// The escape hatch check 1 alone cannot see: a hand-built
			// cedar.NewEngine(...) call that never goes through
			// buildCedarGate/buildCedarEngineOrNil at all, so counting
			// calls to those two names would miss it.
			file: "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\n" +
				"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"var zzGateProbeDirectEngine, _ = cedar.NewEngine(cedar.Options{})\n",
			// Must be check 2's message specifically: check 1 still sees
			// exactly one builder call here, so a failure mentioning call
			// sites would mean the gate fired for the wrong reason.
			wantOutput: "NewEngine( appears 3 time(s)",
		},
		{
			name: "cedar-engine-singleton/aliased-import-newengine-bypass",
			gate: "check-cedar-engine-singleton.sh",
			// The alias hole: `cedarpolicy "…/core/policy/cedar"` is an
			// idiom this repo already uses (core/rpc/contextbootstrap_
			// wiring.go:41, core/rpc/views/settings/fleet.go:15), so a
			// second engine can be constructed under any local name. A
			// gate anchored on the literal `cedar.NewEngine(` is blind to
			// it — verified by planting this exact file and watching the
			// pre-fix gate exit 0.
			file: "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\n" +
				"import cedarpolicy \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"var zzGateProbeAliasEngine, _ = cedarpolicy.NewEngine(cedarpolicy.Options{})\n",
			wantOutput: "NewEngine( appears 3 time(s)",
		},
		{
			name: "cedar-engine-singleton/engine-built-outside-core-rpc",
			gate: "check-cedar-engine-singleton.sh",
			// The scope hole: the singleton promise is process-wide, but a
			// gate scanning only core/rpc cannot see a second engine built
			// in any other package under core/. Verified the same way.
			file: "core/policy/zzgateprobe/probe.go",
			content: "package zzgateprobe\n\n" +
				"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"var ZzGateProbeEngine, _ = cedar.NewEngine(cedar.Options{})\n",
			wantOutput: "NewEngine( appears 3 time(s)",
		},
		{
			// vm-execution-surface-truth-01PMZD14 WP03 (HV-03/UNIT-1, G-1).
			// The cmd/ scope hole this WP closes: before widening Check 2's
			// scan root from core/ alone to core/+cmd/, a hand-built
			// *cedar.Engine anywhere under cmd/ — most concretely
			// cmd/harness-vm, which owns its own Cedar-gated model-call
			// path (agentexec.go) — bypassed the singleton exactly as
			// completely as one built under core/, and this gate could not
			// see it. VERIFIED pre-widening: with CORE_ROOT="core" alone,
			// the gate reported "no other production caller under core"
			// with this exact construction sitting one directory over
			// (spec.md §0.3, §4.1 D-2 — the evasion R-4 rejects).
			name: "cedar-engine-singleton/engine-built-under-cmd",
			gate: "check-cedar-engine-singleton.sh",
			file: "cmd/zzgateprobe/probe.go",
			content: "package zzgateprobe\n\n" +
				"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
				"var ZzGateProbeEngine, _ = cedar.NewEngine(cedar.Options{})\n",
			wantOutput: "NewEngine( appears 3 time(s)",
		},
		{
			name: "slog-privacy/typed-attr-constructor",
			gate: "check-no-user-content-in-slog.sh",
			file: "core/rpc/zz_gate_probe.go",
			// slog.String(...) is how a multi-line log call spells its keys.
			content: "package rpc\n\nvar zzGateProbe = slog.String(\"Prompt\", p)\n",
		},
		{
			name: "onboarding-starter-tools/unregistered-tool-name",
			// The planted tool name itself — printed only in the gate's
			// unregistered-names listing, so a gate that fails for any
			// other reason (or crashes) does not satisfy this proof.
			wantOutput: "harness_nonexistent_tool",
			gate:       "check-onboarding-starter-tools.sh",
			// The code.md:23 class (first-run-onboarding-01PMOB01 WP04): a
			// shipped starter prompt naming a harness_* tool register.go
			// does not register — the exact shape that let
			// harness_write_propose_cedar_policy survive its own deletion.
			// Appended to an existing starter (chat.md) rather than a new
			// file so the planted name lands inside the *.md glob the gate
			// actually scans.
			file:   "core/mcp/builtin/harness/onboarding/chat.md",
			append: "\n\nCall `harness_nonexistent_tool` to finish up.\n",
		},
		{
			name: "broker-topic-consumers/unconsumed-topic-const",
			// The planted const's own violation line — the gate prints
			// `ident = "value" (file:line) has no frontend subscriber…`
			// only when it actually derived and rejected this topic.
			wantOutput: `TopicNobodyReads = "nobody:reads-this"`,
			gate:       "check-broker-topic-consumers.sh",
			// self-update-repair-01PMUP01 WP06, spec §6: the planted-violation
			// proof the spec names by its exact variable name. A Topic* const
			// with no frontend subscriber, no Go subscriber, no
			// passthroughTopics entry, and no allowlist line — nobody
			// annotates it, so the gate's derived input set must catch it
			// on its own, not because someone declared it a violation.
			file:    "core/rpc/zz_gate_probe.go",
			content: "package rpc\n\nconst TopicNobodyReads = \"nobody:reads-this\"\n",
		},
		{
			name: "listpending-coverage/no-client-reader",
			// The planted binding's own failure line ("<fam>_ListPending
			// has no reader in …") — exit-code-only would also pass for
			// a gate that dies parsing bindings.go.
			wantOutput: "ZzGateProbe_ListPending has no reader",
			gate:       "check-listpending-coverage.sh",
			// The A11 shape exactly: a *_ListPending binding whose body
			// compiles and whose doc comment (if any) reads correctly, with
			// zero callers anywhere in harnessClient.ts. This is precisely
			// what let Permissions_ListPending sit dead for as long as it
			// did — the method LOOKED wired because it existed end to end
			// on the Go side.
			file:   "core/rpc/bindings.go",
			append: "\nfunc (b *Bindings) ZzGateProbe_ListPending() ([]FlatPermissionRequest, error) {\n\treturn nil, nil\n}\n",
		},
		{
			// upgrade-path-coverage-01PMUG01 WP02. Byte-mutate an already
			// COMMITTED snapshot file (core/storage/sqlite/testdata/
			// upgrade/v0.63.0/dump.sql — committed in this mission's own
			// WP01 commit, so it is present at HEAD by the time this test
			// runs) and confirm the gate rejects the mismatch. The gate's
			// real comparison base in CI is origin/main (see the script's
			// header) — UPGRADE_SNAPSHOTS_BASE_REF=HEAD here points it at
			// this branch's own last commit instead, which already
			// contains the unmutated file, so this exercises the SAME
			// git-diff codepath the CI gate uses, not a mock of it.
			name: "upgrade-snapshots-locked/byte-mutation",
			// The mutated file's own violation line, not the gate's
			// "[upgrade-snapshots-locked]" label — the label prefixes
			// EVERY failure this gate can emit, including "BASE_REF does
			// not resolve", so matching it would still pass for a gate
			// that can no longer find its comparison base at all.
			wantOutput: "v0.63.0/dump.sql differs from its merge-base",
			gate:       "check-upgrade-snapshots-locked.sh",
			file:       "core/storage/sqlite/testdata/upgrade/v0.63.0/dump.sql",
			append:     "-- zzGateProbe: this byte must not be here\n",
			env:        map[string]string{"UPGRADE_SNAPSHOTS_BASE_REF": "HEAD"},
		},
		{
			// upgrade-path-coverage-01PMUG01 WP03 (I14). A migration
			// whose Up() runs DROP TABLE with no populated-table test
			// referencing its ID and no allowlist entry must fail.
			name: "destructive-migration-coverage/uncovered-drop-table",
			// The planted migration's own ID, not the gate's
			// "[destructive-migration-coverage]" label — the label also
			// prefixes "checkdestructivemigrations failed to build", so
			// matching it would let a compile-broken checker satisfy
			// this proof.
			wantOutput: "storage/97-zz-gate-probe",
			gate:       "check-destructive-migration-coverage.sh",
			file:       "core/rpc/views/zzgateprobe/migration_probe.go",
			content: "package zzgateprobe\n\n" +
				"import (\n" +
				"\t\"context\"\n\n" +
				"\t\"github.com/kameas-ai/kenaz-harness/core/storage/migrations\"\n" +
				")\n\n" +
				"func zzGateProbeMigration() migrations.Migration {\n" +
				"\treturn migrations.Migration{\n" +
				"\t\tID:            \"storage/97-zz-gate-probe\",\n" +
				"\t\tVersion:       97,\n" +
				"\t\tOwningMission: \"storage\",\n" +
				"\t\tUpSource:      \"DROP " + "TABLE zz_gate_probe_table\",\n" +
				"\t\tUp: func(ctx context.Context, tx migrations.WriteTx) error {\n" +
				"\t\t\t_, err := tx.Exec(ctx, \"DROP " + "TABLE zz_gate_probe_table\")\n" +
				"\t\t\treturn err\n" +
				"\t\t},\n" +
				"\t}\n" +
				"}\n",
		},
		{
			// upgrade-path-coverage-01PMUG01 WP05 (FR-4b). Replants the
			// exact shape core/rpc/api_narrative_gate_boot_test.go had
			// before its fix: rpc.New(c) with no WithSettingsStore
			// override, so the settings store resolves through
			// settings.NewFileStoreFromEnv() -> os.UserConfigDir() (the
			// gate's sentinel HOME while this test runs; the developer's
			// real config dir on an unguarded run) and then a real write
			// (SetMemoryNarrativeEnabled) lands in it. This is a NEW
			// probe file rather than an edit to the real (now-fixed)
			// test file, matching the house convention elsewhere in this
			// table (zz_gate_probe.go) of planting the violation
			// alongside the real code rather than mutating it in place.
			name: "tests-are-hermetic/unsandboxed-settings-write",
			// Deliberately the PLANTED write's own path, not the gate's
			// generic header. The header appears for any sentinel change
			// at all — including the .kenaz/harness.log write that made
			// this gate fail unconditionally before it was carved out —
			// so matching on it would still pass for a gate that is
			// simply broken. settings.json is written only by the probe.
			wantOutput: "kenaz-harness/settings.json",
			gate:       "check-tests-are-hermetic.sh",
			file:       "core/rpc/zz_gate_probe_unsandboxed_settings_test.go",
			content: "package rpc\n\n" +
				"import (\n" +
				"\t\"context\"\n" +
				"\t\"testing\"\n\n" +
				"\t\"github.com/kameas-ai/kenaz-harness/core\"\n" +
				")\n\n" +
				"func TestZzGateProbeUnsandboxedSettingsWrite(t *testing.T) {\n" +
				"\tc, err := core.New(core.Options{DataDir: t.TempDir()})\n" +
				"\tif err != nil {\n" +
				"\t\tt.Fatalf(\"core.New: %v\", err)\n" +
				"\t}\n" +
				"\tapi := New(c)\n" +
				"\tif err := api.Settings().SetMemoryNarrativeEnabled(context.Background(), true); err != nil {\n" +
				"\t\tt.Fatalf(\"SetMemoryNarrativeEnabled: %v\", err)\n" +
				"\t}\n" +
				"}\n",
		},
		{
			// trust-surfaces-that-fire-01PMZ202 WP08 (UNIT-7), G-2 leg c: an
			// AllEvents entry with no firer AND no allowlist row — the
			// planted-violation shape the WP's own spec names ("an
			// AllEvents entry with no firer and no allowlist row"). A new
			// event is added to core/hooks.AllEvents via an
			// `AllEvents = append(AllEvents, ...)` reassignment (legal Go —
			// AllEvents is a package-level var, and this is not a second
			// `var AllEvents = []string{...}` declaration) so the plant can
			// land as a pure end-of-file append, the only shape the shared
			// plant() helper supports for an existing file. The gate's
			// leg-c parser is deliberately not limited to the hand-written
			// literal block for exactly this reason.
			name:       "hook-event-fire-sites/allevents-entry-no-firer-no-allowlist-row",
			gate:       "check-hook-event-fire-sites.sh",
			wantOutput: "zz_gate_probe_event",
			file:       "core/hooks/hooks.go",
			append: "\n\nconst EventZzGateProbe = \"zz_gate_probe_event\"\n\n" +
				"func zzGateProbeAppendsAnEventlessEvent() {\n" +
				"\tAllEvents = append(AllEvents, EventZzGateProbe)\n" +
				"}\n",
		},
		{
			// The absence gate. Unlike every other case in this table,
			// the violation cannot be planted as a file: the defect is a
			// snapshot directory that does NOT exist for a tag that DOES.
			// Adding a file cannot create that; advancing the tag can.
			//
			// UPGRADE_SNAPSHOT_PRESENT_MAX_TAG=v99.0.0 stands in for "a
			// release was just tagged and nobody ran upgrade-snapshot.sh"
			// — the exact situation that shipped on v0.63.2, v0.64.0 and
			// v0.64.1. It is not a suppression knob and cannot make a
			// real violation pass; it only chooses which tag counts as
			// newest, so the gate's real comparison runs against the real
			// committed snapshot directories.
			name: "upgrade-snapshot-present/chain-behind-newest-tag",
			// The gate's specific diagnosis, not its "[upgrade-snapshot-
			// present]" label — the label also prefixes "no stable release
			// tag is reachable" and "no snapshot directories", so matching
			// it would let a gate that cannot see tags at all satisfy this
			// proof.
			wantOutput: "chain is behind the newest release tag",
			gate:       "check-upgrade-snapshot-present.sh",
			env:        map[string]string{"UPGRADE_SNAPSHOT_PRESENT_MAX_TAG": "v99.0.0"},
		},
		{
			// structured-output-is-reachable-01PMZE14 WP03 (spec §7 G-1):
			// the class is a codegen'd manifest attr, declared and
			// authored, with no reader — ModelAttrs.JsonSchema (spec
			// §1.2) and ModelAttrs.StreamToChat (docs/unwired-
			// ledger.md:1674) are two real instances of it, both found
			// by hand because no gate saw node attrs at all before this
			// WP. Rather than editing the real ModelAttrs registration
			// (which check-knob-coverage.sh's own case-table convention
			// avoids — see the tests-are-hermetic case above), this
			// plants a new probe struct with one field registered and
			// one left untouched: the exact shape "someone adds a field
			// and forgets the registration" produces. AC-013/AC-014's
			// falsification (removing the real JsonSchema registration
			// and confirming TestKnobCoverage_ModelAttrs goes red) was
			// performed by hand against the real struct during WP02/
			// WP03 development; this case is the CI-enforced,
			// repeatable proof that the general mechanism — the one any
			// future mission's ModelAttrs-shaped struct rides — can
			// fail.
			name:       "knob-coverage/unregistered-field",
			wantOutput: "Unregistered",
			gate:       "check-knob-coverage.sh",
			file:       "core/agentgraph/zz_gate_probe_knob_coverage_test.go",
			content: "package agentgraph\n\n" +
				"import (\n" +
				"\t\"testing\"\n\n" +
				"\t\"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage\"\n" +
				")\n\n" +
				"// zzGateProbeAttrs mirrors ModelAttrs' shape for gate-probe purposes:\n" +
				"// a struct with one field registered and one left untouched, the exact\n" +
				"// class ModelAttrs.JsonSchema was before WP02/WP03.\n" +
				"type zzGateProbeAttrs struct {\n" +
				"\tRegistered   string\n" +
				"\tUnregistered string\n" +
				"}\n\n" +
				"func init() {\n" +
				"\tknobcoverage.Register[zzGateProbeAttrs](\"Registered\", \"gate-probe: deliberately leaves Unregistered untouched\")\n" +
				"}\n\n" +
				"func TestKnobCoverage_ZZGateProbe(t *testing.T) {\n" +
				"\tif got := knobcoverage.Uncovered[zzGateProbeAttrs](); len(got) != 0 {\n" +
				"\t\tt.Fatalf(\"gate-probe: uncovered fields %v (expected — this is the planted violation)\", got)\n" +
				"\t}\n" +
				"}\n",
		},
		{
			// entry-points-and-crash-reporting-01PMZD13 UNIT-2. A built,
			// signed, zipped .exe with no installer payload line
			// reproduces the kenaz-updater.exe defect: shipped in the zip,
			// silently absent from the NSIS installer.
			name:       "installer-payload/unlisted-build-target",
			wantOutput: "zzgateprobe.exe",
			gate:       "check-installer-payload.sh",
			file:       ".github/workflows/release.yml",
			append:     "\n      # zzGateProbe: -o \"build/bin/zzgateprobe.exe\"\n",
		},
		{
			// entry-points-and-crash-reporting-01PMZD13 UNIT-1. Deviates
			// from the mission's spec §5 gate table, which prescribes a
			// PLAIN `package main` file with no build tag as the planted
			// violation. That content is NOT a sound violation: `./cmd/...`
			// (added to pr.yml by this same unit) prefix-covers any bare
			// `cmd/<name>`, so a tagless probe is genuinely, correctly
			// covered and the gate must NOT fail on it — confirmed by
			// running exactly that probe first, which left the gate green.
			// The real defect class this gate exists to catch (spec §1.1)
			// is a build-TAG-gated entry point — cmd/harness-served's own
			// `serve` tag — that no pr.yml step passes -tags for, which a
			// same-build-tag probe reproduces soundly: `zzgateprobe` is
			// gated behind a tag no pr.yml step ever requests, so it is
			// genuinely uncovered under real Go build-constraint semantics.
			name:       "entrypoint-coverage/tagged-package-with-no-covering-step",
			wantOutput: "cmd/zzgateprobe",
			gate:       "check-entrypoint-coverage.sh",
			file:       "cmd/zzgateprobe/main.go",
			content:    "//go:build zzgateprobe\n\npackage main\n\nfunc main() {}\n",
		},
		{
			// entry-points-and-crash-reporting-01PMZD13 UNIT-4.
			// check-seam-implementers.sh's FIRST EVER planted-violation
			// proof (spec §5). ZzGateProbeUnsatisfiableSeam's method takes
			// a brand-new unexported param type defined nowhere else in
			// the tree, so no real type — production or test — can
			// possibly implement it. The gate must name the interface,
			// not just exit non-zero.
			name:       "seam-implementers/unsatisfiable-interface",
			wantOutput: "ZzGateProbeUnsatisfiableSeam",
			gate:       "check-seam-implementers.sh",
			file:       "core/agentgraph/seams.go",
			append: "\n" +
				"// zzGateProbeUniqueParam and ZzGateProbeUnsatisfiableSeam are planted by\n" +
				"// gates_can_fail_test.go's check-seam-implementers.sh proof and removed\n" +
				"// after the test runs.\n" +
				"type zzGateProbeUniqueParam struct{}\n\n" +
				"type ZzGateProbeUnsatisfiableSeam interface {\n" +
				"\tZzGateProbeMethod(zzGateProbeUniqueParam) error\n" +
				"}\n",
		},
		{
			// entry-points-and-crash-reporting-01PMZD13 UNIT-5.
			// check-csp.sh's FIRST EVER planted-violation proof (spec §5).
			// This gate reads a BUILT artifact (frontend/dist/index.html),
			// which the test harness does not produce, so the case plants
			// a throwaway fixture HTML file with a weakened CSP and points
			// the gate at it via CSP_CHECK_DIST_HTML / CSP_CHECK_MODE —
			// env overrides added in this same unit, mirroring the
			// UPGRADE_SNAPSHOTS_BASE_REF precedent, so the SAME comparison
			// the gate runs against a real build runs here too.
			name:       "csp/unsafe-eval-in-script-src",
			wantOutput: "script-src contains unsafe-eval",
			gate:       "check-csp.sh",
			file:       "scripts/ci/testdata/zz_gate_probe_csp/index.html",
			content: "<!doctype html>\n<html><head>\n" +
				"<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; " +
				"connect-src 'none'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
				"img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; " +
				"frame-ancestors 'none'; object-src 'none'\">\n</head><body></body></html>\n",
			env: map[string]string{
				"CSP_CHECK_DIST_HTML": "scripts/ci/testdata/zz_gate_probe_csp/index.html",
				"CSP_CHECK_MODE":      "desktop",
			},
		},
		{
			// model-authored-graphs-01PMGA01 UNIT-8. UNIT-2 stopped the
			// manager persisting graphs the validator rejects and UNIT-4
			// made graph writes ask Cedar — both inside Manager.saveGraph.
			// Neither survives a SECOND writer: an os.WriteFile against the
			// library bypasses the validator AND the consent gate, and every
			// existing test still passes because they all drive saveGraph.
			// That is what this gate exists for, and this plants exactly it.
			name: "graph-write-paths/second-writer-bypasses-validation",
			// The probe's own file, not the gate's "[graph-write-paths]"
			// label — the label also prefixes "no production .go files" and
			// "saveGraph not found", so matching it would let a gate that
			// is simply pointed at the wrong package satisfy this proof.
			wantOutput: "zz_gate_probe.go",
			gate:       "check-graph-write-paths.sh",
			file:       "core/rpc/views/agentgraph/zz_gate_probe.go",
			content: "package agentgraph\n\n" +
				"import \"os\"\n\n" +
				"func zzGateProbeSecondWriter(path string, b []byte) error {\n" +
				"\treturn os." + "WriteFile(path, b, 0o644)\n" +
				"}\n",
		},
		{
			// audit-that-tells-the-truth-01PMZA10 WP12 (G-3, new class). An
			// external-content FTS5 table with an AFTER INSERT sync trigger
			// and no AFTER DELETE trigger (and no direct 'delete'-command
			// sync) is the exact defect events_fts shipped with at 0001 and
			// migration 0007 corrected — and the same class sessions/0335
			// exists to fix for messages_fts. Plants a fresh table+trigger
			// pair reproducing only the AFTER INSERT half.
			name: "fts-sync/external-content-table-no-delete-sync",
			// The probe table's own name, not the gate's "[fts-sync]" label
			// — the label also prefixes "checkftssync failed to build",
			// which would let a compile-broken checker satisfy this proof.
			wantOutput: "zz_probe_fts",
			gate:       "check-fts-sync.sh",
			file:       "core/rpc/views/zzgateprobe/migration_probe.sql",
			content: "CREATE VIRTUAL TABLE IF NOT EXISTS zz_probe_fts USING fts5(\n" +
				"    payload,\n" +
				"    content='zz_probe_table',\n" +
				"    content_rowid='rowid'\n" +
				");\n\n" +
				"CREATE TRIGGER IF NOT EXISTS zz_probe_ai AFTER INSERT ON zz_probe_table BEGIN\n" +
				"    INSERT INTO zz_probe_fts(rowid, payload) VALUES (new.rowid, new.payload);\n" +
				"END;\n",
		},
		{
			// THE CLASS CI COULD NOT SEE. An independent review of PR #300
			// broke v1 of this gate against the real production file: move
			// the write OUT of saveGraph into an unvalidated sibling method
			// and v1 reported OK — the package still held exactly one
			// mutator, and its bare line-number comparison still "passed".
			// The gate reported clean for precisely the bypass its own
			// docstring warns about.
			//
			// The case above only ever planted an ADDITIVE second writer in
			// a NEW file, so this table could not see the hole either. This
			// plants a sibling method in manager.go ITSELF — same file,
			// outside saveGraph's line range — which is the shape a real
			// refactor produces.
			name:       "graph-write-paths/write-moved-out-of-savegraph",
			wantOutput: "outside Manager.saveGraph",
			gate:       "check-graph-write-paths.sh",
			file:       "core/rpc/views/agentgraph/manager.go",
			append: "\n\nfunc (m *Manager) zzGateProbeBypassPersist(path string, b []byte) error {\n" +
				"\treturn os." + "WriteFile(path, b, 0o644)\n" +
				"}\n",
		},
		{
			// served-mode-is-a-real-mode-01PMZ707 WP02 (I15, forward
			// direction), CLAUDE.md's gate-extension rule + spec §5.2
			// deliverable 4. A new exported Bindings method with no serve
			// dispatch case and no allowlist entry — the exact shape 419
			// real desktop bindings were in before this WP, invisible to
			// every PR because the pre-promotion script exited 0
			// unconditionally. GATE is the new default (no env override
			// needed) — this case is also the proof the default itself
			// gates, not just --gate/SERVE_DRIFT_GATE=1 opted in by hand.
			name:       "serve-dispatch-drift/forward-unallowlisted-binding",
			wantOutput: "Zz_Injected",
			gate:       "check-serve-dispatch-drift.sh",
			file:       "core/rpc/bindings.go",
			append:     "\nfunc (bnd *Bindings) Zz_Injected() {}\n",
		},
		{
			// served-mode-is-a-real-mode-01PMZ707 WP07, AC-717 + CLAUDE.md's
			// gate-extension rule. check-serve-dispatch-drift.sh's
			// read_allowlist() strips every comment and treats the file as
			// a flat name list — it has never known about classification,
			// so a WP07 that reclassified 416 entries and relied on that
			// gate alone would have shipped a taxonomy nothing enforces.
			// This plants an entry under an untriaged reason paragraph that
			// names neither a date nor an owner — the exact shape 419
			// entries were in before this WP, now caught before promotion
			// rather than ageing silently the way the file's own prior
			// bulk note did.
			name:       "serve-gap-classification/untriaged-entry-missing-date-and-owner",
			wantOutput: "Zz_Injected",
			gate:       "check-serve-gap-classification.sh",
			file:       "scripts/ci/allowlists/i15-serve-dispatch-gap.txt",
			append:     "# No date or owner mentioned anywhere in this reason.\n\"Zz_Injected\"\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		// Not parallel: these mutate the working tree.
		t.Run(tc.name, func(t *testing.T) {
			var cleanup func()
			if tc.file != "" {
				full := filepath.Join(root, tc.file)
				cleanup = plant(t, full, tc.content, tc.append)
				defer cleanup()
			}
			if tc.file2 != "" {
				full2 := filepath.Join(root, tc.file2)
				cleanup2 := plant(t, full2, tc.content2, tc.append2)
				defer cleanup2()
			}

			violationDesc := tc.file
			if violationDesc == "" {
				// An ABSENCE-shaped violation (e.g. a missing snapshot
				// directory) has no file to name — describe it by env
				// instead so the failure message still says what was
				// actually varied.
				violationDesc = fmt.Sprintf("env %v", tc.env)
			}

			code, out := runGateEnv(t, tc.gate, root, tc.env)
			if code == 0 {
				t.Fatalf("%s exited 0 with a planted violation (%s) — the gate cannot fail.\noutput:\n%s",
					tc.gate, violationDesc, out)
			}
			// A non-zero exit alone is a weak proof: a gate that is
			// BROKEN (fails on every run, planted violation or not)
			// satisfies it. check-tests-are-hermetic.sh shipped in
			// exactly that state and this table passed anyway
			// (upgrade-path-coverage-01PMUG01 WP05 review, 2026-08-18).
			// Where a case names the message the gate is supposed to
			// produce, require it, so the proof is about THIS violation
			// rather than about the gate exiting non-zero for any reason.
			if tc.wantOutput != "" && !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("%s failed with a planted violation (%s), but its output does not mention %q — "+
					"it may be failing for an unrelated reason.\noutput:\n%s",
					tc.gate, violationDesc, tc.wantOutput, out)
			}
		})
	}
}

// plant writes (or appends to) a file and returns a restore func.
func plant(t *testing.T, full, content, appendText string) func() {
	t.Helper()

	if appendText != "" {
		orig, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("reading %s: %v", full, err)
		}
		if err := os.WriteFile(full, append(orig, []byte(appendText)...), 0o644); err != nil {
			t.Fatalf("appending to %s: %v", full, err)
		}
		return func() {
			if err := os.WriteFile(full, orig, 0o644); err != nil {
				t.Errorf("restoring %s: %v — WORKING TREE IS DIRTY", full, err)
			}
		}
	}

	if _, err := os.Stat(full); err == nil {
		t.Fatalf("probe file %s already exists; refusing to clobber it", full)
	}
	// Some probes live in a package that does not exist yet (the
	// unregistered-builtin-tool case needs a fresh directory under
	// core/tools/). Track whether we created it so cleanup can undo it —
	// git ignores empty directories, but a leftover one confuses the next
	// run's "refusing to clobber" guard less than a leftover file would.
	dir := filepath.Dir(full)
	createdDir := ""
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
		createdDir = dir
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", full, err)
	}
	return func() {
		if err := os.Remove(full); err != nil {
			t.Errorf("removing %s: %v — WORKING TREE IS DIRTY", full, err)
		}
		if createdDir != "" {
			if err := os.Remove(createdDir); err != nil {
				t.Errorf("removing %s: %v — WORKING TREE IS DIRTY", createdDir, err)
			}
		}
	}
}

// TestToolContainmentUnconditionalGate_PlantedConditionalWrapperFails is
// the planted-violation proof for
// check-tool-containment-unconditional.sh (AC-015,
// harness-self-attach-01PMHS01 UNIT-4). The shared plant() helper above
// only supports appending to, or creating, a file — this gate's defect
// class is specifically about an EXISTING line's indentation (nesting
// the merged-resolver construction back inside a conditional), which
// append cannot express, so this test does its own
// read-mutate-restore cycle on core/rpc/api.go directly rather than
// going through the cases table.
func TestToolContainmentUnconditionalGate_PlantedConditionalWrapperFails(t *testing.T) {
	root := repoRoot(t)
	apiPath := filepath.Join(root, "core", "rpc", "api.go")

	orig, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatalf("reading %s: %v", apiPath, err)
	}

	const target = "\tperms := toolloop.NewMergedResolver(staticPerms, sessionArm)\n"
	if !strings.Contains(string(orig), target) {
		t.Fatalf("expected line not found in api.go — the UNIT-4 wire may have moved; "+
			"update this test and the gate together:\n%q", target)
	}

	// Reproduce the pre-UNIT-4 shape: the construction nested one level
	// deeper inside a conditional. Still assigns via `perms := ...` — the
	// gate must catch the INDENTATION change (nesting), not merely a
	// renamed variable.
	mutated := "\tif c != nil && c.DataDir() != \"\" {\n" +
		"\t\tperms := toolloop.NewMergedResolver(staticPerms, sessionArm)\n" +
		"\t\t_ = perms\n" +
		"\t}\n" +
		"\tvar perms toolloop.PermissionResolver\n"
	newContent := strings.Replace(string(orig), target, mutated, 1)

	if err := os.WriteFile(apiPath, []byte(newContent), 0o644); err != nil {
		t.Fatalf("writing mutated api.go: %v", err)
	}
	defer func() {
		if err := os.WriteFile(apiPath, orig, 0o644); err != nil {
			t.Errorf("restoring api.go: %v — WORKING TREE IS DIRTY", err)
		}
	}()

	code, out := runGate(t, "check-tool-containment-unconditional.sh", root)
	if code == 0 {
		t.Fatalf("check-tool-containment-unconditional.sh exited 0 with the merged resolver "+
			"nested back inside a conditional — the gate cannot fail.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "conditional-assignment shape") {
		t.Fatalf("gate failed, but its output does not mention the expected defect class "+
			"(a broken/unrelated failure would still satisfy a bare non-zero exit code):\n%s", out)
	}
}

// TestAuditStoreBeforeRetentionGate_PlantedStoreRemovalFails is the
// planted-violation proof for check-audit-store-before-retention.sh (G-1,
// audit-that-tells-the-truth-01PMZA10 WP12). The shared plant() helper
// above only supports appending to, or creating, a file — this gate's
// defect class is specifically about REMOVING an existing option from an
// existing call (`audit.WithStore(auditStore)` dropped from the
// auditOpts literal in core/rpc/api.go while
// `eventlog.NewLocalRetentionScheduler(` stays present, exactly the
// shape a future refactor that "simplifies" the option list without
// touching the sweeper could produce), which append cannot express — so
// this test does its own read-mutate-restore cycle on core/rpc/api.go
// directly, mirroring TestToolContainmentUnconditionalGate_
// PlantedConditionalWrapperFails above.
func TestAuditStoreBeforeRetentionGate_PlantedStoreRemovalFails(t *testing.T) {
	root := repoRoot(t)
	apiPath := filepath.Join(root, "core", "rpc", "api.go")

	orig, err := os.ReadFile(apiPath)
	if err != nil {
		t.Fatalf("reading %s: %v", apiPath, err)
	}

	const target = "auditOpts := []audit.Option{audit.WithSubscriber(a.broker), audit.WithGate(auditGate), audit.WithStore(auditStore)}"
	if !strings.Contains(string(orig), target) {
		t.Fatalf("expected line not found in api.go — the UNIT-4 wire may have moved; "+
			"update this test and the gate together:\n%q", target)
	}
	// Drop ONLY the WithStore option — the sweep construction site
	// (eventlog.NewLocalRetentionScheduler) and the frontend panel copy
	// both stay untouched, reproducing exactly the ordering violation
	// G-1 exists to catch: claims outliving the store that backs them.
	mutated := "auditOpts := []audit.Option{audit.WithSubscriber(a.broker), audit.WithGate(auditGate)}"
	newContent := strings.Replace(string(orig), target, mutated, 1)

	if err := os.WriteFile(apiPath, []byte(newContent), 0o644); err != nil {
		t.Fatalf("writing mutated api.go: %v", err)
	}
	defer func() {
		if err := os.WriteFile(apiPath, orig, 0o644); err != nil {
			t.Errorf("restoring api.go: %v — WORKING TREE IS DIRTY", err)
		}
	}()

	code, out := runGate(t, "check-audit-store-before-retention.sh", root)
	if code == 0 {
		t.Fatalf("check-audit-store-before-retention.sh exited 0 with audit.WithStore( removed "+
			"while a sweep construction site remains — the gate cannot fail.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "NewLocalRetentionScheduler(") {
		t.Fatalf("gate failed, but its output does not mention the expected defect "+
			"(a broken/unrelated failure would still satisfy a bare non-zero exit code):\n%s", out)
	}
}

// TestBundleVerifyOrderingGate_PlantedNilSignatureFires is the
// planted-violation proof for check-bundle-verify-ordering.sh (G-1,
// bundle-download-and-verify-01PMZ909 UNIT-9, spec §2 / §7 G-1). The
// shared plant() helper's append-or-create mode cannot express this
// defect class — an EXISTING field literal (`Signature: req.SignatureBytes,`)
// reverting to the pre-UNIT-2 shape (`Signature: nil,`) while the
// verify call site in core/rpc/views/bundle/impl.go stays present — so
// this test does its own read-mutate-restore cycle on
// core/trust/bundleadapter.go directly, mirroring
// TestToolContainmentUnconditionalGate_PlantedConditionalWrapperFails
// and TestAuditStoreBeforeRetentionGate_PlantedStoreRemovalFails above.
func TestBundleVerifyOrderingGate_PlantedNilSignatureFires(t *testing.T) {
	root := repoRoot(t)
	adapterPath := filepath.Join(root, "core", "trust", "bundleadapter.go")

	orig, err := os.ReadFile(adapterPath)
	if err != nil {
		t.Fatalf("reading %s: %v", adapterPath, err)
	}

	const target = "Signature: req.SignatureBytes,"
	if !strings.Contains(string(orig), target) {
		t.Fatalf("expected line not found in bundleadapter.go — the UNIT-2 wire may have moved; "+
			"update this test and the gate together:\n%q", target)
	}
	// Reproduce the exact pre-UNIT-2 defect (spec §1.5): the envelope's
	// Signature field reverts to a hardcoded nil while
	// core/rpc/views/bundle/impl.go still calls VerifyManifestSignatures
	// — the ordering violation this gate exists to catch.
	mutated := "Signature: nil,"
	newContent := strings.Replace(string(orig), target, mutated, 1)

	if err := os.WriteFile(adapterPath, []byte(newContent), 0o644); err != nil {
		t.Fatalf("writing mutated bundleadapter.go: %v", err)
	}
	defer func() {
		if err := os.WriteFile(adapterPath, orig, 0o644); err != nil {
			t.Errorf("restoring bundleadapter.go: %v — WORKING TREE IS DIRTY", err)
		}
	}()

	code, out := runGate(t, "check-bundle-verify-ordering.sh", root)
	if code == 0 {
		t.Fatalf("check-bundle-verify-ordering.sh exited 0 with Signature: nil, restored "+
			"while the impl.go call site remains — the gate cannot fail.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "Signature: nil,") {
		t.Fatalf("gate failed, but its output does not mention the expected defect "+
			"(a broken/unrelated failure would still satisfy a bare non-zero exit code):\n%s", out)
	}
}

// TestBundleChannelKindsSyncGate_PlantedDriftFires is the planted-violation
// proof for check-bundle-channel-kinds-sync.sh (bundle-download-and-
// verify-01PMZ909 UNIT-8/UNIT-9). BundlesView.vue's channel picker
// hardcodes CHANNEL_KINDS rather than querying a
// Bundle_ListChannelKinds RPC (none exists — adding one needs a
// frontend/wailsjs/** regen this campaign deliberately does not run);
// this gate is what keeps that hardcoded list from drifting from the
// backend's registered channels.Registry factories. Removes the
// http_mirror entry from CHANNEL_KINDS while
// core/bundle/channels/http/http.go's `const Kind = "http_mirror"`
// stays present — the shape "a real channel package a user cannot
// select from the UI".
func TestBundleChannelKindsSyncGate_PlantedDriftFires(t *testing.T) {
	root := repoRoot(t)
	vuePath := filepath.Join(root, "frontend", "src", "views", "bundles", "BundlesView.vue")

	orig, err := os.ReadFile(vuePath)
	if err != nil {
		t.Fatalf("reading %s: %v", vuePath, err)
	}

	const target = `{ kind: 'http_mirror', label: 'http_mirror — a URL served over HTTP(S)', field: 'url' },`
	if !strings.Contains(string(orig), target) {
		t.Fatalf("expected CHANNEL_KINDS entry not found in BundlesView.vue — the UNIT-8 "+
			"picker may have moved; update this test and the gate together:\n%q", target)
	}
	newContent := strings.Replace(string(orig), target, "", 1)

	if err := os.WriteFile(vuePath, []byte(newContent), 0o644); err != nil {
		t.Fatalf("writing mutated BundlesView.vue: %v", err)
	}
	defer func() {
		if err := os.WriteFile(vuePath, orig, 0o644); err != nil {
			t.Errorf("restoring BundlesView.vue: %v — WORKING TREE IS DIRTY", err)
		}
	}()

	code, out := runGate(t, "check-bundle-channel-kinds-sync.sh", root)
	if code == 0 {
		t.Fatalf("check-bundle-channel-kinds-sync.sh exited 0 with http_mirror removed from "+
			"the frontend picker while the backend package remains — the gate cannot fail.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "http_mirror") {
		t.Fatalf("gate failed, but its output does not mention the expected defect "+
			"(a broken/unrelated failure would still satisfy a bare non-zero exit code):\n%s", out)
	}
}

// TestServeDispatchDriftGate_PlantedReverseCaseFires is the reverse-direction
// planted-violation proof for check-serve-dispatch-drift.sh (I15,
// served-mode-is-a-real-mode-01PMZ707 WP02, spec §5.2 deliverable 4). The
// shared plant() helper's append mode only supports appending at the END of
// a file, which for this gate's forward direction is fine (a new top-level
// func in bindings.go) but would leave server.go with a bare `case` clause
// outside any switch — syntactically foreign Go, sufficient for THIS gate
// (a pure grep, not a build) but a needlessly fragile plant when the real
// switch is one string-replace away. This does its own read-mutate-restore
// cycle, inserting the planted case INSIDE the dispatch switch, immediately
// before its `default:` arm, mirroring
// TestToolContainmentUnconditionalGate_PlantedConditionalWrapperFails and
// TestAuditStoreBeforeRetentionGate_PlantedStoreRemovalFails above.
//
// The case name (Zz_Injected) intentionally matches the forward-direction
// case's planted binding name in the table above — this test proves the
// REVERSE direction (a dispatch case with no Bindings method) independently
// of that one; the two never run with both plants live at once, so there is
// no real Zz_Injected binding for this case to (correctly) match against.
func TestServeDispatchDriftGate_PlantedReverseCaseFires(t *testing.T) {
	root := repoRoot(t)
	serverPath := filepath.Join(root, "core", "serve", "server.go")

	orig, err := os.ReadFile(serverPath)
	if err != nil {
		t.Fatalf("reading %s: %v", serverPath, err)
	}

	const target = "\tdefault:\n\t\t// FR-005: no fake success"
	if !strings.Contains(string(orig), target) {
		t.Fatalf("expected dispatch-switch default arm not found — server.go's dispatch switch "+
			"may have moved; update this test and the gate together:\n%q", target)
	}
	// A case with no Bindings method of the same name — the exact shape a
	// typo'd case string, or an orphaned served-only method whose binding
	// was since deleted, produces.
	mutated := "\tcase \"Zz_Injected\":\n\t\treturn nil, nil\n\n" + target
	newContent := strings.Replace(string(orig), target, mutated, 1)

	if err := os.WriteFile(serverPath, []byte(newContent), 0o644); err != nil {
		t.Fatalf("writing mutated server.go: %v", err)
	}
	defer func() {
		if err := os.WriteFile(serverPath, orig, 0o644); err != nil {
			t.Errorf("restoring server.go: %v — WORKING TREE IS DIRTY", err)
		}
	}()

	code, out := runGate(t, "check-serve-dispatch-drift.sh", root)
	if code == 0 {
		t.Fatalf("check-serve-dispatch-drift.sh exited 0 with a dispatch case (Zz_Injected) that "+
			"has no matching Bindings method and no reverse-allowlist entry — the gate cannot fail "+
			"on the reverse direction.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "Zz_Injected") {
		t.Fatalf("gate failed, but its output does not mention the planted case name "+
			"(a broken/unrelated failure would still satisfy a bare non-zero exit code):\n%s", out)
	}
	if !strings.Contains(out, "REVERSE") {
		t.Fatalf("gate failed, but not on the REVERSE-direction check — the forward-direction "+
			"check may have coincidentally also failed, which would not prove this direction works:\n%s", out)
	}
}

// TestCSSTokensGate_SameVerdictFromAnyCWD covers the .mjs gate that started
// this sweep. It resolved `src` against process.cwd(), so under pr.yml
// (working-directory: repo root) it threw ENOENT on every run — an uncaught
// Node stack trace swallowed by `continue-on-error: true`. From frontend/ it
// worked. Same script, two answers, and CI only ever saw the broken one.
func TestCSSTokensGate_SameVerdictFromAnyCWD(t *testing.T) {
	// NOT t.Parallel() — same reason as
	// TestGates_VerdictIsIndependentOfWorkingDirectory: it reads a tree
	// that TestGates_PlantedViolationFires mutates.
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "ci", "check-css-tokens.mjs")

	run := func(dir string) (int, string) {
		cmd := exec.Command("node", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return exitErr.ExitCode(), string(out)
		}
		t.Fatalf("running check-css-tokens.mjs: %v (%s)", err, out)
		return -1, ""
	}

	fromRoot, outRoot := run(root)
	fromFrontend, outFrontend := run(filepath.Join(root, "frontend"))
	fromAway, outAway := run(t.TempDir())

	if fromRoot != fromFrontend || fromRoot != fromAway {
		t.Fatalf("check-css-tokens.mjs verdict depends on cwd: root=%d frontend=%d elsewhere=%d\n"+
			"root:\n%s\nfrontend:\n%s\nelsewhere:\n%s",
			fromRoot, fromFrontend, fromAway, outRoot, outFrontend, outAway)
	}

	// An ENOENT stack trace is not a verdict. If the gate dies this way again,
	// say so in the failure message rather than letting the exit code alone
	// stand in for a real result.
	if strings.Contains(outRoot, "ENOENT") {
		t.Fatalf("check-css-tokens.mjs crashed with ENOENT instead of reporting a verdict:\n%s", outRoot)
	}
}

// TestCheckDeclaredOutputPorts_IgnoresPortsGenWrites is I16's true
// negative (approval-node-01PMZC12 UNIT-9, AC-11): a port written ONLY
// through ports_gen.go's generated ToPortValues() — which writes every
// declared port of a kind unconditionally, whether or not the real
// executor ever populates the field — must NOT be treated as evidence
// of a real writer. Without this the gate would land as a repo-wide
// false-positive generator, since ports_gen.go trivially "writes"
// every port that exists.
//
// Plants a fake write in ports_gen.go (excluded from the scan by
// filename) alongside a manifest declaring the SAME port with no real
// executor writer, and asserts the gate still reports it — proving the
// exclusion is load-bearing, not decorative. Not in the table above:
// it needs two coordinated plants (manifest + ports_gen.go), which the
// single-file plant() helper does not support.
func TestCheckDeclaredOutputPorts_IgnoresPortsGenWrites(t *testing.T) {
	root := repoRoot(t)

	manifestPath := filepath.Join(root, "core/agentgraph/nodes/manifests/zz_gate_probe_gen.yaml")
	manifestContent := "schema_version: \"1\"\nmanifest_version: \"1.0.0\"\nid: zz_gate_probe_gen\nextends: write\ndisplay_name: ZZ Gate Probe Gen\ndescription: \"I16 true-negative probe: a port written only via ports_gen.go must not count.\"\nexecutor: agentgraph.ExecZzGateProbeGen\ndispatch: graph\nports:\n  inputs:\n    - { name: in, type: any }\n  outputs:\n    - { name: zz_gate_probe_gen_port, type: any }\nbudget: none\n"
	cleanupManifest := plant(t, manifestPath, manifestContent, "")
	defer cleanupManifest()

	genPath := filepath.Join(root, "core/agentgraph/ports_gen.go")
	// Shaped exactly like a real ToPortValues assignment
	// (`pv["k"] = o.K`) so a naive grep for the write pattern would
	// count it — the whole point of excluding this file by name.
	genAppend := "\n// zzGateProbeGenGeneratedOnlyWrite exists ONLY to prove\n" +
		"// check-declared-output-ports.sh (I16) ignores ports_gen.go —\n" +
		"// see TestCheckDeclaredOutputPorts_IgnoresPortsGenWrites.\n" +
		"func zzGateProbeGenGeneratedOnlyWrite(pv PortValues) {\n" +
		"\tpv[\"zz_gate_probe_gen_port\"] = nil\n" +
		"}\n"
	cleanupGen := plant(t, genPath, "", genAppend)
	defer cleanupGen()

	code, out := runGate(t, "check-declared-output-ports.sh", root)
	if code == 0 {
		t.Fatalf("check-declared-output-ports.sh exited 0 — a port written only in ports_gen.go was accepted as having a real writer.\noutput:\n%s", out)
	}
	if !strings.Contains(out, "zz_gate_probe_gen_port") {
		t.Fatalf("check-declared-output-ports.sh failed, but not for the planted port — output does not mention zz_gate_probe_gen_port.\noutput:\n%s", out)
	}
}

// TestNoUnwiredGates_StaleCheckIsPackageAware pins the fix for a
// false-positive class found on 2026-08-21 during v0.70.0 integration.
//
// The stale-entry check matched a BARE symbol name anywhere in the tree.
// The allowlist carries `core/credstore.WithCedarGate` — genuinely
// unwired, with a written justification. approval-node-01PMZC12 added an
// identically named `graphview.WithCedarGate` in a different package and
// wired it, and the gate declared the credstore entry stale, demanding
// deletion of a justification that was still true.
//
// That is the worst failure mode a gate of this kind has: it does not
// merely miss something, it actively instructs you to delete the record
// of a real gap. Deleting the line would have made credstore's unwired
// gate invisible to the very check that exists to track it.
//
// Two directions, both asserted:
//   - a same-named symbol in ANOTHER package must NOT mark the entry
//     stale (the false positive)
//   - a real caller of the entry's OWN package MUST still mark it stale
//     (the true positive the fix must not trade away)
func TestNoUnwiredGates_StaleCheckIsPackageAware(t *testing.T) {
	root := repoRoot(t)
	const gate = "check-no-unwired-gates.sh"

	// Direction 1: the real tree already contains graphview.WithCedarGate,
	// wired at core/rpc/api.go, while core/credstore.WithCedarGate stays
	// allowlisted. A package-blind check fails here.
	if code, out := runGate(t, gate, root); code != 0 {
		t.Fatalf("gate must pass on the real tree — a same-named symbol in another package is not staleness (exit %d).\n%s", code, out)
	}

	// Direction 2: plant a genuine in-package caller. The entry IS stale
	// now and the gate must say so; otherwise the fix bought a false
	// negative.
	probe := filepath.Join(root, "core/credstore/zz_stale_probe.go")
	content := "package credstore\n\n" +
		"import \"github.com/kameas-ai/kenaz-harness/core/policy/cedar\"\n\n" +
		"func zzStaleProbeUsesGate(g cedar.Gate) StoreOption { return WithCedarGate(g, nil) }\n"
	cleanup := plant(t, probe, content, "")
	defer cleanup()

	code, out := runGate(t, gate, root)
	if code == 0 {
		t.Fatalf("gate passed with a real in-package caller of an allowlisted symbol — the stale check cannot fail.\n%s", out)
	}
	if !strings.Contains(out, "STALE") || !strings.Contains(out, "credstore.WithCedarGate") {
		t.Fatalf("gate failed, but not with the STALE diagnosis for credstore.WithCedarGate:\n%s", out)
	}
}
