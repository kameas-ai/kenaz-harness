package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// filesystemFullRecommendedTemplatePath is the shipped template
// `filesystem-full-recommended.cedar` offers as the hardening template
// for the `filesystem-full` MCP recipe. The template's own header
// promises the model "CANNOT touch your secrets, credentials, or .git
// internals through this policy."
//
// Before trust-surfaces-that-fire-01PMZ202 WP18, every forbid clause in
// the file was written against action `file_read` / `file_write` —
// which are evaluated ONLY by the agentgraph state executor's
// `read_file` / `write_file` node kinds (core/policy/cedar/hooks.go
// CheckFileRead / CheckFileWrite), not by the gate the recipe installs
// this template for. The gate real filesystem tool calls go through is
// core/tools/fs.Gate, which evaluates `read_filesystem` /
// `write_filesystem` against the FilesystemOp resource family
// (Gate.Evaluate, this package). So every rule in the template resolved
// cedar.NotApplicable, and core/policy/cedar/hooks.go's enforce() maps
// NotApplicable to nil — the same as an explicit Allow. The user saw a
// green "hardened" affordance over a policy that matched nothing.
const filesystemFullRecommendedTemplatePath = "../../policy/cedar/policies/filesystem-full-recommended.cedar"

// installFilesystemFullTemplate copies the REAL, shipped template file
// (not a rewritten-in-the-test copy) into a fresh DataDir's policy/
// directory, exactly the way CedarPolicy_InstallTemplate does for a
// user who accepts the filesystem-full recipe's recommended hardening.
// Returns the DataDir.
func installFilesystemFullTemplate(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filesystemFullRecommendedTemplatePath)
	if err != nil {
		t.Fatalf("reading shipped template at %s: %v", filesystemFullRecommendedTemplatePath, err)
	}

	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", policyDir, err)
	}
	dst := filepath.Join(policyDir, "filesystem-full-recommended.cedar")
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", dst, err)
	}
	return dataDir
}

// newRealFilesystemFullEngine builds a *cedar.Engine the way
// core/rpc/builtins_wiring.go:502 assembles the production fs gate's
// engine: real engine, embedded defaults + every file under DataDir's
// policy/ (which is where InstallTemplate places the recommended
// template), loaded from disk. No cedar.AllowAll{} stand-in.
func newRealFilesystemFullEngine(t *testing.T, dataDir string) *cedar.Engine {
	t.Helper()
	engine, err := cedar.NewEngine(cedar.Options{
		DataDir:         dataDir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

// failIfPromptedPrompter fails the test the instant Prompt is called.
// Gate.Evaluate only reaches the Prompter on a cedar.NotApplicable
// outcome (see the `case cedar.NotApplicable:` branch in gate.go); the
// cedar.Allow and cedar.Deny branches both return immediately without
// ever consulting it. Using this Prompter for the deny cases proves the
// denial comes from the Cedar policy itself — an explicit forbid match —
// and not from a fallback prompt refusal, which is the exact ambiguity
// core/policy/cedar/hooks.go's enforce() collapses for the OTHER
// (agentgraph) gate path and which this test must not reproduce here.
type failIfPromptedPrompter struct{ t *testing.T }

func (p failIfPromptedPrompter) Prompt(_ context.Context, s PromptSurface) (PromptResponse, error) {
	p.t.Helper()
	p.t.Fatalf("Prompter invoked for %q (op=%s) — want an explicit cedar.Deny from the installed template, not a fallthrough to the interactive prompt", s.CanonicalPath, s.Op)
	return PromptDeny, nil
}

// TestFilesystemFullRecommendedTemplate_DeniesSensitivePaths is
// trust-surfaces-that-fire-01PMZ202 WP18's AC-12, driven through the
// REAL production gate (core/tools/fs.Gate) with the REAL shipped
// template installed the way a user's accepted recipe recommendation
// installs it. One case per forbid family in the file.
//
// Mutation (falsification): revert the action retarget in
// filesystem-full-recommended.cedar back to `Action::"file_read"` /
// `Action::"file_write"`. Every case below must fail — the outcome
// becomes cedar.NotApplicable (verified separately: NotApplicable would
// have driven the fallthrough into failIfPromptedPrompter, which itself
// would fail the test with a different, more informative message).
func TestFilesystemFullRecommendedTemplate_DeniesSensitivePaths(t *testing.T) {
	t.Parallel()

	dataDir := installFilesystemFullTemplate(t)
	engine := newRealFilesystemFullEngine(t, dataDir)

	cases := []struct {
		name string
		op   Op
		path string
	}{
		{"ssh_key_read", OpRead, "/home/tester/.ssh/id_rsa"},
		{"aws_credentials_write", OpWrite, "/home/tester/.aws/credentials"},
		{"gnupg_secring_read", OpRead, "/home/tester/.gnupg/secring.gpg"},
		{"gh_hosts_write", OpWrite, "/home/tester/.config/gh/hosts.yml"},
		{"netrc_read", OpRead, "/home/tester/.netrc"},
		{"docker_config_write", OpWrite, "/home/tester/.docker/config.json"},
		{"dotenv_read", OpRead, "/home/tester/project/.env"},
		{"dotenv_production_write", OpWrite, "/home/tester/project/.env.production"},
		{"macos_keychain_read", OpRead, "/home/tester/Library/Keychains/login.keychain-db"},
		{"etc_write", OpWrite, "/etc/hosts"},
		{"usr_write", OpWrite, "/usr/local/bin/evil"},
		{"git_config_write", OpWrite, "/home/tester/project/.git/config"},
		{"harness_data_db_read", OpRead, "/home/tester/.local/share/kenaz-harness/data.db"},
		{"harness_keychain_write", OpWrite, "/home/tester/.local/share/kenaz-harness/keychain.enc"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Each subtest gets its own Prompter bound to its own *testing.T
			// — calling Fatalf on a *testing.T from a different subtest's
			// goroutine trips Go's "may have called FailNow on a parent
			// test" guard and produces a misleading panic in the output.
			gate := NewGate(GateOptions{
				Engine:   engine,
				Prompter: failIfPromptedPrompter{t: t},
			})
			decision, err := gate.Evaluate(context.Background(), tc.op, tc.path)
			if err != nil {
				t.Fatalf("Evaluate(%s, %s): unexpected error: %v", tc.op, tc.path, err)
			}
			if decision.Outcome != cedar.Deny {
				t.Fatalf("Evaluate(%s, %s).Outcome = %s, want %s (reason=%q) — the template did not deny this sensitive path", tc.op, tc.path, decision.Outcome, cedar.Deny, decision.Reason)
			}
		})
	}
}

// TestFilesystemFullRecommendedTemplate_ReadsGitConfigOfficiallyAllowed
// pins one documented exception the header itself calls out: `.git`
// READS stay permitted (only .git WRITES are denied) so the model can
// still learn the remote URL. Uses the same failIfPromptedPrompter as
// the deny test, but now asserting the OPPOSITE — the read must reach
// cedar.NotApplicable (no forbid matches a read) and therefore fall
// through to the prompt, which must NOT be pre-empted by an
// over-broad rewrite of the retargeted rule.
func TestFilesystemFullRecommendedTemplate_ReadsGitConfigOfficiallyAllowed(t *testing.T) {
	t.Parallel()

	dataDir := installFilesystemFullTemplate(t)
	engine := newRealFilesystemFullEngine(t, dataDir)

	prompter := &recordingPrompterAlwaysAllowOnce{}
	gate := NewGate(GateOptions{Engine: engine, Prompter: prompter})

	decision, err := gate.Evaluate(context.Background(), OpRead, "/home/tester/project/.git/config")
	if err != nil {
		t.Fatalf("Evaluate: unexpected error: %v", err)
	}
	if !prompter.called {
		t.Fatalf("expected the read of .git/config to reach the interactive prompt (cedar.NotApplicable) — got a decision without consulting the Prompter, meaning something now denies (or permits) reads of .git/config at the Cedar layer")
	}
	if decision.Outcome != cedar.Allow {
		t.Fatalf("Evaluate.Outcome = %s, want %s", decision.Outcome, cedar.Allow)
	}
}

// recordingPrompterAlwaysAllowOnce records whether it was invoked and
// always grants a one-shot allow. Used to prove a path reached the
// NotApplicable -> prompt branch (rather than being pre-empted by an
// explicit cedar.Deny) and that the gate still permits it end to end.
type recordingPrompterAlwaysAllowOnce struct {
	called bool
}

func (p *recordingPrompterAlwaysAllowOnce) Prompt(_ context.Context, _ PromptSurface) (PromptResponse, error) {
	p.called = true
	return PromptAllowOnce, nil
}

// TestFilesystemFullRecommendedTemplate_OrdinaryPathsStillReachPrompt
// is the paired positive the deny test needs: with the SAME installed
// template, on the SAME real gate, an ordinary project path that
// matches none of the forbid families must NOT be denied by the
// template. It reaches cedar.NotApplicable and falls through to the
// interactive prompt exactly as it would with no template installed —
// proving the fix is a narrow, discriminating retarget of the existing
// rules and not, say, a blanket `forbid (principal, action, resource)`
// with no `when` clause (which would also make the deny test above
// pass, for the wrong reason).
func TestFilesystemFullRecommendedTemplate_OrdinaryPathsStillReachPrompt(t *testing.T) {
	t.Parallel()

	dataDir := installFilesystemFullTemplate(t)
	engine := newRealFilesystemFullEngine(t, dataDir)

	cases := []struct {
		name string
		op   Op
		path string
	}{
		{"readme_read", OpRead, "/home/tester/project/README.md"},
		{"source_file_write", OpWrite, "/home/tester/project/main.go"},
		{"tmp_scratch_write", OpWrite, "/home/tester/project/build/out.txt"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prompter := &recordingPrompterAlwaysAllowOnce{}
			gate := NewGate(GateOptions{Engine: engine, Prompter: prompter})

			decision, err := gate.Evaluate(context.Background(), tc.op, tc.path)
			if err != nil {
				t.Fatalf("Evaluate(%s, %s): unexpected error: %v", tc.op, tc.path, err)
			}
			if !prompter.called {
				t.Fatalf("Evaluate(%s, %s): Prompter never invoked — an ordinary path was denied (or allowed) at the Cedar layer instead of falling through to the interactive prompt; outcome=%s reason=%q", tc.op, tc.path, decision.Outcome, decision.Reason)
			}
			if decision.Outcome != cedar.Allow {
				t.Fatalf("Evaluate(%s, %s).Outcome = %s, want %s", tc.op, tc.path, decision.Outcome, cedar.Allow)
			}
		})
	}
}
