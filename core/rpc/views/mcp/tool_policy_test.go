package mcp

// trust-surfaces-that-fire-01PMZ202 WP24 (finding CHAT-05): tests for
// the RPC-layer half of the writer. The Go-level round-trip and the
// production kernelToolAdapter path are covered in core/toolloop and
// core/rpc/views/agentgraph/chat respectively; these pin the API
// methods a Wails binding actually calls (SetToolPolicy /
// ListToolPolicies), including the "no DataDir wired" degrade the
// rpc.New(nil) test harness relies on elsewhere in this package.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

func TestAPI_SetToolPolicy_NoDataDirReturnsSentinel(t *testing.T) {
	a := NewAPI()
	err := a.SetToolPolicy(context.Background(), "filesystem", "*", "confirm_each", "")
	if !errors.Is(err, ErrDataDirNotConfigured) {
		t.Fatalf("err = %v, want ErrDataDirNotConfigured", err)
	}
}

func TestAPI_SetToolPolicy_EmptyDataDirFuncReturnsSentinel(t *testing.T) {
	a := NewAPI(WithDataDir(func() string { return "" }))
	err := a.SetToolPolicy(context.Background(), "filesystem", "*", "confirm_each", "")
	if !errors.Is(err, ErrDataDirNotConfigured) {
		t.Fatalf("err = %v, want ErrDataDirNotConfigured", err)
	}
}

func TestAPI_ListToolPolicies_NoDataDirReturnsEmptyNotError(t *testing.T) {
	a := NewAPI()
	rules, err := a.ListToolPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListToolPolicies: %v", err)
	}
	if rules == nil || len(rules) != 0 {
		t.Fatalf("rules = %+v, want an empty non-nil slice", rules)
	}
}

// AC-24a/AC-24c through the RPC surface: a rule set via the API is
// visible via ListToolPolicies AND survives a fresh
// toolloop.NewStaticResolverFromDataDir over the same directory —
// simulating a chassis restart — proving the RPC methods write the
// exact file the production resolver reads.
func TestAPI_SetToolPolicy_WritesRealFileAndSurvivesFreshResolver(t *testing.T) {
	dir := t.TempDir()
	a := NewAPI(WithDataDir(func() string { return dir }))

	if err := a.SetToolPolicy(context.Background(), "filesystem", "*", "confirm_each", "fs default"); err != nil {
		t.Fatalf("SetToolPolicy: %v", err)
	}

	rules, err := a.ListToolPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListToolPolicies: %v", err)
	}
	if len(rules) != 1 || rules[0].Policy != toolloop.PolicyConfirmEach {
		t.Fatalf("rules = %+v, want one confirm_each rule", rules)
	}

	// Fresh resolver, freshly constructed — not the API's in-memory
	// state — must see it, and the file itself must exist at the exact
	// path toolloop.NewStaticResolverFromDataDir reads.
	resolver, err := toolloop.NewStaticResolverFromDataDir(dir)
	if err != nil {
		t.Fatalf("NewStaticResolverFromDataDir: %v", err)
	}
	res, err := resolver.Resolve(context.Background(), "sess", "filesystem", "delete_file")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyConfirmEach {
		t.Fatalf("policy = %q, want confirm_each — the RPC write did not survive a fresh resolver", res.Policy)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "mcp_servers.json")); statErr != nil {
		t.Fatalf("mcp_servers.json was not written to DataDir: %v", statErr)
	}
}

func TestAPI_SetToolPolicy_RejectsUnknownPolicy(t *testing.T) {
	dir := t.TempDir()
	a := NewAPI(WithDataDir(func() string { return dir }))
	if err := a.SetToolPolicy(context.Background(), "x", "y", "yolo", ""); err == nil {
		t.Fatal("expected an error for an unknown policy string")
	}
}
