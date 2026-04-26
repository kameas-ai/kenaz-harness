package toolloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStaticResolver_PriorityAndWildcards(t *testing.T) {
	rules := []permRule{
		// wildcard server + wildcard tool — lowest specificity
		{Server: "*", Tool: "*", Policy: PolicyConfirmEach, Reason: "default-confirm"},
		// wildcard server + exact tool
		{Server: "*", Tool: "exec", Policy: PolicyDeny, Reason: "exec disabled"},
		// exact server + wildcard tool
		{Server: "filesystem", Tool: "*", Policy: PolicyConfirmEach, Reason: "fs default"},
		// exact + exact — highest specificity
		{Server: "filesystem", Tool: "read", Policy: PolicyAutoAllow},
		{Server: "filesystem", Tool: "delete", Policy: PolicyDeny, Reason: "no deletes"},
	}
	resolver, err := NewStaticResolver(rules)
	if err != nil {
		t.Fatalf("NewStaticResolver: %v", err)
	}

	cases := []struct {
		server, tool string
		want         ToolPolicy
		wantReason   string
	}{
		// exact+exact wins over exact+wild
		{"filesystem", "read", PolicyAutoAllow, ""},
		{"filesystem", "delete", PolicyDeny, "no deletes"},
		// exact+wild wins over wild+wild for unmatched filesystem tools
		{"filesystem", "list", PolicyConfirmEach, "fs default"},
		// wild+exact wins over wild+wild for the global "exec" rule
		{"github", "exec", PolicyDeny, "exec disabled"},
		// wild+wild fallback
		{"github", "get_issue", PolicyConfirmEach, "default-confirm"},
	}
	for _, tc := range cases {
		t.Run(tc.server+"."+tc.tool, func(t *testing.T) {
			res, err := resolver.Resolve(context.Background(), "sess", tc.server, tc.tool)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if res.Policy != tc.want {
				t.Fatalf("policy = %q, want %q", res.Policy, tc.want)
			}
			if res.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if res.Server != tc.server || res.Tool != tc.tool {
				t.Fatalf("(server,tool) = (%q,%q), want (%q,%q)", res.Server, res.Tool, tc.server, tc.tool)
			}
		})
	}
}

func TestStaticResolver_NoRulesDefaultsToAutoAllow(t *testing.T) {
	resolver, err := NewStaticResolver(nil)
	if err != nil {
		t.Fatalf("NewStaticResolver: %v", err)
	}
	res, err := resolver.Resolve(context.Background(), "s", "any", "thing")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestStaticResolver_RejectsUnknownPolicy(t *testing.T) {
	_, err := NewStaticResolver([]permRule{{Server: "*", Tool: "*", Policy: "zonk"}})
	if err == nil {
		t.Fatal("expected error on unknown policy")
	}
}

func TestStaticResolverFromFile_MissingFileDefaultsAutoAllow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doesnotexist.json")
	resolver, err := NewStaticResolverFromFile(path)
	if err != nil {
		t.Fatalf("NewStaticResolverFromFile: %v", err)
	}
	res, err := resolver.Resolve(context.Background(), "s", "any", "thing")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestStaticResolverFromFile_LoadsRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_servers.json")
	body := `{
		"version": 1,
		"rules": [
			{"server": "filesystem", "tool": "*", "policy": "confirm_each"},
			{"server": "*", "tool": "exec", "policy": "deny", "reason": "shell exec disabled by policy"}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resolver, err := NewStaticResolverFromFile(path)
	if err != nil {
		t.Fatalf("NewStaticResolverFromFile: %v", err)
	}
	res, err := resolver.Resolve(context.Background(), "s", "anyserver", "exec")
	if err != nil {
		t.Fatalf("Resolve exec: %v", err)
	}
	if res.Policy != PolicyDeny || res.Reason != "shell exec disabled by policy" {
		t.Fatalf("exec resolution = %+v", res)
	}
	res, err = resolver.Resolve(context.Background(), "s", "filesystem", "list")
	if err != nil {
		t.Fatalf("Resolve filesystem.list: %v", err)
	}
	if res.Policy != PolicyConfirmEach {
		t.Fatalf("filesystem.list policy = %q, want confirm_each", res.Policy)
	}
}

func TestStaticResolverFromFile_MalformedJSONErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := NewStaticResolverFromFile(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestStaticResolverFromFile_BadPolicyErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	body := `{"version":1,"rules":[{"server":"*","tool":"*","policy":"yolo"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := NewStaticResolverFromFile(path)
	if err == nil {
		t.Fatal("expected policy validation error")
	}
}

func TestStaticResolverFromDataDir_EmptyDir(t *testing.T) {
	resolver, err := NewStaticResolverFromDataDir("")
	if err != nil {
		t.Fatalf("NewStaticResolverFromDataDir: %v", err)
	}
	res, _ := resolver.Resolve(context.Background(), "s", "x", "y")
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestStaticResolverFromDataDir_MissingFile(t *testing.T) {
	dir := t.TempDir()
	resolver, err := NewStaticResolverFromDataDir(dir)
	if err != nil {
		t.Fatalf("NewStaticResolverFromDataDir: %v", err)
	}
	res, _ := resolver.Resolve(context.Background(), "s", "x", "y")
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

// stubOverrideReader returns a fixed list of overrides for one
// session id.
type stubOverrideReader struct {
	sessionID string
	overrides []MCPOverride
	err       error
}

func (r *stubOverrideReader) MCPOverrides(_ context.Context, sessionID string) ([]MCPOverride, error) {
	if r.err != nil {
		return nil, r.err
	}
	if sessionID != r.sessionID {
		return nil, nil
	}
	return r.overrides, nil
}

func TestSessionOverrideResolver_PriorityAndWildcards(t *testing.T) {
	reader := &stubOverrideReader{
		sessionID: "sess-1",
		overrides: []MCPOverride{
			{Server: "*", Tool: "*", Policy: PolicyConfirmEach},
			{Server: "filesystem", Tool: "*", Policy: PolicyAutoAllow},
			{Server: "filesystem", Tool: "delete", Policy: PolicyDeny, Reason: "no deletes for this session"},
		},
	}
	resolver := NewSessionOverrideResolver(reader)

	// exact+exact wins
	res, err := resolver.Resolve(context.Background(), "sess-1", "filesystem", "delete")
	if err != nil {
		t.Fatalf("Resolve delete: %v", err)
	}
	if res.Policy != PolicyDeny {
		t.Fatalf("filesystem.delete policy = %q", res.Policy)
	}

	// exact+wild over wild+wild
	res, err = resolver.Resolve(context.Background(), "sess-1", "filesystem", "list")
	if err != nil {
		t.Fatalf("Resolve list: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("filesystem.list policy = %q", res.Policy)
	}

	// wild+wild fallback
	res, err = resolver.Resolve(context.Background(), "sess-1", "github", "get_issue")
	if err != nil {
		t.Fatalf("Resolve github: %v", err)
	}
	if res.Policy != PolicyConfirmEach {
		t.Fatalf("github.get_issue policy = %q", res.Policy)
	}
}

func TestSessionOverrideResolver_EmptyOverridesAreAutoAllow(t *testing.T) {
	resolver := NewSessionOverrideResolver(NoopSessionOverrideReader{})
	res, err := resolver.Resolve(context.Background(), "any", "x", "y")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestSessionOverrideResolver_UnknownSessionAutoAllow(t *testing.T) {
	reader := &stubOverrideReader{
		sessionID: "sess-known",
		overrides: []MCPOverride{{Server: "*", Tool: "*", Policy: PolicyDeny}},
	}
	resolver := NewSessionOverrideResolver(reader)
	res, err := resolver.Resolve(context.Background(), "sess-unknown", "x", "y")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("unknown session resolved as %q, want auto_allow", res.Policy)
	}
}

func TestSessionOverrideResolver_NilReaderIsNoop(t *testing.T) {
	resolver := NewSessionOverrideResolver(nil)
	res, err := resolver.Resolve(context.Background(), "any", "x", "y")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestSessionOverrideResolver_ReaderErrorPropagates(t *testing.T) {
	wantErr := errors.New("db down")
	resolver := NewSessionOverrideResolver(&stubOverrideReader{err: wantErr})
	_, err := resolver.Resolve(context.Background(), "s", "x", "y")
	if err == nil {
		t.Fatal("expected error from reader")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wraps %v", err, wantErr)
	}
}

func TestMergedResolver_SessionOverridesWinWhenMatched(t *testing.T) {
	staticRes, err := NewStaticResolver([]permRule{
		{Server: "filesystem", Tool: "delete", Policy: PolicyDeny, Reason: "global ban"},
	})
	if err != nil {
		t.Fatalf("NewStaticResolver: %v", err)
	}
	sessionRes := NewSessionOverrideResolver(&stubOverrideReader{
		sessionID: "sess-1",
		overrides: []MCPOverride{
			{Server: "filesystem", Tool: "delete", Policy: PolicyAutoAllow, Reason: "trusted operator"},
		},
	})
	merged := NewMergedResolver(staticRes, sessionRes)

	res, err := merged.Resolve(context.Background(), "sess-1", "filesystem", "delete")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow (session override)", res.Policy)
	}
	if res.Reason != "trusted operator" {
		t.Fatalf("reason = %q, want trusted operator", res.Reason)
	}
}

func TestMergedResolver_StaticAppliesWhenSessionDoesNotMatch(t *testing.T) {
	staticRes, err := NewStaticResolver([]permRule{
		{Server: "*", Tool: "exec", Policy: PolicyDeny, Reason: "no shell"},
	})
	if err != nil {
		t.Fatalf("NewStaticResolver: %v", err)
	}
	sessionRes := NewSessionOverrideResolver(&stubOverrideReader{
		sessionID: "sess-1",
		// session has rules but none match (server, tool="exec")
		overrides: []MCPOverride{
			{Server: "filesystem", Tool: "*", Policy: PolicyAutoAllow},
		},
	})
	merged := NewMergedResolver(staticRes, sessionRes)
	res, err := merged.Resolve(context.Background(), "sess-1", "github", "exec")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyDeny {
		t.Fatalf("policy = %q, want deny (static)", res.Policy)
	}
	if res.Reason != "no shell" {
		t.Fatalf("reason = %q, want no shell", res.Reason)
	}
}

func TestMergedResolver_BothNilIsAutoAllow(t *testing.T) {
	merged := NewMergedResolver(nil, nil)
	res, err := merged.Resolve(context.Background(), "sess", "x", "y")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != PolicyAutoAllow {
		t.Fatalf("policy = %q, want auto_allow", res.Policy)
	}
}

func TestMergedResolver_AcceptsExternalSessionResolver(t *testing.T) {
	// External resolvers go through the shim path. A non-auto_allow
	// result counts as a match; auto_allow without a Reason falls
	// through to the static resolver.
	staticRes, _ := NewStaticResolver([]permRule{
		{Server: "*", Tool: "*", Policy: PolicyDeny, Reason: "static-default"},
	})
	external := externalResolverFunc(func(_ context.Context, _, server, tool string) (Resolution, error) {
		if server == "approved" {
			return Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}, nil
		}
		// no match — auto_allow with no reason
		return Resolution{Server: server, Tool: tool, Policy: PolicyAutoAllow}, nil
	})
	merged := NewMergedResolver(staticRes, external)

	// Server "approved" → external returns auto_allow but with no
	// reason; the shim treats this as a non-match and the static
	// deny applies. To match, the external must signal via a
	// non-auto_allow policy or a non-empty Reason.
	res, _ := merged.Resolve(context.Background(), "s", "approved", "x")
	if res.Policy != PolicyDeny {
		t.Fatalf("policy = %q, want deny — external auto_allow without reason should not override static", res.Policy)
	}

	// External signals match with non-auto_allow policy:
	external2 := externalResolverFunc(func(_ context.Context, _, server, tool string) (Resolution, error) {
		return Resolution{Server: server, Tool: tool, Policy: PolicyConfirmEach, Reason: "session-confirm"}, nil
	})
	merged2 := NewMergedResolver(staticRes, external2)
	res, _ = merged2.Resolve(context.Background(), "s", "approved", "x")
	if res.Policy != PolicyConfirmEach {
		t.Fatalf("policy = %q, want confirm_each", res.Policy)
	}
}

type externalResolverFunc func(ctx context.Context, sessionID, server, tool string) (Resolution, error)

func (f externalResolverFunc) Resolve(ctx context.Context, sessionID, server, tool string) (Resolution, error) {
	return f(ctx, sessionID, server, tool)
}
