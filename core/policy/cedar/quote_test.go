package cedar

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// quoteCases covers every escape class Cedar defines plus the ones Go's
// strconv.Quote would get wrong (\x.., \u....).
var quoteCases = []string{
	"git status", "", `"`, `\`, `\"`, `trailing\`, `it's`, "a*b",
	"line\nbreak", "cr\rlf", "tab\t", "nul\x00", "esc\x1b", "del\x7f",
	"c1\u0085", "bidi\u202e", "e\u0301", "caf\u00e9", "\U0001F642", "\ufeffbom",
	`x"); permit(principal, action, resource); //`,
}

// TestQuoteString_RoundTripsThroughVendoredParser builds a real policy
// around the quoted literal, parses it with cedar-go, and requires the
// entity id to come back byte-for-byte.
func TestQuoteString_RoundTripsThroughVendoredParser(t *testing.T) {
	for _, s := range quoteCases {
		body := "permit(principal, action == Action::\"run_bash_command\", resource == " +
			EntityTypeBashCommand + "::" + QuoteString(s) + ");\n"
		ps, err := cedarlib.NewPolicySetFromBytes("q.cedar", []byte(body))
		if err != nil {
			t.Errorf("%q: generated policy does not parse: %v\n%s", s, err, body)
			continue
		}
		n := 0
		for range ps.All() {
			n++
		}
		if n != 1 {
			t.Errorf("%q: parsed %d policies, want exactly 1 (injection)", s, n)
		}
		id, ok := ResourceScopeID([]byte(body), EntityTypeBashCommand)
		if !ok || id != s {
			t.Errorf("%q: round-trip id = %q (ok=%v)", s, id, ok)
		}
		// And the authorizer matches exactly that raw id, nothing else.
		req := cedarlib.Request{
			Principal: UserUID(),
			Action:    cedarlib.NewEntityUID("Action", "run_bash_command"),
			Resource:  cedarlib.NewEntityUID(EntityTypeBashCommand, cedarlib.String(s)),
			Context:   cedarlib.NewRecord(nil),
		}
		if d, _ := cedarlib.Authorize(ps, cedarlib.EntityMap{}, req); d != cedarlib.Allow {
			t.Errorf("%q: authorizer did not match the raw id", s)
		}
		req.Resource = cedarlib.NewEntityUID(EntityTypeBashCommand, cedarlib.String(s+"x"))
		if d, _ := cedarlib.Authorize(ps, cedarlib.EntityMap{}, req); d == cedarlib.Allow {
			t.Errorf("%q: authorizer matched a different id", s)
		}
	}
}

func TestQuoteLikePrefix_EscapesWildcard(t *testing.T) {
	for _, tc := range []struct {
		prefix, path string
		want         bool
	}{
		{"/home/u/", "/home/u/a/b", true},
		{"/home/u/", "/home/v/a", false},
		{"/tmp/*/", "/tmp/*/x", true},
		{"/tmp/*/", "/tmp/other/x", false}, // a dir named * is not a wildcard
		{`/tmp/q"uote/`, `/tmp/q"uote/x`, true},
		{`/tmp/b\s/`, `/tmp/b\s/x`, true},
		{"/tmp/nl\n/", "/tmp/nl\n/x", true},
	} {
		body := `permit(principal, action, resource) when { context.p like ` + QuoteLikePrefix(tc.prefix) + ` };`
		ps, err := cedarlib.NewPolicySetFromBytes("l.cedar", []byte(body))
		if err != nil {
			t.Errorf("%q: does not parse: %v\n%s", tc.prefix, err, body)
			continue
		}
		req := cedarlib.Request{
			Principal: UserUID(),
			Action:    cedarlib.NewEntityUID("Action", "a"),
			Resource:  cedarlib.NewEntityUID("R", "r"),
			Context:   cedarlib.NewRecord(cedarlib.RecordMap{"p": cedarlib.String(tc.path)}),
		}
		d, _ := cedarlib.Authorize(ps, cedarlib.EntityMap{}, req)
		if (d == cedarlib.Allow) != tc.want {
			t.Errorf("prefix %q path %q: allow=%v, want %v", tc.prefix, tc.path, d == cedarlib.Allow, tc.want)
		}
	}
}

func TestPersistableFamilyID(t *testing.T) {
	for id, want := range map[string]bool{
		"git status": true, `say"hi`: true, "": false, "invalid": false,
		"..x": false, "a\nb": false, "a\x00b": false, "a\tb": false,
	} {
		if got := PersistableFamilyID(id); got != want {
			t.Errorf("PersistableFamilyID(%q) = %v, want %v", id, got, want)
		}
	}
}

// A recipe id carrying a quote must not inject policy through the
// mcp_spawn grant; a control-character id is refused outright.
func TestWriteMCPSpawnSnippet_EscapesRecipeID(t *testing.T) {
	ctx := context.Background()
	e, dir := newDiskEngine(t, nil)
	evil := `r::mcp_spawn"); permit(principal, action, resource); permit(principal, action, resource == Credential::"z`
	if err := WriteMCPSpawnSnippet(ctx, dir, e, evil); err != nil {
		t.Fatalf("WriteMCPSpawnSnippet: %v", err)
	}
	if err := e.UserPolicyLoadError(); err != nil {
		t.Fatalf("user policy failed to load: %v", err)
	}
	if d := e.Evaluate(ctx, UserUID(), ActionUseTool, ToolUID("evil", "x"), nil); d.Outcome == Allow {
		t.Fatalf("use_tool allowed via recipe-id injection (matched %q)", d.MatchedPolicy)
	}
	if d := e.Evaluate(ctx, UserUID(), ActionUseCredential, CredentialUID(evil, "mcp_spawn"), nil); d.Outcome != Allow {
		t.Errorf("the approved recipe's own spawn grant = %v, want Allow", d.Outcome)
	}

	if err := WriteMCPSpawnSnippet(ctx, dir, e, "bad\nid"); err == nil {
		t.Error("control-character recipe id persisted; want refused")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, PolicyDir))
	for _, ent := range entries {
		if strings.Contains(ent.Name(), "bad") {
			t.Errorf("refused recipe id still wrote %s", ent.Name())
		}
	}
}
