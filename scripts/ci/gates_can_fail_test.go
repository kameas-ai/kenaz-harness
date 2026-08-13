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
// NOTE — THIS TEST DOES NOT RUN IN CI YET. pr.yml's test-go step scopes to
// `./core/... ./cmd/harness-vm/...`, which excludes ./scripts/... along with
// the root package, cmd/kenaz-updater and cmd/mcpsubcmd — 18 test functions
// that never execute. Add `./scripts/...` (at minimum) to that step. It is a
// one-line change, deliberately not made here because #279 is concurrently
// editing .github/workflows/pr.yml.

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
			name: "slog-privacy/typed-attr-constructor",
			gate: "check-no-user-content-in-slog.sh",
			file: "core/rpc/zz_gate_probe.go",
			// slog.String(...) is how a multi-line log call spells its keys.
			content: "package rpc\n\nvar zzGateProbe = slog.String(\"Prompt\", p)\n",
		},
	}

	for _, tc := range cases {
		tc := tc
		// Not parallel: these mutate the working tree.
		t.Run(tc.name, func(t *testing.T) {
			full := filepath.Join(root, tc.file)
			cleanup := plant(t, full, tc.content, tc.append)
			defer cleanup()

			code, out := runGate(t, tc.gate, root)
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
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", full, err)
	}
	return func() {
		if err := os.Remove(full); err != nil {
			t.Errorf("removing %s: %v — WORKING TREE IS DIRTY", full, err)
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
