package fleet

// handoff_roundtrip_test.go — device-keys-handoff-01DEVKH01 WP05 (FR-5,
// AC-3, AC-6): share → each recipient device accepts independently;
// legacy v1 "direct" items; not-for-this-device; 429 backoff; 404; delete;
// inbox undecryptable.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// recipientDevice is a HandoffHandler for one of the recipient's devices:
// its own data dir with a REAL node_id.txt, and the shared fixture seed.
func recipientDevice(t *testing.T, f *fakeV2Fleet, node string) *HandoffHandler {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "fleet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fleet", "node_id.txt"), []byte(node+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StoreContextSeed(fxSeed()); err != nil {
		t.Fatal(err)
	}
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = dir
	return NewHandoffHandler(c, &fakeEmitter{}, nil)
}

// OQ-1 (owner-ruled, review fix #3): ONE accept per user. Every device of
// the recipient CAN decrypt (the key is wrapped to each — pinned by
// TestShareSession_V2_WrapsToEveryDevice), but the first device to open
// the item deletes it through the real DELETE path, so the user's other
// devices then get handoff_not_found with honest copy.
func TestHandoffRoundTrip_OneAcceptPerUser(t *testing.T) {
	f, sender, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	evs := plainEvents(4)
	res, err := sender.ShareSession(context.Background(), "sess_rt", fxRecipientUser, evs)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	devA := recipientDevice(t, f, fxNodeA)
	if _, err := devA.Inbox(context.Background()); err != nil {
		t.Fatalf("inbox: %v", err)
	}
	got, err := devA.AcceptShare(context.Background(), res.InboxItemID)
	if err != nil {
		t.Fatalf("device A accept: %v", err)
	}
	if got.Mode != "wrapped" || got.SessionID != "sess_rt" || got.SenderEmail != "alice@example.com" || len(got.Events) != 4 {
		t.Fatalf("device A: %+v", got)
	}
	for i := range evs {
		if got.Events[i].Seq != evs[i].Seq || !bytes.Equal(got.Events[i].Bytes, evs[i].Bytes) {
			t.Fatalf("event %d mismatch", i)
		}
	}
	// What the RPC layer does after persisting: the real DELETE.
	if err := devA.DeleteShare(context.Background(), res.InboxItemID); err != nil {
		t.Fatalf("delete after accept: %v", err)
	}
	if d := f.deleted(); len(d) != 1 || d[0] != res.InboxItemID {
		t.Fatalf("fleet deletes = %v", d)
	}
	// The user's other device: gone, with copy that says why.
	_, err = recipientDevice(t, f, fxNodeB).AcceptShare(context.Background(), res.InboxItemID)
	if !errors.Is(err, ErrHandoffItemGone) || !strings.Contains(err.Error(), "already opened on another of your devices") {
		t.Fatalf("device B after A accepted: %v", err)
	}
	// A device whose key the item was never wrapped to is refused too.
	f2, sender2, _ := sendRig(t)
	f2.setKeys(fxRecipientUser, fxKeySet(t))
	res2, _ := sender2.ShareSession(context.Background(), "sess_rt2", fxRecipientUser, evs)
	if _, err := recipientDevice(t, f2, "01J9SOMEOTHERNODE00000000").AcceptShare(context.Background(), res2.InboxItemID); !errors.Is(err, ErrHandoffNotForThisDevice) {
		t.Fatalf("foreign device: %v", err)
	}
}

// Review fix #9: the v1 "direct" accept arm is gone — a direct-mode item
// is refused with update-the-app copy, never decrypted.
func TestHandoffAccept_DirectModeRefused(t *testing.T) {
	f := newFakeV2Fleet(t)
	withExternalToken(t, "tok-direct")
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys[:1])
	ev, _ := json.Marshal([]fleetHandoffEventWire{{Seq: 1, EncryptedPayload: bytes.Repeat([]byte{1}, 32), Nonce: make([]byte, 24)}})
	it := &fakeStoredItem{ID: uuid.NewString(), SessionID: "s", Sender: fakeSender, Recipient: fxRecipientUser,
		Mode: "direct", EphTop: keys[0].PublicKey, Events: ev, ReceivedAt: time.Now().UTC(),
		Recipients: []fleetHandoffRecipientOut{{KeyID: keys[0].KeyID, Fingerprint: keys[0].Fingerprint, EphemeralPublicKey: keys[0].PublicKey}}}
	f.addDirectItem(it)
	_, err := recipientDevice(t, f, fxNodeA).AcceptShare(context.Background(), it.ID)
	var he *HandoffError
	if !errors.As(err, &he) || he.Code != "unknown_mode" {
		t.Fatalf("err = %v", err)
	}
}

// Review fix #2(a): events must be exactly seq 1..N — reorder / gap /
// duplicate is refused before decryption or persistence.
func TestHandoffAccept_NonContiguousSeqsRefused(t *testing.T) {
	for name, seqs := range map[string][]uint64{
		"reordered": {2, 1, 3},
		"gap":       {1, 3},
		"duplicate": {1, 1, 2},
		"from zero": {0, 1},
	} {
		f, sender, _ := sendRig(t)
		f.setKeys(fxRecipientUser, fxKeySet(t))
		res, err := sender.ShareSession(context.Background(), "sess_seq", fxRecipientUser, plainEvents(len(seqs)))
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		for _, it := range f.items {
			if it.ID != res.InboxItemID {
				continue
			}
			var evs []fleetHandoffEventWire
			_ = json.Unmarshal(it.Events, &evs)
			for i := range evs {
				evs[i].Seq = seqs[i]
			}
			it.Events, _ = json.Marshal(evs)
		}
		f.mu.Unlock()
		if _, err := recipientDevice(t, f, fxNodeA).AcceptShare(context.Background(), res.InboxItemID); !errors.Is(err, ErrHandoffEventsOutOfOrder) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Fetch is rate limited fleet-side: honour Retry-After, bounded, no hot loop.
func TestHandoffAccept_429_WaitsRetryAfter(t *testing.T) {
	f, sender, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	res, err := sender.ShareSession(context.Background(), "sess_429", fxRecipientUser, plainEvents(1))
	if err != nil {
		t.Fatal(err)
	}
	dev := recipientDevice(t, f, fxNodeA)
	var mu sync.Mutex
	var waits []time.Duration
	dev.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return nil
	}
	f.mu.Lock()
	f.getStatus = http.StatusTooManyRequests
	f.mu.Unlock()
	if _, err := dev.AcceptShare(context.Background(), res.InboxItemID); err != nil {
		t.Fatalf("accept after one 429: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(waits) != 1 || waits[0] != time.Second || f.getCount() != 2 {
		t.Fatalf("waits = %v gets = %d, want one 1s wait then success", waits, f.getCount())
	}
}

func TestHandoffAccept_NotFound_AndDelete(t *testing.T) {
	f := newFakeV2Fleet(t)
	withExternalToken(t, "tok-404")
	dev := recipientDevice(t, f, fxNodeA)
	_, err := dev.AcceptShare(context.Background(), uuid.NewString())
	if !errors.Is(err, ErrHandoffItemGone) {
		t.Fatalf("err = %v", err)
	}
	if err := dev.DeleteShare(context.Background(), "a1b2c3d4-0000-4000-8000-000000000000"); err != nil {
		t.Fatalf("delete (idempotent): %v", err)
	}
	if got := f.deleted(); len(got) != 1 {
		t.Fatalf("deletes = %v", got)
	}
}

// AC-9 shape: after the recipient's keys rotate (e.g. recovery import on
// the device it was sent to), fleet flags the item undecryptable.
func TestHandoffInbox_UndecryptableFlag(t *testing.T) {
	f, sender, _ := sendRig(t)
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys[:1])
	if _, err := sender.ShareSession(context.Background(), "sess_u", fxRecipientUser, plainEvents(1)); err != nil {
		t.Fatal(err)
	}
	f.setKeys(fxRecipientUser, keys[1:]) // the addressed key was revoked
	items, err := recipientDevice(t, f, fxNodeB).Inbox(context.Background())
	if err != nil || len(items) != 1 || !items[0].Undecryptable {
		t.Fatalf("inbox = %+v, %v", items, err)
	}
}

// Review fix #10: decrypting emits NO inbound audit; RecordAccepted (called
// after the persist) emits it with the sender in SenderUserID and this
// user in RecipientUserID.
func TestHandoffInboundAudit_AfterPersist_CorrectFields(t *testing.T) {
	f, sender, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	res, err := sender.ShareSession(context.Background(), "sess_aud", fxRecipientUser, plainEvents(1))
	if err != nil {
		t.Fatal(err)
	}
	dev := recipientDevice(t, f, fxNodeA)
	if err := SaveIdentity(dev.client.dataDir, Identity{UserID: fxRecipientUser}); err != nil {
		t.Fatal(err)
	}
	em := dev.emitter.(*fakeEmitter)
	got, err := dev.AcceptShare(context.Background(), res.InboxItemID)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(em.snapshot()); n != 0 {
		t.Fatalf("AcceptShare emitted %d audit events; inbound audit belongs after persist", n)
	}
	dev.RecordAccepted(context.Background(), got.InboxItemID, got.SessionID, got.SenderUserID, "local-1")
	evs := em.snapshot()
	if len(evs) != 1 || evs[0].Kind != contextaudit.KindFleetSessionSharedInbound {
		t.Fatalf("audit = %+v", evs)
	}
	var p contextaudit.FleetSessionHandoffPayload
	_ = json.Unmarshal(evs[0].Payload, &p)
	if p.SenderUserID != fakeSender || p.RecipientUserID != fxRecipientUser || p.InboxItemID != res.InboxItemID || p.LocalSessionID != "local-1" {
		t.Fatalf("payload = %+v", p)
	}
}
