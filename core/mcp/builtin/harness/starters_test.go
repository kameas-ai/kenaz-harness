package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// harnessToolNamePattern matches any harness_* identifier token that could
// appear in a starter prompt's markdown body. Mirrors AC-003's
// `grep -o 'harness_[a-z_]*'` contract.
var harnessToolNamePattern = regexp.MustCompile(`harness_[a-z_]+`)

// registeredHarnessToolNames returns the live registered tool-name set by
// actually calling RegisterAll — not a hand-written list in the test, which
// is exactly the drift that let code.md:23 survive the
// harness_write_propose_cedar_policy deletion (first-run-onboarding-01PMOB01
// spec §"Prompt truth pass"). A zero-value Managers{} is fine: RegisterAll
// only wires handler funcs, it never invokes them.
func registeredHarnessToolNames(t *testing.T) map[string]bool {
	t.Helper()
	srv := RegisterAll(NewServer(), Managers{})
	out := make(map[string]bool)
	for _, spec := range srv.Tools() {
		out[spec.Name] = true
	}
	return out
}

// TestStarterPromptsNameOnlyRegisteredTools is WP01's load-bearing test
// (FR-001 / AC-003): every harness_* identifier extracted from a shipped
// starter's markdown body must resolve to a tool RegisterAll actually
// registered. It is a table test over the full shipped starter set so a
// future starter addition is covered automatically.
//
// Mutation: re-add `harness_write_propose_cedar_policy` to code.md (the
// tool the 2026-08-14 sweep deleted from register.go). This test must fail.
func TestStarterPromptsNameOnlyRegisteredTools(t *testing.T) {
	t.Parallel()
	registered := registeredHarnessToolNames(t)

	starters, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	if len(starters) == 0 {
		t.Fatal("LoadStarters(\"\") returned no starters — nothing to check")
	}

	for _, s := range starters {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			t.Parallel()
			names := harnessToolNamePattern.FindAllString(s.SystemPrompt, -1)
			for _, name := range names {
				if !registered[name] {
					t.Errorf("starter %q names %q, which is not in register.go's registered tool set", s.ID, name)
				}
			}
		})
	}
}

// TestLoadStarters_Embedded asserts the five canonical starters ship.
func TestLoadStarters_Embedded(t *testing.T) {
	t.Parallel()
	got, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	want := map[string]bool{"chat": false, "code": false, "data": false, "research": false, "writing": false}
	for _, s := range got {
		if _, ok := want[s.ID]; ok {
			want[s.ID] = true
		}
		if s.Source != "embedded" {
			t.Errorf("starter %q source = %q, want embedded", s.ID, s.Source)
		}
	}
	for id, ok := range want {
		if !ok {
			t.Errorf("missing starter %q", id)
		}
	}
}

// TestLoadStarters_UserOverride verifies a user-dropped file shadows the
// embedded entry with the same id.
func TestLoadStarters_UserOverride(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "onboarding", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	override := []byte("---\nid: code\ntitle: My Custom Code Setup\ndescription: override\n---\nMy custom prompt body.\n")
	if err := os.WriteFile(filepath.Join(dir, "code.md"), override, 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	got, err := LoadStarters(dataDir)
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	var code Starter
	for _, s := range got {
		if s.ID == "code" {
			code = s
			break
		}
	}
	if code.Title != "My Custom Code Setup" {
		t.Errorf("override title not applied: %q", code.Title)
	}
	if code.Source != "user" {
		t.Errorf("override source = %q, want user", code.Source)
	}
}

// TestStarterPromptsNameNoUnreachableTools is the FR-002 / AC-004 assertion
// WP01 could not make and WP05 was meant to inherit: *reachability*, not
// registration.
//
// TestStarterPromptsNameOnlyRegisteredTools above proves every harness_*
// name resolves in register.go. That is strictly weaker than what FR-002
// requires ("no prompt promises a capability the session cannot reach"),
// because all twelve harness_* tools are registered on a server that is
// attached to nothing: core/rpc/api.go builds a.harnessServer and never
// reads it again, and harness.NewTransport has zero production callers. A
// registered tool on an unattached server is in no session's catalog.
//
// Before first-run-onboarding-01PMOB01 WP02 that gap was inert — the audit's
// own words, "both halves are inert, so nothing is model-visible today".
// WP02 wired delivery. From that commit on, any harness_* name left in a
// shipped starter is a live instruction to call a tool the model cannot see,
// on the user's first ever turn. AC-004 pins the shipped-branch assertion:
// "under retire/park, the assertion is that the set is empty."
//
// The B10 owner ruling is ATTACH, but the attach work
// (mcp-connector-lifecycle-01PMMC01 WP07) targets a later release. FR-002
// binds on the branch that ships, not the branch that was decided, so this
// test asserts empty for as long as the transport is unwired.
//
// WHEN ATTACH LANDS: do not delete this test. Replace the emptiness
// assertion with the stronger one WP05 specifies — for a live
// kind=onboarding session, every harness_* name in a starter appears in
// that session's Tools(ctx) listing. Registration alone must never again be
// the only thing standing behind a starter prompt's promise.
func TestStarterPromptsNameNoUnreachableTools(t *testing.T) {
	t.Parallel()
	starters, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	if len(starters) == 0 {
		t.Fatal("LoadStarters(\"\") returned no starters — nothing to check")
	}
	for _, s := range starters {
		if names := harnessToolNamePattern.FindAllString(s.SystemPrompt, -1); len(names) != 0 {
			t.Errorf("starter %q names harness_* tool(s) %v, but the harness-self MCP server is "+
				"attached to nothing in this release, so a session of any kind can call none of "+
				"them. Delivered prompts may not name unreachable tools (FR-002/AC-004).",
				s.ID, names)
		}
	}
}

// TestStarterPromptsCarryNoEngineeringNotes pins the leak class WP01 created
// and WP02 made live: WP01 recorded a pending-decision note as an HTML
// comment in code.md's *body*. Markdown comments are not stripped anywhere —
// parseStarter takes the whole post-frontmatter body verbatim as
// SystemPrompt — so once WP02 wired delivery, nine lines of mission-internal
// chatter (mission ids, spec section numbers, "do not fix this either
// direction") shipped to the model as part of its first-turn system prompt.
//
// Engineering notes belong in the frontmatter, where parseStarter drops any
// line beginning with '#' on the floor. Both code.md and chat.md carry their
// notes there now.
func TestStarterPromptsCarryNoEngineeringNotes(t *testing.T) {
	t.Parallel()
	starters, err := LoadStarters("")
	if err != nil {
		t.Fatalf("LoadStarters: %v", err)
	}
	for _, s := range starters {
		if strings.Contains(s.SystemPrompt, "<!--") {
			t.Errorf("starter %q's SystemPrompt contains an HTML comment. The body is handed to "+
				"the model verbatim; put engineering notes in the frontmatter as '#' lines, which "+
				"parseStarter skips.\nprompt:\n%s", s.ID, s.SystemPrompt)
		}
	}
}
