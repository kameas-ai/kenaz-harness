package bash

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	cedargo "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Allow-always snippets are written into real files and loaded by a real
// disk engine (embedded defaults included), exactly as in production.
// Before the fix, writePolicySnippet concatenated the pattern into the
// Cedar literal raw: a newline left it unterminated (the user policy
// failed to load and the engine failed closed for every tool call), and
// a `"` let the pattern close the literal and append its own policy.

func newSnippetTool(t *testing.T) (*Tool, *cedar.Engine, string) {
	t.Helper()
	dataDir := t.TempDir()
	eng, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = eng
		o.DataDir = dataDir
	})
	return tool, eng, dataDir
}

func policyFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, cedar.PolicyDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func bashOutcome(eng *cedar.Engine, pattern string) cedar.Outcome {
	return eng.Evaluate(context.Background(), cedar.UserUID(), cedar.ActionRunBashCommand, cedar.BashCommandUID(pattern), nil).Outcome
}

// assertNoBroadGrant checks that nothing outside the approved pattern
// became allowed: another bash command, an arbitrary tool, a credential.
func assertNoBroadGrant(t *testing.T, eng *cedar.Engine) {
	t.Helper()
	ctx := context.Background()
	if got := bashOutcome(eng, "rm"); got == cedar.Allow {
		t.Errorf("run_bash_command on BashCommand::\"rm\" = Allow; the snippet granted more than its pattern")
	}
	if got := bashOutcome(eng, "y"); got == cedar.Allow {
		t.Errorf("run_bash_command on BashCommand::\"y\" = Allow; the snippet granted more than its pattern")
	}
	tool := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("evil", "exfiltrate"), nil)
	if tool.Outcome == cedar.Allow {
		t.Errorf("use_tool on an arbitrary tool = Allow (matched %q); policy injection", tool.MatchedPolicy)
	}
	cred := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseCredential, cedar.CredentialUID("openai", "provider_call"),
		map[cedargo.String]cedargo.Value{})
	if cred.Outcome == cedar.Allow {
		t.Errorf("use_credential = Allow (matched %q); policy injection", cred.MatchedPolicy)
	}
}

// A pattern that is a single line, carries no control character, and
// still closes the literal and the permit, then adds a blanket permit
// and reopens a literal so the file parses. This is what a model-chosen
// argv[0] can be: filepath.Base keeps quotes, parentheses and spaces.
const injectionNoNewline = `x"); permit(principal, action, resource); permit(principal, action, resource == BashCommand::"y`

func TestAllowAlways_QuoteInjection_GrantsNothingElse(t *testing.T) {
	if p := DerivePattern([]string{"/tmp/" + injectionNoNewline}); p != injectionNoNewline {
		t.Fatalf("precondition: DerivePattern = %q, want the payload verbatim", p)
	}
	tool, eng, _ := newSnippetTool(t)
	tool.writePolicySnippet(injectionNoNewline)

	if err := eng.UserPolicyLoadError(); err != nil {
		t.Fatalf("user policy failed to load after Allow always: %v", err)
	}
	assertNoBroadGrant(t, eng)
	// The grant that was approved still works, for exactly that pattern.
	if got := bashOutcome(eng, injectionNoNewline); got != cedar.Allow {
		t.Errorf("approved pattern outcome = %v, want Allow", got)
	}
}

func TestAllowAlways_NewlineInjection_RefusedEngineHealthy(t *testing.T) {
	for _, pattern := range []string{
		"x\");\npermit(principal, action, resource);\n//",
		"data.db\necho", // the shape of the live incident
		"tab\there",
		"nul\x00byte",
		"esc\x1b[31m",
		"c1\u0085next",
		// Not control characters, but cedar-go v1.6.0 cannot parse them
		// back ("bad unicode rune"): U+FFFD and invalid UTF-8.
		"rep\uFFFDlacement",
		"bad\xffutf8",
	} {
		t.Run(strconv.QuoteToASCII(pattern), func(t *testing.T) {
			tool, eng, dataDir := newSnippetTool(t)
			tool.writePolicySnippet(pattern)

			if files := policyFiles(t, dataDir); len(files) != 0 {
				t.Errorf("control-character pattern was persisted: %v", files)
			}
			if err := eng.UserPolicyLoadError(); err != nil {
				t.Fatalf("engine unhealthy after Allow always: %v", err)
			}
			assertNoBroadGrant(t, eng)

			// The engine still evaluates ordinary grants normally.
			tool.writePolicySnippet("git status")
			if got := bashOutcome(eng, "git status"); got != cedar.Allow {
				t.Errorf("git status after a refused snippet = %v, want Allow", got)
			}
		})
	}
}

func TestAllowAlways_ReservedAndUnmatchablePatternsRefused(t *testing.T) {
	// "invalid" is what BashCommandUID substitutes for every malformed
	// pattern, so a grant on it would allow all of them at once.
	// A leading ".." pattern is replaced with "invalid" request-side, so
	// a grant on its raw text could never match.
	for _, pattern := range []string{"invalid", "..x", ""} {
		tool, _, dataDir := newSnippetTool(t)
		tool.writePolicySnippet(pattern)
		if files := policyFiles(t, dataDir); len(files) != 0 {
			t.Errorf("pattern %q persisted as %v; want refused", pattern, files)
		}
	}
}

func TestAllowAlways_QuoteBackslashRoundTrip(t *testing.T) {
	for _, pattern := range []string{
		`say"hi`,
		`back\slash`,
		`trailing\`,
		`\"`,
		`it's`,
		`star*`,
		"caf\u00e9",
		"e\u0301", // combining mark: cedar-go escapes grapheme extenders
		"\U0001F642 run",
	} {
		t.Run(pattern, func(t *testing.T) {
			tool, eng, dataDir := newSnippetTool(t)
			tool.writePolicySnippet(pattern)
			if err := eng.UserPolicyLoadError(); err != nil {
				t.Fatalf("user policy failed to load: %v", err)
			}
			files := policyFiles(t, dataDir)
			if len(files) != 1 {
				t.Fatalf("policy files = %v, want exactly one", files)
			}
			body, err := os.ReadFile(filepath.Join(dataDir, cedar.PolicyDir, files[0]))
			if err != nil {
				t.Fatal(err)
			}
			id, ok := cedar.ResourceScopeID(body, cedar.EntityTypeBashCommand)
			if !ok || id != pattern {
				t.Fatalf("parsed entity id = %q (ok=%v), want %q exactly\nbody:\n%s", id, ok, pattern, body)
			}
			if got := bashOutcome(eng, pattern); got != cedar.Allow {
				t.Errorf("approved pattern outcome = %v, want Allow", got)
			}
			assertNoBroadGrant(t, eng)
		})
	}
}

// Ordinary patterns must produce byte-for-byte the snippet they always
// did, so existing grants and the request-side resource id are unchanged.
func TestAllowAlways_OrdinaryPatternUnchanged(t *testing.T) {
	tool, eng, dataDir := newSnippetTool(t)
	tool.writePolicySnippet("git status")
	body, err := os.ReadFile(filepath.Join(dataDir, cedar.PolicyDir, "bash_allow_git_status.cedar"))
	if err != nil {
		t.Fatal(err)
	}
	want := "permit(\n" +
		"  principal,\n" +
		"  action == Action::\"run_bash_command\",\n" +
		"  resource == BashCommand::\"git status\"\n" +
		");\n"
	if string(body) != want {
		t.Errorf("snippet body changed for an ordinary pattern:\ngot:\n%s\nwant:\n%s", body, want)
	}
	if got := bashOutcome(eng, DerivePattern([]string{"/usr/bin/git", "status", "-s"})); got != cedar.Allow {
		t.Errorf("git status outcome = %v, want Allow", got)
	}
	if got := bashOutcome(eng, "git push"); got == cedar.Allow {
		t.Errorf("git push = Allow; grant leaked past its pattern")
	}
}

// The prompt tells the modal when "Allow always" would be refused, so
// the button is disabled instead of silently acting as Allow once.
func TestCedarGate_SurfaceFlagsUnpersistablePattern(t *testing.T) {
	for _, tc := range []struct {
		argv0       string
		unavailable bool
	}{
		{"/usr/bin/git", false},
		{"/tmp/data.db\necho", true},
		{"/tmp/x\uFFFD", true},
		{"/tmp/invalid", true},
	} {
		reg := cedar.NewRegistry(cedar.WithTimeout(2 * time.Second))
		got := make(chan cedar.PromptSurface, 1)
		go func() {
			for {
				if list := reg.ListPending(); len(list) > 0 {
					got <- list[0].Surface
					_ = reg.Resolve(list[0].RequestID, cedar.DecisionAllowOnce)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		tool, _ := newTool(t, func(o *Options) {
			o.CedarEngine = buildNotApplicableEngine(t)
			o.PromptRegistry = reg
		})
		if allow, _ := tool.cedarGate(context.Background(), []string{tc.argv0}, ""); !allow {
			t.Fatalf("%q: gate refused an Allow once", tc.argv0)
		}
		surf := <-got
		if surf.Bash == nil || surf.Bash.AllowAlwaysUnavailable != tc.unavailable {
			t.Errorf("%q: surface = %+v, want AllowAlwaysUnavailable=%v", tc.argv0, surf.Bash, tc.unavailable)
		}
	}
}
