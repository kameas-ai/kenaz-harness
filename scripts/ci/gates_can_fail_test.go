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
	"check-upgrade-snapshots-locked.sh",
	"check-destructive-migration-coverage.sh",
}

// TestGates_VerdictIsIndependentOfWorkingDirectory is the direct regression
// test for the class. A gate invoked from a foreign cwd must reach the same
// conclusion it reaches from the repo root — never a vacuous pass.
func TestGates_VerdictIsIndependentOfWorkingDirectory(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	for _, gate := range cwdSensitiveGates {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
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
		name    string
		gate    string
		file    string // repo-relative path to create, or "" to append
		append  string // when file already exists, append this instead
		content string
		env     map[string]string // extra env vars for this case's runGate call, if any
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
			name: "slog-privacy/typed-attr-constructor",
			gate: "check-no-user-content-in-slog.sh",
			file: "core/rpc/zz_gate_probe.go",
			// slog.String(...) is how a multi-line log call spells its keys.
			content: "package rpc\n\nvar zzGateProbe = slog.String(\"Prompt\", p)\n",
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
			name:   "upgrade-snapshots-locked/byte-mutation",
			gate:   "check-upgrade-snapshots-locked.sh",
			file:   "core/storage/sqlite/testdata/upgrade/v0.63.0/dump.sql",
			append: "-- zzGateProbe: this byte must not be here\n",
			env:    map[string]string{"UPGRADE_SNAPSHOTS_BASE_REF": "HEAD"},
		},
		{
			// upgrade-path-coverage-01PMUG01 WP03 (I14). A migration
			// whose Up() runs DROP TABLE with no populated-table test
			// referencing its ID and no allowlist entry must fail.
			name: "destructive-migration-coverage/uncovered-drop-table",
			gate: "check-destructive-migration-coverage.sh",
			file: "core/rpc/views/zzgateprobe/migration_probe.go",
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
			gate: "check-tests-are-hermetic.sh",
			file: "core/rpc/zz_gate_probe_unsandboxed_settings_test.go",
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
	}

	for _, tc := range cases {
		tc := tc
		// Not parallel: these mutate the working tree.
		t.Run(tc.name, func(t *testing.T) {
			full := filepath.Join(root, tc.file)
			cleanup := plant(t, full, tc.content, tc.append)
			defer cleanup()

			code, out := runGateEnv(t, tc.gate, root, tc.env)
			if code == 0 {
				t.Fatalf("%s exited 0 with a planted violation in %s — the gate cannot fail.\noutput:\n%s",
					tc.gate, tc.file, out)
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

// TestCSSTokensGate_SameVerdictFromAnyCWD covers the .mjs gate that started
// this sweep. It resolved `src` against process.cwd(), so under pr.yml
// (working-directory: repo root) it threw ENOENT on every run — an uncaught
// Node stack trace swallowed by `continue-on-error: true`. From frontend/ it
// worked. Same script, two answers, and CI only ever saw the broken one.
func TestCSSTokensGate_SameVerdictFromAnyCWD(t *testing.T) {
	t.Parallel()
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
