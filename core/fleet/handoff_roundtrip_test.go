package fleet

// handoff_roundtrip_test.go — device-keys-handoff-01DEVKH01 WP05 (FR-5,
// AC-3, AC-6): share → each recipient device accepts independently;
// legacy v1 "direct" items; not-for-this-device; 429 backoff; 404; delete;
// inbox undecryptable.

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestHandoffRoundTrip_EachDeviceAccepts(t *testing.T) {
	f, sender, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	evs := plainEvents(4)
	res, err := sender.ShareSession(context.Background(), "sess_rt", fxRecipientUser, evs)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	for _, node := range []string{fxNodeA, fxNodeB} {
		dev := recipientDevice(t, f, node)
		if _, err := dev.Inbox(context.Background()); err != nil {
			t.Fatalf("inbox: %v", err)
		}
		got, err := dev.AcceptShare(context.Background(), res.InboxItemID)
		if err != nil {
			t.Fatalf("device %s accept: %v", node, err)
		}
		if got.Mode != "wrapped" || got.SessionID != "sess_rt" || got.SenderEmail != "alice@example.com" || len(got.Events) != 4 {
			t.Fatalf("device %s: %+v", node, got)
		}
		for i := range evs {
			if got.Events[i].Seq != evs[i].Seq || !bytes.Equal(got.Events[i].Bytes, evs[i].Bytes) {
				t.Fatalf("device %s event %d mismatch", node, i)
			}
		}
	}
	// A device whose key the item was not wrapped to cannot open it.
	other := recipientDevice(t, f, "01J9SOMEOTHERNODE00000000")
	if _, err := other.AcceptShare(context.Background(), res.InboxItemID); !errors.Is(err, ErrHandoffNotForThisDevice) {
		t.Fatalf("foreign device: %v", err)
	}
}

// buildDirectItem constructs what a v0.91 (v1) sender produced for a
// single-key recipient: HKDF(X25519(eph, pub), "handoff-v1"), events sealed
// directly, no AAD.
func buildDirectItem(t *testing.T, recipientPub []byte, keyID string, evs []SessionEventRecord) *fakeStoredItem {
	t.Helper()
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := ecdh.X25519().NewPublicKey(recipientPub)
	shared, _ := eph.ECDH(pub)
	key, err := DeriveKey(shared, LabelHandoffKey)
	if err != nil {
		t.Fatal(err)
	}
	var wire []fleetHandoffEventWire
	for _, e := range evs {
		ct, nonce, err := Encrypt(key, e.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		wire = append(wire, fleetHandoffEventWire{Seq: e.Seq, EncryptedPayload: ct, Nonce: nonce})
	}
	raw, _ := json.Marshal(wire)
	return &fakeStoredItem{
		ID: uuid.NewString(), SessionID: "sess_v1", Sender: fakeSender, Recipient: fxRecipientUser, Mode: "direct",
		EphTop: eph.PublicKey().Bytes(), Events: raw, ReceivedAt: time.Now().UTC(),
		Recipients: []fleetHandoffRecipientOut{{KeyID: keyID, Fingerprint: KeyFingerprint(recipientPub), EphemeralPublicKey: eph.PublicKey().Bytes()}},
	}
}

func TestHandoffAccept_LegacyDirectItem(t *testing.T) {
	f := newFakeV2Fleet(t)
	withExternalToken(t, "tok-direct")
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys[:1])
	evs := plainEvents(2)
	it := buildDirectItem(t, keys[0].PublicKey, keys[0].KeyID, evs)
	f.addDirectItem(it)

	got, err := recipientDevice(t, f, fxNodeA).AcceptShare(context.Background(), it.ID)
	if err != nil {
		t.Fatalf("v1 direct accept: %v", err)
	}
	if got.Mode != "direct" || len(got.Events) != 2 || !bytes.Equal(got.Events[1].Bytes, evs[1].Bytes) {
		t.Fatalf("direct = %+v", got)
	}
	if _, err := recipientDevice(t, f, fxNodeB).AcceptShare(context.Background(), it.ID); !errors.Is(err, ErrHandoffNotForThisDevice) {
		t.Fatalf("direct item on another device: %v", err)
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
