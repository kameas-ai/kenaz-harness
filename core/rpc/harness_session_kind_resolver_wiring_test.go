package rpc

// harness-self-attach-01PMHS01 UNIT-4 — wiring-level tests. These drive
// the ACTUAL production wire (New's constructed a.toolPermsResolver),
// not a hand-built stand-in resolver — a hand-built equivalent would
// prove the resolver type works (that's UNIT-3's job, in
// harness_session_kind_resolver_test.go) but say nothing about whether
// New() actually reaches for it.
//
// The harness-self MCP server itself has NO production caller yet —
// that is UNIT-6, hard-gated on this unit's AC-002 (spec.md). So AC-006
// and AC-007 below drive the discoverer with a FAKE pool that reports
// the harness-self tool names this list tracks (see the note on
// harnessWriteToolNames below) under the real "harness-self" server
// prefix, through the REAL merged resolver. That is a faithful test of
// "does the live wire filter harness-self-shaped tool names correctly"
// without needing UNIT-6's attach — the pool contents are not what this
// unit changes; the resolver is.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/mcp"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// 7 read + 5 write harness-self tool names, pulled from the same
// exported constants harness_wiring.go registers against the real
// server (core/mcp/builtin/harness/register.go) — kept as symbols
// rather than literals so this test cannot drift from the real tool set
// without failing to compile. Not exhaustive against every tool the
// live server registers today (it predates model-authored-graphs-01PMGA01's
// harness_read_materialize_run / harness_write_draft_agent_graph, which
// this list was never extended to track) — it exists to prove the
// resolver correctly filters the write/read split for the tools it
// does track, not to enumerate the server's full surface.
//
// harnessmcp.ToolSetSetting no longer exists:
// harness-self-attach-01PMHS01 UNIT-8 (G-4) removed
// harness_write_set_setting entirely (see the doc comment above
// harness.ProjectWriter in core/mcp/builtin/harness/handlers.go), so it
// is absent from harnessWriteToolNames rather than merely untracked.
var (
	harnessReadToolNames = []string{
		harnessmcp.ToolListProviders,
		harnessmcp.ToolListMCPRecipes,
		harnessmcp.ToolListSettings,
		harnessmcp.ToolGetStatus,
		harnessmcp.ToolGetRecommendations,
		harnessmcp.ToolListSessions,
		harnessmcp.ToolListModels,
	}
	harnessWriteToolNames = []string{
		harnessmcp.ToolAddProvider,
		harnessmcp.ToolRemoveProvider,
		harnessmcp.ToolInstallMCPRecipe,
		harnessmcp.ToolCreateProject,
		harnessmcp.ToolCreateSession,
	}
)

// harnessShapedPool is a fake mcp.Pool reporting the harness-self tool
// names tracked above under server "harness-self". No harness-self
// wiring is involved — this exists purely so AC-006/AC-007 can drive
// the REAL merged resolver against the real tool-name shapes it will
// see once UNIT-6 attaches the real server.
type harnessShapedPool struct{}

func (harnessShapedPool) Open(context.Context, []mcp.ServerSpec) error { return nil }
func (harnessShapedPool) Close(context.Context) error                  { return nil }
func (harnessShapedPool) Call(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
	panic("harnessShapedPool.Call: not reached by these tests")
}
func (harnessShapedPool) Tools(context.Context) ([]mcp.Tool, error) {
	var out []mcp.Tool
	for _, name := range harnessReadToolNames {
		out = append(out, mcp.Tool{Server: "harness-self", Name: name})
	}
	for _, name := range harnessWriteToolNames {
		out = append(out, mcp.Tool{Server: "harness-self", Name: name})
	}
	return out, nil
}

// bootAPIWithCore boots a real Core + rpc.API over dataDir, optionally
// pre-seeding <dataDir>/mcp_servers.json with mcpServersJSON (skipped
// when empty). Returns both the Core (so tests can create real sessions
// through c.SessionManager()) and the API.
func bootAPIWithCore(t *testing.T, dataDir, mcpServersJSON string) (*core.Core, *API) {
	t.Helper()
	sandboxUserConfigDir(t)
	if mcpServersJSON != "" {
		if err := os.WriteFile(filepath.Join(dataDir, "mcp_servers.json"), []byte(mcpServersJSON), 0o644); err != nil {
			t.Fatalf("write mcp_servers.json: %v", err)
		}
	}
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	// rpc.New starts background workers — among them the fleet
	// ConfigPoller (settings.API.SetFleetClient -> fleet.ConfigPoller.Start).
	// Without this cleanup the poller outlives the test and keeps calling
	// fleet.LoadTokens -> keyring.Get() for the rest of the test BINARY's
	// life, racing every later test that writes go-keyring's package-level
	// mock global via keyring.MockInit().
	//
	// That is not hypothetical: it reddened a full-suite
	// `-race -short -p 4` run on 2026-08-20 as
	//
	//   --- FAIL: TestKeychainDelete_NotFoundIsSilent
	//       testing.go:1712: race detected during execution of test
	//
	// with the detector naming keyring.MockInit (keychain_test.go:22) as
	// the write and this helper's leaked poller as the read. The failure
	// surfaces in whichever test happens to call MockInit, which is why it
	// looks like an unrelated keychain flake and why it never reproduces in
	// isolation — the racing goroutine belongs to a different test.
	//
	// API.Shutdown is nil-safe and idempotent, and stops the poller via
	// settingsImpl.StopFleetBackground.
	t.Cleanup(api.Shutdown)
	return c, api
}

// splitNamesByPrefix partitions namespaced "harness-self__<tool>" names
// into their bare tool names, and separately reports whether ANY
// harness_write_* name is present.
func harnessWriteNamesPresent(names []string) []string {
	var writes []string
	for _, n := range names {
		bare := n
		if idx := len("harness-self" + llm.ToolNameSeparator); len(n) > idx && n[:idx] == "harness-self"+llm.ToolNameSeparator {
			bare = n[idx:]
		}
		for _, w := range harnessWriteToolNames {
			if bare == w {
				writes = append(writes, n)
			}
		}
	}
	sort.Strings(writes)
	return writes
}

func harnessReadNamesPresent(names []string) []string {
	var reads []string
	for _, n := range names {
		bare := n
		if idx := len("harness-self" + llm.ToolNameSeparator); len(n) > idx && n[:idx] == "harness-self"+llm.ToolNameSeparator {
			bare = n[idx:]
		}
		for _, r := range harnessReadToolNames {
			if bare == r {
				reads = append(reads, n)
			}
		}
	}
	sort.Strings(reads)
	return reads
}

// TestHarnessContainment_DiscovererFiltersWriteToolsForChatSession is
// AC-006.
//
// Mutation: revert the merged resolver (UNIT-4's wire in newLLMStack)
// to the bare static resolver. Must fail — verified by hand below.
func TestHarnessContainment_DiscovererFiltersWriteToolsForChatSession(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil after New() — UNIT-4's wire did not run")
	}

	// The Sessions() RPC view's Create defaults Kind to "chat"
	// (session.Manager.CreateInProject) — exactly the case AC-006 names.
	// api.toolPermsResolver's session arm reads through the SAME
	// c.SessionManager() this creates the row in, so no separate handle
	// on c is needed here.
	rec, err := api.Sessions().Create(context.Background(), "chat session")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	pool := harnessShapedPool{}
	discoverer := llm.NewMCPToolDiscoverer(pool, api.toolPermsResolver)

	specs, err := discoverer.Tools(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}

	if writes := harnessWriteNamesPresent(names); len(writes) != 0 {
		t.Fatalf("kind=chat session: harness_write_* tools present in listing: %v", writes)
	}
	if reads := harnessReadNamesPresent(names); len(reads) != len(harnessReadToolNames) {
		t.Fatalf("kind=chat session: expected all %d harness_read_* tools, got %d: %v", len(harnessReadToolNames), len(reads), reads)
	}
}

// TestHarnessContainment_SessionlessDiscoveryClosesWriteTools is AC-007
// (C-011's session-less discovery path — wfToolDiscovererAdapter).
//
// This ALSO pins "one resolver reaches both consumers": the
// wfToolDiscovererAdapter below wraps the SAME corellm.ToolDiscoverer
// instance AC-006 drives, which itself closes over the SAME
// api.toolPermsResolver — structurally, not just behaviourally, the one
// merged resolver newLLMStack built.
//
// Mutation: revert the merged resolver. Must fail — verified by hand
// below.
func TestHarnessContainment_SessionlessDiscoveryClosesWriteTools(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil after New()")
	}

	pool := harnessShapedPool{}
	discoverer := llm.NewMCPToolDiscoverer(pool, api.toolPermsResolver)
	wfAdapter := &wfToolDiscovererAdapter{inner: discoverer}

	specs, err := wfAdapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	if writes := harnessWriteNamesPresent(names); len(writes) != 0 {
		t.Fatalf("session-less discovery: harness_write_* tools present: %v", writes)
	}
}

// TestHarnessContainment_AC015_EmptyDataDirCannotBeAbsent is AC-015,
// case (i). "Empty DataDir" for rpc.New's purposes means a nil
// *core.Core: core.New itself REJECTS an empty DataDir
// ("core: DataDir required", core/core.go:232-234), so a real,
// non-nil *core.Core with an empty DataDir cannot exist through the
// public constructor. coreDataDir(nil) == "" is the actual "empty
// DataDir" boot path api.go's New/newLLMStack degrade on.
//
// Mutation: move the merged-resolver construction back inside the
// `if c != nil && c.DataDir() != ""` block (i.e. revert UNIT-4's wire).
// Must fail — verified by hand below.
func TestHarnessContainment_AC015_EmptyDataDirCannotBeAbsent(t *testing.T) {
	api := New(nil)
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil for a nil-core boot — this is exactly the pre-UNIT-4 hole: nil is indistinguishable from \"no resolver\" to both consumers")
	}

	res, err := api.toolPermsResolver.Resolve(context.Background(), "any-session-id-at-all", "harness-self", harnessmcp.ToolAddProvider)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("empty-DataDir boot: harness_write_add_provider policy = %q, want %q", res.Policy, toolloop.PolicyDeny)
	}
	// "No provider created" follows from this Deny: kernelToolAdapter's
	// PolicyDeny branch (pre-existing, and pinned alongside this unit by
	// AC-016) returns an IsError result BEFORE calling the tool pool —
	// see core/rpc/views/agentgraph/chat/kernel_tool_adapter.go's
	// dispatch. A resolver that reports Deny here can never reach a
	// handler that would create a provider.
}

// TestHarnessContainment_AC015_MalformedStaticConfigCannotBeAbsent is
// AC-015, case (ii): a real DataDir with a malformed
// mcp_servers.json — the static arm fails to load (soft-fails to nil,
// logged) but the session arm (real Cedar engine, real session store)
// must still deny on its own.
//
// Mutation: move the merged-resolver construction back inside the
// `if c != nil && c.DataDir() != ""` block. Must fail — verified by
// hand below. (That mutation does not even touch this scenario's DataDir
// gate, since DataDir IS non-empty here — the point is the STATIC
// arm's own permErr branch used to be the thing that left `perms` at
// its zero value, before UNIT-4; this test targets that branch
// specifically, distinct from case (i)'s nil-core branch.)
func TestHarnessContainment_AC015_MalformedStaticConfigCannotBeAbsent(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "{not valid json")
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil")
	}

	sessionMgr := c.SessionManager()
	rec, err := sessionMgr.Create(context.Background(), "chat session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Kind != session.SessionKindChat {
		t.Fatalf("default session kind = %q, want %q", rec.Kind, session.SessionKindChat)
	}

	res, err := api.toolPermsResolver.Resolve(context.Background(), rec.ID, "harness-self", harnessmcp.ToolAddProvider)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("malformed mcp_servers.json boot: harness_write_add_provider policy = %q, want %q (reason=%q)", res.Policy, toolloop.PolicyDeny, res.Reason)
	}
}
