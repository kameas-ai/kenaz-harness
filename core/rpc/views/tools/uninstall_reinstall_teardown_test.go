package tools_test

// connector-lifecycle-truth-01PMZ303 UNIT-6: end-to-end coverage for
// FR-004 ("Uninstalling a connector removes it, on every transport,
// and re-installing evicts the old one") driven through the SAME
// surface a real install/uninstall uses: tools.API.InstallRecipe /
// UninstallRecipe, wired to a REAL *dispatch.Pool wrapping a REAL
// *http.Pool against real httptest.Server fixtures — not the
// fakePool test double impl_test.go's other tests use. Per the
// mission spec's test rule R-2 ("Fixtures must drive the layer under
// test... a fake sub-pool proves nothing about http.Pool"), the unit
// tests in core/mcp/transport/http/pool_test.go and
// core/mcp/dispatch/pool_test.go already pin the exact teardown
// mechanics (probe-goroutine exit, ownership restore on failure).
// This file's job is different: prove the WIRING from the RPC-facing
// API actually reaches those mechanics, and prove the negative
// (unrelated installs survive) that CLAUDE.md requires of any
// deletion-path change.

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/dispatch"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	mcphttp "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// countingToolsListServer answers every JSON-RPC POST with a
// one-tool success envelope and counts requests, so a test can prove
// the health probe has actually stopped (not just that CloseOne
// returned nil, which — spec.md AC-004 — was already true before the
// fix).
func countingToolsListServer(toolName string, reqCount *int32) *httptest.Server {
	return httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(reqCount, 1)
		var raw map[string]any
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      raw["id"],
			"result": map[string]any{
				"tools": []any{map[string]any{"name": toolName}},
			},
		})
	}))
}

// httpRecipe builds a minimal transport:"http" recipe with no
// required env keys, so InstallRecipe's keychain-staging step is a
// no-op and the test can focus on pool wiring.
func httpRecipe(id, url string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		DisplayName: "Test " + id,
		Transport:   recipes.TransportHTTP,
		URL:         url,
	}
}

// waitForRequest polls reqCount until it is non-zero, or fails the
// test. Used to prove a probe (or an explicit Tools() call) actually
// reached the server before asserting anything stopped — an
// assertion that "no more requests arrived" is worthless if none
// ever arrived in the first place.
func waitForRequest(t *testing.T, reqCount *int32) int32 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := atomic.LoadInt32(reqCount); n > 0 {
			return n
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no request ever reached the server; the test proves nothing")
	return 0
}

// TestUninstallRecipe_HTTPTransport_TearsDownRealPool is FR-004 (a):
// uninstalling an http-transport recipe, driven through the real
// tools.API + dispatch.Pool + http.Pool stack, removes its tools from
// routing and stops its health-probe requests. Before UNIT-6,
// dispatch.Pool.CloseOne's http arm was comment-only and reported
// success without calling into http.Pool at all — this proves the
// RPC-facing entry point now reaches the real teardown, not just that
// the lower-level http.Pool.CloseOne unit tests pass in isolation.
func TestUninstallRecipe_HTTPTransport_TearsDownRealPool(t *testing.T) {
	var reqCount int32
	srv := countingToolsListServer("remote_tool", &reqCount)
	defer srv.Close()

	httpPool := mcphttp.NewPool(mcphttp.PoolOptions{
		PingPeriod: 15 * time.Millisecond, // real (short) cadence, real ticker: this test proves WIRING, not exact timing (that's pinned deterministically in core/mcp/transport/http/pool_test.go)
	})
	dispatchPool := dispatch.New(dispatch.Options{HTTP: httpPool})
	t.Cleanup(func() { _ = dispatchPool.Close(context.Background()) })

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{httpRecipe("remote-a", srv.URL)}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    dispatchPool,
		Secrets: backend,
		DataDir: t.TempDir(),
	})

	if _, err := api.InstallRecipe(context.Background(), "remote-a", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	// Tools() reaches the real server (the install actually spawned a
	// live connection, not a stub).
	toolsBefore, err := dispatchPool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools before uninstall: %v", err)
	}
	if len(toolsBefore) != 1 || toolsBefore[0].Server != "remote-a" {
		t.Fatalf("Tools before uninstall = %+v, want exactly remote-a's tool", toolsBefore)
	}

	// Confirm the probe is actually ticking before asserting it stops.
	before := waitForRequest(t, &reqCount)

	if err := api.UninstallRecipe(context.Background(), "remote-a"); err != nil {
		t.Fatalf("UninstallRecipe: %v", err)
	}

	// (a) tools gone from routing.
	toolsAfter, err := dispatchPool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools after uninstall: %v", err)
	}
	if len(toolsAfter) != 0 {
		t.Errorf("Tools after uninstall = %+v, want empty", toolsAfter)
	}

	// (c) the server receives no further request across a window well
	// past several probe periods — UninstallRecipe already returned,
	// so if the probe goroutine were still alive it would have had
	// many chances to tick by now.
	afterUninstall := atomic.LoadInt32(&reqCount)
	time.Sleep(150 * time.Millisecond)
	final := atomic.LoadInt32(&reqCount)
	if final != afterUninstall {
		t.Errorf("requests kept arriving after UninstallRecipe: %d -> %d (probe should have stopped)", afterUninstall, final)
	}
	t.Logf("requests before uninstall (proving the probe was alive) = %d; after the 150ms post-uninstall window = %d", before, final)

	// (b)/(c) restated via Call: the server is genuinely gone from
	// dispatch, not just absent from one aggregate listing.
	if _, err := dispatchPool.Call(context.Background(), "remote-a", "remote_tool", nil); err == nil {
		t.Error("Call after UninstallRecipe: want an error, got nil")
	}
}

// TestUninstallRecipe_HTTPTransport_LeavesUnrelatedInstallsAlone is
// the required negative half: a teardown that over-deletes is worse
// than one that under-deletes (CLAUDE.md's framing for this exact
// class of change). Two recipes are installed against two independent
// servers; uninstalling one must not touch the other's connection,
// tools, enabled-list entry, or keychain-staged credential.
func TestUninstallRecipe_HTTPTransport_LeavesUnrelatedInstallsAlone(t *testing.T) {
	var reqA, reqB int32
	srvA := countingToolsListServer("tool_a", &reqA)
	defer srvA.Close()
	srvB := countingToolsListServer("tool_b", &reqB)
	defer srvB.Close()

	httpPool := mcphttp.NewPool(mcphttp.PoolOptions{PingPeriod: -1}) // disable the probe: irrelevant to this assertion
	dispatchPool := dispatch.New(dispatch.Options{HTTP: httpPool})
	t.Cleanup(func() { _ = dispatchPool.Close(context.Background()) })

	recipeA := httpRecipe("remote-a", srvA.URL)
	recipeB := httpRecipe("remote-b", srvB.URL)
	recipeB.EnvKeys = []recipes.EnvKey{{Name: "REMOTE_B_TOKEN", Display: "Token", Required: true}}
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{recipeA, recipeB}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	keychain := &recordingKeychainForTest{backend: backend}
	api := tools.New(tools.Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     dispatchPool,
		Secrets:  backend,
		Keychain: keychain,
		DataDir:  t.TempDir(),
	})

	if _, err := api.InstallRecipe(context.Background(), "remote-a", nil, nil); err != nil {
		t.Fatalf("InstallRecipe(remote-a): %v", err)
	}
	if _, err := api.InstallRecipe(context.Background(), "remote-b", map[string]string{"REMOTE_B_TOKEN": "secret-b"}, nil); err != nil {
		t.Fatalf("InstallRecipe(remote-b): %v", err)
	}

	if err := api.UninstallRecipe(context.Background(), "remote-a"); err != nil {
		t.Fatalf("UninstallRecipe(remote-a): %v", err)
	}

	// remote-b's tool is still routed.
	toolsAfter, err := dispatchPool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(toolsAfter) != 1 || toolsAfter[0].Server != "remote-b" {
		t.Fatalf("Tools after uninstalling remote-a = %+v, want exactly remote-b's tool untouched", toolsAfter)
	}
	if _, err := dispatchPool.Call(context.Background(), "remote-b", "tool_b", nil); err != nil {
		t.Errorf("Call(remote-b) after uninstalling remote-a: unrelated install must still work, got %v", err)
	}

	// remote-b's enabled-list entry survives.
	if _, ok := enabled.Get("remote-b"); !ok {
		t.Error("enabled list lost remote-b after uninstalling remote-a")
	}
	if _, ok := enabled.Get("remote-a"); ok {
		t.Error("enabled list still has remote-a after uninstalling it")
	}

	// remote-b's keychain-staged credential survives untouched (only
	// ForgetRecipeKey should ever remove it — see UninstallRecipe's own
	// doc comment: "Keychain entries persist by design").
	locator := recipes.KeychainLocator("remote-b", "REMOTE_B_TOKEN")
	if _, err := backend.Resolve(context.Background(), secrets.CredentialReference{Kind: secrets.RefKeychain, Locator: locator}); err != nil {
		t.Errorf("remote-b's keychain credential was lost after uninstalling remote-a: %v", err)
	}
}

// TestInstallRecipe_HTTPTransport_ReinstallEvictsOldConnection is
// FR-004's re-install half (spec.md N-5's evict-before-spawn step,
// impl.go:390). It also RUNS (not just reads) the check this
// mission's report must make explicit: whether the claimed defect —
// "the pool accumulates a second entry and the first one's probe
// keeps running" — actually reproduces on this tree. It does not:
// http.Pool.openOne has, since v0.2.0 (git log -S evidence in the
// mission report), unconditionally replaced a same-name entry and
// stopped the OLD entry's probe on ANY re-Open, independent of
// whether CloseOne ran first — see
// core/mcp/transport/http/pool_test.go's
// TestPoolOpenReplacesExistingNameEvenWithoutCloseOne, which pins
// that fact directly against openOne with CloseOne never called at
// all. What this test adds on top: the full InstallRecipe path
// (evict-before-spawn CloseOne, THEN OpenOne) still works end-to-end
// after CloseOne stopped being a no-op, and still leaves exactly one
// live entry — no regression, and no accumulation either way.
func TestInstallRecipe_HTTPTransport_ReinstallEvictsOldConnection(t *testing.T) {
	var reqCount int32
	srv := countingToolsListServer("remote_tool", &reqCount)
	defer srv.Close()

	httpPool := mcphttp.NewPool(mcphttp.PoolOptions{PingPeriod: -1})
	dispatchPool := dispatch.New(dispatch.Options{HTTP: httpPool})
	t.Cleanup(func() { _ = dispatchPool.Close(context.Background()) })

	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{httpRecipe("remote-a", srv.URL)}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	api := tools.New(tools.Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    dispatchPool,
		Secrets: backend,
		DataDir: t.TempDir(),
	})

	if _, err := api.InstallRecipe(context.Background(), "remote-a", nil, nil); err != nil {
		t.Fatalf("first InstallRecipe: %v", err)
	}
	// Re-install the SAME recipe id without an intervening Uninstall —
	// exactly the evict-before-spawn path at impl.go:390.
	if _, err := api.InstallRecipe(context.Background(), "remote-a", nil, nil); err != nil {
		t.Fatalf("second InstallRecipe (re-install): %v", err)
	}

	toolsAfter, err := dispatchPool.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools after reinstall: %v", err)
	}
	if len(toolsAfter) != 1 {
		t.Fatalf("Tools after reinstall = %+v, want exactly one entry (no accumulation)", toolsAfter)
	}
	if enabled.List() == nil || len(enabled.List()) != 1 {
		t.Fatalf("enabled list after reinstall = %v, want exactly one entry", enabled.List())
	}
}

// recordingKeychainForTest is a minimal KeychainWriter that stages
// plaintext into the shared secrets.MemoryBackend, mirroring the
// production keychainWriter shape closely enough for this test's
// purposes (core/rpc's real adapter also touches the OS keychain,
// which this package cannot depend on).
type recordingKeychainForTest struct {
	backend *secrets.MemoryBackend
}

func (k *recordingKeychainForTest) Write(_ context.Context, locator string, plaintext []byte) error {
	if k.backend != nil {
		k.backend.SetEntry(secrets.RefKeychain, locator, plaintext)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}
