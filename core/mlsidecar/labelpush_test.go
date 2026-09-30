package mlsidecar

// labelpush_test.go — WP14 proofs, all against REAL sqlite (the
// production storage.Open migration path, so migration 1601 is what
// creates the revision column and the cursor table) and the stub sidecar
// (sidecar_stub_test.go). No wall-clock assertions.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice/labels"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

type pushHarness struct {
	dir   string
	db    storage.DB
	store *labels.SQLStore
}

func openPushHarness(t *testing.T, dir string) *pushHarness {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("storage open: %v", err)
	}
	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := db.(sqlHandle)
	if !ok || h.SQL() == nil {
		t.Fatal("storage.DB exposes no SQL() handle")
	}
	return &pushHarness{dir: dir, db: db, store: labels.NewSQLStore(h.SQL())}
}

func (h *pushHarness) close(t *testing.T) {
	t.Helper()
	if err := h.db.Close(context.Background()); err != nil {
		t.Fatalf("storage close: %v", err)
	}
}

func (h *pushHarness) insert(t *testing.T, kind, hash string, ts int64) {
	t.Helper()
	err := h.store.Insert(context.Background(), labels.Row{
		KindID: kind, PromptVersion: "v1", FeaturesHash: hash, FeaturesJSON: `{"x":1}`,
		FeaturesComplete: true, ModelID: "heuristic/x-v1", Rung: "heuristic", Decision: true,
		Confidence: 80, Shown: true, UserAction: labels.ActionIgnored, SessionID: "sess",
		CreatedAt: time.UnixMilli(ts).UTC(),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
}

func newPusher(stub *stubSidecar, store labels.PushSource, healthy bool) *LabelPusher {
	return &LabelPusher{
		Client:  NewClient(stub.URL(), nil),
		Source:  store,
		Healthy: func() bool { return healthy },
	}
}

// TestLabelPush_CursorResumesAcrossRestart: push, close the DB, reopen the
// same data dir, add one row — the new pusher sends ONLY the new row.
func TestLabelPush_CursorResumesAcrossRestart(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	dir := t.TempDir()

	h := openPushHarness(t, dir)
	h.insert(t, "branch_now", "h1", 1000)
	h.insert(t, "branch_now", "h2", 1001)
	res, err := newPusher(stub, h.store, true).PushOnce(context.Background())
	if err != nil || res.Pushed != 2 {
		t.Fatalf("first push = %+v, err %v; want 2 pushed", res, err)
	}
	h.close(t)

	h2 := openPushHarness(t, dir)
	defer h2.close(t)
	h2.insert(t, "branch_now", "h3", 1002)
	before := stub.labelPostCount()
	res, err = newPusher(stub, h2.store, true).PushOnce(context.Background())
	if err != nil || res.Pushed != 1 {
		t.Fatalf("post-restart push = %+v, err %v; want exactly 1 (the new row)", res, err)
	}
	posts := stub.labelPostSnapshot()
	if len(posts) != before+1 || len(posts[len(posts)-1].Rows) != 1 || posts[len(posts)-1].Rows[0].FeaturesHash != "h3" {
		t.Fatalf("post-restart POST re-sent already-acked rows: %+v", posts[len(posts)-1])
	}
	if got := len(stub.mirrorSnapshot()); got != 3 {
		t.Errorf("mirror holds %d rows, want 3", got)
	}
}

// TestLabelPush_IdempotentDoublePush: a second push with nothing new makes
// no POST, and a forced replay (cursor reset) changes nothing in the
// mirror — the engine's (client, kind, features_hash, ts) upsert absorbs it.
func TestLabelPush_IdempotentDoublePush(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	h.insert(t, "branch_now", "h1", 1000)
	h.insert(t, "compact_now", "h2", 1001)
	p := newPusher(stub, h.store, true)
	ctx := context.Background()

	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 2 {
		t.Fatalf("first push = %+v, %v", res, err)
	}
	posts := stub.labelPostCount()
	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 0 || res.Batches != 0 {
		t.Fatalf("second push = %+v, %v; want a no-op", res, err)
	}
	if stub.labelPostCount() != posts {
		t.Errorf("a push with nothing new still POSTed")
	}

	mirror1 := stub.mirrorSnapshot()
	if err := p.ResetCursor(ctx); err != nil {
		t.Fatal(err)
	}
	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 2 {
		t.Fatalf("replay push = %+v, %v; want a full re-push of 2", res, err)
	}
	mirror2 := stub.mirrorSnapshot()
	if len(mirror2) != len(mirror1) {
		t.Fatalf("replay changed mirror cardinality: %d -> %d", len(mirror1), len(mirror2))
	}
	for k, v := range mirror1 {
		if mirror2[k].Revision != v.Revision {
			t.Errorf("replay changed revision of %s: %d -> %d", k, v.Revision, mirror2[k].Revision)
		}
	}
}

// TestLabelPush_UpdatedRowRepushesWithHigherRevisionAndReplaces: a
// dismissal after the first push re-pushes the row with a HIGHER revision
// and the engine's stored row is replaced (same key, new user_action).
func TestLabelPush_UpdatedRowRepushesWithHigherRevisionAndReplaces(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	h.insert(t, "branch_now", "h2", 1001)
	p := newPusher(stub, h.store, true)
	if _, err := p.PushOnce(ctx); err != nil {
		t.Fatal(err)
	}
	key := "harness|branch_now|h1|1000"
	first := stub.mirrorSnapshot()[key]
	if first.UserAction != "ignored" {
		t.Fatalf("first-push action = %q, want ignored", first.UserAction)
	}

	if err := h.store.UpdateAction(ctx, "sess", "branch_now", "h1", labels.ActionDismissed); err != nil {
		t.Fatal(err)
	}
	res, err := p.PushOnce(ctx)
	if err != nil || res.Pushed != 1 {
		t.Fatalf("re-push = %+v, %v; want exactly the updated row", res, err)
	}
	after := stub.mirrorSnapshot()[key]
	if after.UserAction != "dismissed" {
		t.Errorf("engine row action = %q, want dismissed (higher revision replaces)", after.UserAction)
	}
	if after.Revision <= first.Revision {
		t.Errorf("re-push revision %d not above first push %d", after.Revision, first.Revision)
	}
	if after.TS != 1000 {
		t.Errorf("re-push ts = %d, want the original 1000 (ts is part of the upsert key)", after.TS)
	}
	if len(stub.mirrorSnapshot()) != 2 {
		t.Errorf("update created a second engine row instead of replacing")
	}
}

// TestLabelPush_UnhealthyPushesNothingAndLosesNothing.
func TestLabelPush_UnhealthyPushesNothingAndLosesNothing(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	h.insert(t, "branch_now", "h2", 1001)

	var healthy atomic.Bool
	p := &LabelPusher{Client: NewClient(stub.URL(), nil), Source: h.store, Healthy: healthy.Load}

	res, err := p.PushOnce(ctx)
	if err != nil || res.Skipped != "sidecar_unhealthy" || res.Pushed != 0 {
		t.Fatalf("unhealthy push = %+v, %v", res, err)
	}
	if stub.labelPostCount() != 0 {
		t.Fatalf("unhealthy sidecar received %d label POSTs, want 0", stub.labelPostCount())
	}
	// A pusher with NO health signal is also silent (never guesses).
	silent := &LabelPusher{Client: NewClient(stub.URL(), nil), Source: h.store}
	if res, _ := silent.PushOnce(ctx); res.Skipped != "sidecar_unhealthy" || stub.labelPostCount() != 0 {
		t.Fatalf("nil-Healthy pusher pushed: %+v", res)
	}

	// Recovery: nothing was lost; the whole backlog arrives.
	healthy.Store(true)
	res, err = p.PushOnce(ctx)
	if err != nil || res.Pushed != 2 {
		t.Fatalf("recovery push = %+v, %v; want the full backlog of 2", res, err)
	}
}

// TestLabelPush_EngineFailureKeepsCursor: a failing POST loses nothing.
func TestLabelPush_EngineFailureKeepsCursor(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	p := newPusher(stub, h.store, true)

	stub.setLabelsFail(http.StatusInternalServerError)
	if _, err := p.PushOnce(ctx); err == nil {
		t.Fatal("PushOnce against a failing engine returned nil error")
	}
	cur, _ := h.store.LoadCursor(ctx, "sidecar", "branch_now")
	if (cur != labels.PushCursor{}) {
		t.Fatalf("cursor advanced on a failed POST: %+v", cur)
	}
	stub.setLabelsFail(0)
	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 1 {
		t.Fatalf("retry = %+v, %v; want the row delivered", res, err)
	}
}

// TestLabelPush_CaptureOffPushesNothing is the call-count proof: with the
// capture toggle off the pusher makes ZERO store calls and ZERO HTTP calls.
func TestLabelPush_CaptureOffPushesNothing(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	src := &countingSource{}
	p := &LabelPusher{
		Client:  NewClient(stub.URL(), nil),
		Source:  src,
		Enabled: func() bool { return false },
		Healthy: func() bool { return true },
	}
	res, err := p.PushOnce(context.Background())
	if err != nil || res.Skipped != "capture_disabled" {
		t.Fatalf("PushOnce = %+v, %v; want capture_disabled", res, err)
	}
	p.Nudge()
	if src.calls.Load() != 0 {
		t.Errorf("capture off yet the label store was called %d times", src.calls.Load())
	}
	if stub.labelPostCount() != 0 {
		t.Errorf("capture off yet %d label POSTs reached the sidecar", stub.labelPostCount())
	}
}

// TestLabelPush_PartialAckResumesFromAck: an engine that only acks part of
// a batch advances the cursor to the ack, and the remainder is re-sent.
func TestLabelPush_PartialAckResumesFromAck(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	for i, hash := range []string{"h1", "h2", "h3"} {
		h.insert(t, "branch_now", hash, int64(1000+i))
	}
	stub.setLabelsAckLimit(1)
	p := newPusher(stub, h.store, true)
	res, err := p.PushOnce(ctx)
	if err != nil || res.Pushed != 3 {
		t.Fatalf("PushOnce = %+v, %v; want all 3 delivered over repeated acks", res, err)
	}
	if got := len(stub.mirrorSnapshot()); got != 3 {
		t.Errorf("mirror = %d rows, want 3", got)
	}
}

// TestLabelPush_BogusAckIsRefused: an ack outside the pushed window must
// not move the cursor (it could skip unsent rows).
func TestLabelPush_BogusAckIsRefused(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	stub.setLabelsBogusAck(&LabelAck{TS: 1, Revision: 999999})
	if _, err := newPusher(stub, h.store, true).PushOnce(ctx); err == nil {
		t.Fatal("a bogus ack was accepted")
	}
	cur, _ := h.store.LoadCursor(ctx, "sidecar", "branch_now")
	if (cur != labels.PushCursor{}) {
		t.Errorf("cursor moved on a bogus ack: %+v", cur)
	}
}

// TestLabelPush_FullRepushAfterMirrorLoss: the engine's mirror is wiped;
// ResetCursor + PushOnce rebuilds it from the harness DB.
func TestLabelPush_FullRepushAfterMirrorLoss(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	h.insert(t, "branch_now", "h1", 1000)
	h.insert(t, "escalate_model", "h2", 1001)
	p := newPusher(stub, h.store, true)
	if _, err := p.PushOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stub.dropLabelMirror()
	if len(stub.mirrorSnapshot()) != 0 {
		t.Fatal("mirror not dropped")
	}
	if err := p.ResetCursor(ctx); err != nil {
		t.Fatal(err)
	}
	if res, err := p.PushOnce(ctx); err != nil || res.Pushed != 2 {
		t.Fatalf("rebuild = %+v, %v; want 2", res, err)
	}
	if got := len(stub.mirrorSnapshot()); got != 2 {
		t.Errorf("rebuilt mirror = %d rows, want 2", got)
	}
}

// TestLabelPush_CarriesFeaturesCompleteVerbatim.
func TestLabelPush_CarriesFeaturesCompleteVerbatim(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	ctx := context.Background()
	if err := h.store.Insert(ctx, labels.Row{
		KindID: "compact_now", PromptVersion: "v1", FeaturesHash: "hp", FeaturesJSON: `{"placeholder":true}`,
		FeaturesComplete: false, ModelID: "m", Rung: "heuristic", SessionID: "s", UserAction: labels.ActionIgnored,
		CreatedAt: time.UnixMilli(5).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPusher(stub, h.store, true).PushOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := stub.mirrorSnapshot()["harness|compact_now|hp|5"]
	if got.FeaturesComplete {
		t.Error("features_complete=false was not carried verbatim")
	}
	if string(got.Features) != `{"placeholder":true}` {
		t.Errorf("features = %s", got.Features)
	}
}

// TestLabelPush_LoopbackOnly: a non-loopback base URL is refused before
// any connection is attempted — the lane has no egress path.
func TestLabelPush_LoopbackOnly(t *testing.T) {
	var dials atomic.Int32
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		dials.Add(1)
		return nil, errors.New("must never be dialed")
	})
	for _, base := range []string{"http://example.com:7774", "https://10.1.2.3", "http://192.168.0.5:7774", "not a url", "http://[2001:db8::1]:80"} {
		p := &LabelPusher{
			Client:  NewClient(base, &http.Client{Transport: rt}),
			Source:  &countingSource{},
			Healthy: func() bool { return true },
		}
		if _, err := p.PushOnce(context.Background()); !errors.Is(err, ErrNotLoopback) {
			t.Errorf("base %q: err = %v, want ErrNotLoopback", base, err)
		}
	}
	if dials.Load() != 0 {
		t.Errorf("%d requests left the pusher toward a non-loopback address", dials.Load())
	}
	for _, ok := range []string{DefaultBaseURL, "http://localhost:7774", "http://[::1]:7774", "http://127.0.0.2:1"} {
		if err := checkLoopbackURL(ok); err != nil {
			t.Errorf("loopback %q refused: %v", ok, err)
		}
	}
}

// TestLabelPush_NudgeCoalescesAndPushes: Nudge starts a background drain
// that delivers the rows; concurrent nudges coalesce.
func TestLabelPush_NudgeCoalescesAndPushes(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	h.insert(t, "branch_now", "h1", 1000)
	p := newPusher(stub, h.store, true)
	for i := 0; i < 5; i++ {
		p.Nudge()
	}
	deadline := time.After(10 * time.Second)
	for len(stub.mirrorSnapshot()) < 1 {
		select {
		case <-deadline:
			t.Fatal("Nudge never delivered the row")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Wait for the drain goroutine to go idle before the DB closes.
	for p.running.Load() {
		select {
		case <-deadline:
			t.Fatal("drain goroutine never went idle")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// countingSource is a labels.PushSource that only counts calls (race-safe).
type countingSource struct {
	calls atomic.Int32
	mu    sync.Mutex
}

func (c *countingSource) PushKinds(context.Context) ([]string, error) {
	c.calls.Add(1)
	return nil, nil
}
func (c *countingSource) PendingSince(context.Context, string, int64, int) ([]labels.PushRow, error) {
	c.calls.Add(1)
	return nil, nil
}
func (c *countingSource) LoadCursor(context.Context, string, string) (labels.PushCursor, error) {
	c.calls.Add(1)
	return labels.PushCursor{}, nil
}
func (c *countingSource) SaveCursor(context.Context, string, string, labels.PushCursor) error {
	c.calls.Add(1)
	return nil
}
func (c *countingSource) ResetCursors(context.Context, string) error {
	c.calls.Add(1)
	return nil
}
