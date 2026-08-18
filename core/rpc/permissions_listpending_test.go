package rpc

// Permission-prompt rehydration WIRING test (consent-surfaces-truth
// -01PMTR01 WP03 / dead-code-audit finding A11).
//
// Permissions_ListPending existed end to end on the Go side — bindings.go,
// the view, cedar.Registry.ListPending — with ZERO frontend callers. A
// permission prompt lost across a reload never returned: the backend
// goroutine stayed parked inside cedar.Registry.RequestInteractive and the
// turn hung until the registry's 5-minute PromptTimeout fired and
// fail-closed denied it.
//
// This test drives the REAL registry a live gate would use — the same
// process-singleton *cedar.Registry api.go wires into every gate site
// (api.go:1127) — parks a real RequestInteractive call, proves the pending
// row surfaces through Bindings.Permissions_ListPending with the exact
// same flattened shape the live `bash:permission-pending` topic pushes,
// then resolves it through Bindings.Permissions_Resolve and asserts the
// parked call returns PROMPTLY (well under the multi-minute timeout) with
// the resolved decision — i.e. the turn completes instead of timing out.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestPermissionsListPending_RehydratesLiveShapeAndResolveCompletesTheTurn
// is FR-004/FR-005's headline acceptance test.
//
// Mutation: delete the onMounted reconcile call — this is a frontend
//
//	mutation, pinned separately by the Vitest suite; this Go test instead
//	pins the SERVER half: that ListPending returns something to reconcile
//	against and that Resolve genuinely reaches the parked call.
//
// Mutation: return the raw cedar.PendingRequest from the binding
//
//	unflattened (revert the []FlatPermissionRequest change) → this test
//	fails to compile, which is the strongest possible mutation signal for
//	FR-005's "no second projection" rule.
//
// Mutation: point Resolve at a fresh/wrong request id → the parked
//
//	RequestInteractive call never returns and the test times out on the
//	select, which is exactly the bug (the turn hangs) surfacing as a
//	failing test rather than a silent 5-minute hang in production.
func TestPermissionsListPending_RehydratesLiveShapeAndResolveCompletesTheTurn(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	bnd := NewBindings(api)

	reg := api.PromptRegistry()
	if reg == nil {
		t.Fatal("nil prompt registry — every gate site depends on this being non-nil")
	}

	surface := cedar.PromptSurface{
		Bash: &cedar.BashPromptSurface{
			Pattern:   "git *",
			Argv:      []string{"git", "push", "--force"},
			Dangerous: true,
		},
		SessionID: "s-listpending-1",
	}

	type outcome struct {
		res cedar.Resolution
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := reg.RequestInteractive(context.Background(), surface)
		done <- outcome{res: res, err: err}
	}()

	// Poll ListPending until the parked request shows up (it is posted to
	// the registry's pending map before the goroutine blocks, so this is
	// fast — a short poll avoids a fixed sleep that could flake under load).
	var pending []FlatPermissionRequest
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err = bnd.Permissions_ListPending()
		if err != nil {
			t.Fatalf("Permissions_ListPending: %v", err)
		}
		if len(pending) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("Permissions_ListPending: got %d rows, want 1 — the parked request never surfaced", len(pending))
	}
	got := pending[0]

	// ── FR-005: byte-identical to the live topic payload for the same
	// request. The dispatcher wired at api.go:1127-1139 calls
	// FlattenPendingRequest(payload) on the exact same underlying
	// cedar.PendingRequest this test's RequestInteractive call created.
	// Fetch the raw entry back out via the registry's own ListPending and
	// flatten it the SAME way, then assert field-for-field equality.
	raw := reg.ListPending()
	if len(raw) != 1 {
		t.Fatalf("registry.ListPending(): got %d, want 1", len(raw))
	}
	want := FlattenPendingRequest(raw[0])
	if got.RequestID != want.RequestID ||
		got.Family != want.Family ||
		got.ResourceDisplay != want.ResourceDisplay ||
		got.ResourceUID != want.ResourceUID ||
		got.DangerousTier != want.DangerousTier ||
		got.Surface != want.Surface {
		t.Fatalf("Permissions_ListPending row does not match FlattenPendingRequest(raw) — a second\n"+
			"projection has crept in, which FR-005 forbids.\ngot:  %#v\nwant: %#v", got, want)
	}
	if got.Family != "bash" {
		t.Errorf("Family = %q, want %q", got.Family, "bash")
	}
	if got.ResourceDisplay != "git push --force" {
		t.Errorf("ResourceDisplay = %q, want %q (full argv, not just the pattern)", got.ResourceDisplay, "git push --force")
	}
	if !got.DangerousTier {
		t.Errorf("DangerousTier = false, want true")
	}

	// ── FR-004: resolving from the rehydrated request reaches
	// Registry.Resolve and the turn completes rather than timing out.
	if err := bnd.Permissions_Resolve(got.RequestID, string(cedar.DecisionAllowOnce)); err != nil {
		t.Fatalf("Permissions_Resolve: %v", err)
	}

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("RequestInteractive returned an error: %v", o.err)
		}
		if o.res.Decision != cedar.DecisionAllowOnce {
			t.Fatalf("RequestInteractive resolved with Decision=%q, want %q", o.res.Decision, cedar.DecisionAllowOnce)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestInteractive did not return within 2s of Resolve — the parked goroutine was not " +
			"reached, which is the exact production defect (a 5-minute hang) this WP fixes")
	}

	// The registry must show nothing pending now — the resolve was a
	// real answer, not a stray call to an unrelated id.
	if left, _ := bnd.Permissions_ListPending(); len(left) != 0 {
		t.Errorf("Permissions_ListPending after resolve: got %d rows, want 0: %#v", len(left), left)
	}
}
