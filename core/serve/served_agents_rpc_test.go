package serve_test

// served_agents_rpc_test.go drives the Agents_* family through the REAL
// served transport: real HTTP, real serve.dispatch, real rpc.API over
// core.New. contracts/agents-served-rpc.md is the contract under test.

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	agentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agents"
	"github.com/kameas-ai/kenaz-harness/core/serve"

	"context"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
)

func newAgentsHarness(t *testing.T) (baseURL string) {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir(), WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := rpc.New(c)
	t.Cleanup(api.Shutdown)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	srv := serve.New(api, addr, "tok", nil, nil)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	waitForListener(t, addr)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	})
	return "http://" + addr
}

func TestServedAgents_CRUDEndToEnd(t *testing.T) {
	baseURL := newAgentsHarness(t)

	// ListProfiles: bundled profiles ship regardless of DataDir contents.
	var initial []agentsview.ProfileSummaryWire
	rpcCall(t, baseURL, "Agents_ListProfiles", map[string]any{}, &initial)
	if len(initial) == 0 {
		t.Fatal("Agents_ListProfiles returned no profiles — bundled set should be non-empty")
	}
	for _, p := range initial {
		if !p.Bundled {
			t.Fatalf("fresh DataDir has a non-bundled profile in the list: %+v", p)
		}
	}

	// SaveProfile: create a user-authored profile.
	custom := agentsview.ProfileWire{
		ID:           "served-test-profile",
		Name:         "Served Test Profile",
		Description:  "created via the served transport",
		Model:        "anthropic/claude-sonnet-4.5",
		AutonomyTier: "default",
		MergePolicy:  "auto",
	}
	rpcCall(t, baseURL, "Agents_SaveProfile", custom, nil)

	// LoadProfile: round-trips exactly what was saved.
	var loaded agentsview.ProfileWire
	rpcCall(t, baseURL, "Agents_LoadProfile", map[string]any{"id": "served-test-profile"}, &loaded)
	if loaded.Name != custom.Name || loaded.Model != custom.Model || loaded.Bundled {
		t.Fatalf("loaded = %+v, want a non-bundled round-trip of %+v", loaded, custom)
	}

	// ListProfiles now includes it.
	var afterSave []agentsview.ProfileSummaryWire
	rpcCall(t, baseURL, "Agents_ListProfiles", map[string]any{}, &afterSave)
	if len(afterSave) != len(initial)+1 {
		t.Fatalf("after save: %d profiles, want %d", len(afterSave), len(initial)+1)
	}

	// DeleteProfile: removes it.
	rpcCall(t, baseURL, "Agents_DeleteProfile", map[string]any{"id": "served-test-profile"}, nil)
	var afterDelete []agentsview.ProfileSummaryWire
	rpcCall(t, baseURL, "Agents_ListProfiles", map[string]any{}, &afterDelete)
	if len(afterDelete) != len(initial) {
		t.Fatalf("after delete: %d profiles, want %d", len(afterDelete), len(initial))
	}

	// LoadProfile on the now-deleted id fails.
	err := rpcCallErr(t, baseURL, "Agents_LoadProfile", map[string]any{"id": "served-test-profile"}, nil)
	if err == "" || !strings.Contains(err, "not found") {
		t.Fatalf("LoadProfile after delete: err = %q, want a not-found error", err)
	}
}

// TestServedAgents_BundledProfilesStayReadOnly proves the served transport
// enforces the SAME bundled-read-only guard as desktop — it is not
// re-implemented at the serve.Server layer, it flows through to
// core/agents unchanged.
func TestServedAgents_BundledProfilesStayReadOnly(t *testing.T) {
	baseURL := newAgentsHarness(t)

	var profiles []agentsview.ProfileSummaryWire
	rpcCall(t, baseURL, "Agents_ListProfiles", map[string]any{}, &profiles)
	var bundledID string
	for _, p := range profiles {
		if p.Bundled {
			bundledID = p.ID
			break
		}
	}
	if bundledID == "" {
		t.Fatal("no bundled profile found to test against")
	}

	var full agentsview.ProfileWire
	rpcCall(t, baseURL, "Agents_LoadProfile", map[string]any{"id": bundledID}, &full)
	full.Description = "attempted overwrite via served transport"

	err := rpcCallErr(t, baseURL, "Agents_SaveProfile", full, nil)
	if err == "" || !strings.Contains(err, "read-only") {
		t.Fatalf("SaveProfile over a bundled id: err = %q, want a read-only rejection", err)
	}

	err = rpcCallErr(t, baseURL, "Agents_DeleteProfile", map[string]any{"id": bundledID}, nil)
	if err == "" || !strings.Contains(err, "read-only") {
		t.Fatalf("DeleteProfile on a bundled id: err = %q, want a read-only rejection", err)
	}

	// The bundled profile is unchanged.
	var stillThere agentsview.ProfileWire
	rpcCall(t, baseURL, "Agents_LoadProfile", map[string]any{"id": bundledID}, &stillThere)
	if stillThere.Description == full.Description {
		t.Fatal("bundled profile description was mutated by the rejected SaveProfile call")
	}
}

// TestServedAgents_DeleteRejectsPathTraversal proves the ID-traversal guard
// (core/agents/loader.go Delete) is reachable and enforced from the
// served HTTP transport, not just from a direct Go call — this is the
// surface contracts/agents-served-rpc.md §2.4 is about: exposing Delete
// over the network is exactly what turns a latent gap into a real one.
func TestServedAgents_DeleteRejectsPathTraversal(t *testing.T) {
	baseURL := newAgentsHarness(t)

	err := rpcCallErr(t, baseURL, "Agents_DeleteProfile", map[string]any{"id": "../canary"}, nil)
	if err == "" {
		t.Fatal("Agents_DeleteProfile(../canary) over HTTP: expected a rejection, got none")
	}
	if !strings.Contains(err, "invalid id") {
		t.Fatalf("Agents_DeleteProfile(../canary): err = %q, want the invalid-id rejection", err)
	}
}

// Serve_ListMethods advertises the whole family (contract §5).
func TestServedAgents_ListedInServeMethods(t *testing.T) {
	baseURL := newAgentsHarness(t)
	var methods struct {
		RPC []string `json:"rpc"`
	}
	rpcCall(t, baseURL, "Serve_ListMethods", nil, &methods)
	joined := strings.Join(methods.RPC, ",")
	for _, m := range []string{"Agents_ListProfiles", "Agents_LoadProfile", "Agents_SaveProfile", "Agents_DeleteProfile"} {
		if !strings.Contains(joined, m) {
			t.Errorf("Serve_ListMethods missing %s", m)
		}
	}
}
