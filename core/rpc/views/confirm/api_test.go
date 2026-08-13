package confirm

// Confirm RPC view tests (confirm-each-enforcement-01PMAG05 WP02/WP03).
//
// The view is thin by design, so these tests are about the few places it
// is allowed to have an opinion: dismissal must deny, "remember" must
// not ride a denial, an unwired bus must say so rather than pretend, and
// a bus-less surface must still answer ListPending without panicking.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// parked starts a Pending on the bus and returns a channel carrying the
// decision the parked caller eventually receives. Mirrors what the chat
// adapter does: park and block.
func parked(t *testing.T, bus *toolloop.ConfirmBus, req toolloop.ConfirmRequest) <-chan toolloop.ConfirmDecision {
	t.Helper()
	out := make(chan toolloop.ConfirmDecision, 1)
	go func() {
		d, err := bus.Pending(context.Background(), req)
		if err != nil {
			// The test's teardown cancels nothing, so an error here is a
			// real failure; surface it as a zero decision and let the
			// assertion report the mismatch.
			out <- toolloop.ConfirmDecision{}
			return
		}
		out <- d
	}()
	return out
}

func awaitParked(t *testing.T, bus *toolloop.ConfirmBus, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bus.PendingCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d parked call(s); have %d", n, bus.PendingCount())
}

func req(session, call, batch, tool string) toolloop.ConfirmRequest {
	return toolloop.ConfirmRequest{
		SessionID:   session,
		CallID:      call,
		BatchID:     batch,
		Server:      "filesystem",
		Tool:        tool,
		ArgsSummary: "1 argument: path (string)",
	}
}

func TestResolve_ApproveAndDeny(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(func(toolloop.ConfirmRequest) {})
	api := New(Config{Bus: bus})

	ok := parked(t, bus, req("s", "c-yes", "b", "write_file"))
	no := parked(t, bus, req("s", "c-no", "b", "delete_file"))
	awaitParked(t, bus, 2)

	if err := api.Resolve(context.Background(), "s", "c-yes", true, "approved", false); err != nil {
		t.Fatalf("Resolve(approve): %v", err)
	}
	if err := api.Resolve(context.Background(), "s", "c-no", false, "nope", false); err != nil {
		t.Fatalf("Resolve(deny): %v", err)
	}

	if d := <-ok; !d.Approved || d.Reason != "approved" {
		t.Fatalf("approved row got %+v", d)
	}
	if d := <-no; d.Approved {
		t.Fatalf("denied row was approved: %+v", d)
	}
}

// "Deny, but remember to allow" is not a thing a user can mean, and the
// boundary the frontend calls is the cheapest place to be sure. The
// dispatcher guards it too; this is the belt to that pair of braces.
func TestResolve_RememberNeverRidesADenial(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	got := parked(t, bus, req("s", "c1", "b", "write_file"))
	awaitParked(t, bus, 1)

	// A caller asking to remember a DENIAL.
	if err := api.Resolve(context.Background(), "s", "c1", false, "no", true); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	d := <-got
	if d.Approved {
		t.Fatal("denial was approved")
	}
	if d.RememberSession {
		t.Fatal("RememberSession survived a denial — a session grant would be written for a tool the user just refused")
	}
}

func TestResolveAlways_ApprovesAndFlagsPersist(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	got := parked(t, bus, req("s", "c1", "b", "write_file"))
	awaitParked(t, bus, 1)

	if err := api.ResolveAlways(context.Background(), "s", "c1", "always"); err != nil {
		t.Fatalf("ResolveAlways: %v", err)
	}
	d := <-got
	if !d.Approved {
		t.Fatal("always-allow did not approve")
	}
	if !d.Persist {
		t.Fatal("always-allow did not request a durable rule")
	}
}

func TestApproveBatch_ResolvesEveryRow(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	a := parked(t, bus, req("s", "c1", "batch-1", "write_file"))
	b := parked(t, bus, req("s", "c2", "batch-1", "create_issue"))
	// A row in a DIFFERENT batch must not be touched by approve-all.
	other := parked(t, bus, req("s", "c3", "batch-2", "read_file"))
	awaitParked(t, bus, 3)

	n, err := api.ApproveBatch(context.Background(), "batch-1", false)
	if err != nil {
		t.Fatalf("ApproveBatch: %v", err)
	}
	if n != 2 {
		t.Fatalf("ApproveBatch resolved %d rows, want 2", n)
	}
	if d := <-a; !d.Approved {
		t.Error("row c1 not approved")
	}
	if d := <-b; !d.Approved {
		t.Error("row c2 not approved")
	}
	if bus.PendingCount() != 1 {
		t.Fatalf("PendingCount = %d, want 1 (the other batch is still parked)", bus.PendingCount())
	}
	_, _ = api.CancelBatch(context.Background(), "batch-2", "teardown")
	<-other
}

// FR-003: dismissal is deny-all, never allow-all. This is the assertion
// that would have to be deleted for a dismissal to start approving, which
// is the point of writing it this way.
func TestCancelBatch_DeniesEveryRow(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	a := parked(t, bus, req("s", "c1", "batch-1", "write_file"))
	b := parked(t, bus, req("s", "c2", "batch-1", "delete_file"))
	awaitParked(t, bus, 2)

	n, err := api.CancelBatch(context.Background(), "batch-1", "")
	if err != nil {
		t.Fatalf("CancelBatch: %v", err)
	}
	if n != 2 {
		t.Fatalf("CancelBatch resolved %d rows, want 2", n)
	}
	for i, ch := range []<-chan toolloop.ConfirmDecision{a, b} {
		d := <-ch
		if d.Approved {
			t.Fatalf("row %d was APPROVED by a dismissal", i)
		}
		if d.Reason == "" {
			t.Errorf("row %d denial carried no reason — the model gets an unexplained tool error", i)
		}
	}
}

// A batch already fully answered row-by-row cancels zero rows. The
// frontend calls cancelBatch on unmount, so this is the common case, not
// an edge one, and it must not error.
func TestCancelBatch_AfterIndividualAnswersIsANoop(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	got := parked(t, bus, req("s", "c1", "batch-1", "write_file"))
	awaitParked(t, bus, 1)

	if err := api.Resolve(context.Background(), "s", "c1", true, "ok", false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	<-got

	n, err := api.CancelBatch(context.Background(), "batch-1", "dismissed")
	if err != nil {
		t.Fatalf("CancelBatch after answering: %v", err)
	}
	if n != 0 {
		t.Fatalf("CancelBatch resolved %d rows, want 0", n)
	}
}

func TestResolve_UnknownRow(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})

	if err := api.Resolve(context.Background(), "s", "nope", true, "", false); !errors.Is(err, ErrUnknownConfirmation) {
		t.Fatalf("Resolve(unknown) = %v, want ErrUnknownConfirmation", err)
	}
	// An empty call id is a frontend bug, not a row.
	if err := api.Resolve(context.Background(), "s", "", true, "", false); !errors.Is(err, ErrUnknownConfirmation) {
		t.Fatalf("Resolve(empty id) = %v, want ErrUnknownConfirmation", err)
	}
	// The re-export is the same value, so errors.Is matches across the
	// package boundary and callers need not import toolloop to classify.
	if !errors.Is(ErrUnknownConfirmation, toolloop.ErrUnknownConfirmation) {
		t.Fatal("the view's sentinel is not the bus's sentinel")
	}
}

func TestListPending_SnapshotForReconnect(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	a := parked(t, bus, req("s1", "c1", "b1", "write_file"))
	b := parked(t, bus, req("s2", "c2", "b2", "read_file"))
	awaitParked(t, bus, 2)

	all, err := api.ListPending(context.Background(), "")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListPending(all) = %d rows, want 2", len(all))
	}
	one, err := api.ListPending(context.Background(), "s1")
	if err != nil {
		t.Fatalf("ListPending(s1): %v", err)
	}
	if len(one) != 1 || one[0].SessionID != "s1" {
		t.Fatalf("ListPending(s1) = %+v", one)
	}
	// The snapshot carries the redaction-safe summary, never values.
	if one[0].ArgsSummary != "1 argument: path (string)" {
		t.Fatalf("ArgsSummary = %q", one[0].ArgsSummary)
	}

	_, _ = api.CancelBatch(context.Background(), "b1", "teardown")
	_, _ = api.CancelBatch(context.Background(), "b2", "teardown")
	<-a
	<-b
}

// A chassis built without the chat stack has no bus. The surface must
// say so on the mutating paths and answer empty on the read path — an
// honest "nothing is wired" rather than a nil dereference or a fake
// success that leaves a caller believing a row was answered.
func TestBusUnavailable(t *testing.T) {
	t.Parallel()

	api := New(Config{})
	ctx := context.Background()

	if err := api.Resolve(ctx, "s", "c", true, "", false); !errors.Is(err, ErrBusUnavailable) {
		t.Errorf("Resolve = %v, want ErrBusUnavailable", err)
	}
	if err := api.ResolveAlways(ctx, "s", "c", ""); !errors.Is(err, ErrBusUnavailable) {
		t.Errorf("ResolveAlways = %v, want ErrBusUnavailable", err)
	}
	if _, err := api.ApproveBatch(ctx, "b", false); !errors.Is(err, ErrBusUnavailable) {
		t.Errorf("ApproveBatch = %v, want ErrBusUnavailable", err)
	}
	if _, err := api.CancelBatch(ctx, "b", ""); !errors.Is(err, ErrBusUnavailable) {
		t.Errorf("CancelBatch = %v, want ErrBusUnavailable", err)
	}
	got, err := api.ListPending(ctx, "")
	if err != nil {
		t.Errorf("ListPending = %v, want nil error", err)
	}
	if got == nil {
		t.Error("ListPending returned nil — the frontend JSON-encodes this and null is not an empty list")
	}
	if len(got) != 0 {
		t.Errorf("ListPending = %+v, want empty", got)
	}
}

// Rows from one turn resolve independently: approving row 2 dispatches
// row 2 while rows 1 and 3 stay parked. The batched modal's per-row
// buttons depend on this.
func TestRowsInABatchResolveIndependently(t *testing.T) {
	t.Parallel()

	bus := toolloop.NewConfirmBus(nil)
	api := New(Config{Bus: bus})
	chans := make([]<-chan toolloop.ConfirmDecision, 3)
	for i, id := range []string{"c1", "c2", "c3"} {
		chans[i] = parked(t, bus, req("s", id, "batch-1", "tool"+id))
	}
	awaitParked(t, bus, 3)

	if err := api.Resolve(context.Background(), "s", "c2", true, "ok", false); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if d := <-chans[1]; !d.Approved {
		t.Fatal("row c2 not approved")
	}
	if n := bus.PendingCount(); n != 2 {
		t.Fatalf("PendingCount = %d after one row resolved, want 2", n)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-chans[0] }()
	go func() { defer wg.Done(); <-chans[2] }()
	if n, _ := api.CancelBatch(context.Background(), "batch-1", "dismissed"); n != 2 {
		t.Fatalf("CancelBatch resolved %d rows, want 2", n)
	}
	wg.Wait()
}
