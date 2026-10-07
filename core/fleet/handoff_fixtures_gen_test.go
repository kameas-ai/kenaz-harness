package fleet

// handoff_fixtures_gen_test.go — regenerates core/fleet/testdata/handoff/*
// (device-keys-handoff-01DEVKH01 AC-7). Run with
//
//	KENAZ_REGEN_HANDOFF_FIXTURES=1 go test ./core/fleet -run TestRegenHandoffFixtures
//
// The ENVELOPES are encoded from verbatim mirrors of kenaz-fleet's own
// response structs (service/device_keys.go PublicKeyResponse /
// PublicKeyEntry; service/handlers_handoff.go HandoffSendResponse,
// HandoffInboxItem, HandoffGetResponse, HandoffRecipientOut,
// HandoffEventWire; httpcore.ErrorResponse) at fleet main 97a1c12 — same
// field names, same json tags, same omitempty, same []byte→base64 std and
// time.Time→RFC3339Nano encodings encoding/json gives fleet. They were
// NOT recorded from the dev fleet: recording needs a live Team-tier
// account's bearer token, which this mission must not handle. See
// testdata/handoff/PROVENANCE.md.

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// ── verbatim mirrors of kenaz-fleet response types (97a1c12) ──

type fleetPublicKeyEntry struct {
	KeyID       string    `json:"key_id"`
	NodeID      string    `json:"node_id"`
	PublicKey   []byte    `json:"public_key" swaggertype:"string" format:"byte"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
}

type fleetPublicKeyResponse struct {
	UserID      string                `json:"user_id"`
	PublicKey   []byte                `json:"public_key" swaggertype:"string" format:"byte"`
	Fingerprint string                `json:"fingerprint"`
	PublicKeys  []fleetPublicKeyEntry `json:"public_keys"`
}

type fleetErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type fleetHandoffSendResponse struct {
	InboxItemID string    `json:"inbox_item_id"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type fleetHandoffInboxItem struct {
	InboxItemID   string    `json:"inbox_item_id"`
	SessionID     string    `json:"session_id"`
	SenderUserID  string    `json:"sender_user_id"`
	SenderEmail   string    `json:"sender_email"`
	ReceivedAt    time.Time `json:"received_at"`
	Undecryptable bool      `json:"undecryptable,omitempty"`
}

type fleetHandoffRecipientOut struct {
	KeyID              string `json:"key_id,omitempty"`
	Fingerprint        string `json:"fingerprint"`
	EphemeralPublicKey []byte `json:"ephemeral_public_key" swaggertype:"string" format:"byte"`
	WrappedKey         []byte `json:"wrapped_key,omitempty" swaggertype:"string" format:"byte"`
	WrapNonce          []byte `json:"wrap_nonce,omitempty" swaggertype:"string" format:"byte"`
}

type fleetHandoffGetResponse struct {
	InboxItemID        string                     `json:"inbox_item_id"`
	SessionID          string                     `json:"session_id"`
	SenderUserID       string                     `json:"sender_user_id"`
	Mode               string                     `json:"mode" enums:"direct,wrapped"`
	EphemeralPublicKey []byte                     `json:"ephemeral_public_key,omitempty" swaggertype:"string" format:"byte"`
	Recipients         []fleetHandoffRecipientOut `json:"recipients"`
	Events             json.RawMessage            `json:"events" swaggertype:"array,object"`
}

type fleetHandoffEventWire struct {
	Seq              uint64 `json:"seq"`
	EncryptedPayload []byte `json:"encrypted_payload" swaggertype:"string" format:"byte"`
	Nonce            []byte `json:"nonce" swaggertype:"string" format:"byte"`
	PrevHash         string `json:"prev_hash,omitempty"`
}

// Fixture identities: two devices of recipient "bob" derived from a fixed
// seed, so fixture tests can also DECRYPT the handoff fixtures.
const (
	fxRecipientUser = "0b7a2f5e-6f4c-4f7e-9a51-6c1d2b3e4f50"
	fxSenderUser    = "6d1e9c4a-2b3f-4a5e-8c7d-9e0f1a2b3c4d"
	fxKeyA          = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	fxKeyB          = "9b2c8a71-0d4e-4b6f-8a3c-2e1f0d9c8b7a"
	fxNodeA         = "01J9FIXTURENODEA0000000000"
	fxNodeB         = "01J9FIXTURENODEB0000000000"
)

func fxSeed() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(0xA0 + i)
	}
	return s
}

func fxKeySet(t *testing.T) []fleetPublicKeyEntry {
	t.Helper()
	a, err := DeriveHandoffPrivKey(fxSeed(), fxNodeA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveHandoffPrivKey(fxSeed(), fxNodeB)
	if err != nil {
		t.Fatal(err)
	}
	ta := time.Date(2026, 10, 7, 9, 0, 0, 123456000, time.UTC)
	tb := time.Date(2026, 10, 6, 17, 30, 0, 0, time.UTC)
	// Newest first, as activeHandoffKeys orders them.
	return []fleetPublicKeyEntry{
		{KeyID: fxKeyA, NodeID: fxNodeA, PublicKey: a.PublicKey().Bytes(), Fingerprint: KeyFingerprint(a.PublicKey().Bytes()), CreatedAt: ta},
		{KeyID: fxKeyB, NodeID: fxNodeB, PublicKey: b.PublicKey().Bytes(), Fingerprint: KeyFingerprint(b.PublicKey().Bytes()), CreatedAt: tb},
	}
}

func writeFixture(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegenHandoffFixtures(t *testing.T) {
	if os.Getenv("KENAZ_REGEN_HANDOFF_FIXTURES") != "1" {
		t.Skip("set KENAZ_REGEN_HANDOFF_FIXTURES=1 to regenerate testdata/handoff")
	}
	keys := fxKeySet(t)
	writeFixture(t, "public_key_set.json", fleetPublicKeyResponse{
		UserID: fxRecipientUser, PublicKey: keys[0].PublicKey, Fingerprint: keys[0].Fingerprint, PublicKeys: keys,
	})
	writeFixture(t, "public_key_not_found.json", fleetErrorResponse{
		Code: "public_key_not_found", Message: "no public key for that user",
	})
	regenHandoffSendFixtures(t, keys)
	regenHandoffReceiveFixtures(t, keys)
}

// regenHandoffSendFixtures / regenHandoffReceiveFixtures: send-side and
// receive-side fixtures (filled in by WP04 / WP05).
func regenHandoffSendFixtures(t *testing.T, keys []fleetPublicKeyEntry) {
	writeFixture(t, "send_success.json", fleetHandoffSendResponse{
		InboxItemID: "c8a7d6e5-f4b3-4a21-9c0d-1e2f3a4b5c6d",
		ExpiresAt:   time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC),
	})
	writeFixture(t, "send_recipient_keys_stale.json", fleetErrorResponse{
		Code: "recipient_keys_stale", Message: "recipient key set changed; re-wrap to the current keys",
		Details: map[string]any{"public_keys": keys},
	})
	writeFixture(t, "send_handoff_empty.json", fleetErrorResponse{
		Code: "handoff_empty", Message: "a handoff must contain at least one event",
	})
	writeFixture(t, "send_rate_limited.json", fleetErrorResponse{
		Code: "rate_limited", Message: "too many handoffs; try again later",
		Details: map[string]any{"retry_after_seconds": 1800},
	})
}

// fxPlainEvents are the fixture handoff's plaintext events (the
// core/session "kenaz.handoff.event" v1 shape).
var fxPlainEvents = []string{
	`{"v":1,"role":"user","content":"Can you check the deploy?","created_at":"2026-10-07T08:00:00Z","title":"Deploy check"}`,
	`{"v":1,"role":"assistant","content":"The deploy is green.","move":{"kind":"final","index":0,"turn_seq":1},"created_at":"2026-10-07T08:00:05Z"}`,
}

const (
	fxItemWrapped = "e1f2a3b4-c5d6-4e7f-8a9b-0c1d2e3f4a5b"
	fxItemDirect  = "f0e1d2c3-b4a5-4968-8776-5a4b3c2d1e0f"
	fxSessionID   = "4be0643f1d98573b97cdca98a65347dd"
)

func regenHandoffReceiveFixtures(t *testing.T, keys []fleetPublicKeyEntry) {
	received := time.Date(2026, 10, 7, 8, 1, 0, 0, time.UTC)
	writeFixture(t, "inbox.json", []fleetHandoffInboxItem{
		{InboxItemID: fxItemWrapped, SessionID: fxSessionID, SenderUserID: fxSenderUser, SenderEmail: "alice@example.com", ReceivedAt: received},
		{InboxItemID: fxItemDirect, SessionID: fxSessionID, SenderUserID: fxSenderUser, SenderEmail: "alice@example.com",
			ReceivedAt: received.Add(-time.Hour), Undecryptable: true},
	})
	// wrapped (v2): one content key, a wrap per device key.
	ck := make([]byte, 32)
	_, _ = rand.Read(ck)
	var evs []fleetHandoffEventWire
	for i, pt := range fxPlainEvents {
		ct, nonce, err := sealHandoffEvent(ck, fxSessionID, uint64(i+1), []byte(pt))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, fleetHandoffEventWire{Seq: uint64(i + 1), EncryptedPayload: ct, Nonce: nonce})
	}
	rawEvs, _ := json.Marshal(evs)
	var recips []fleetHandoffRecipientOut
	for _, k := range keys {
		w, err := wrapContentKey(rand.Reader, k.PublicKey, k.KeyID, ck)
		if err != nil {
			t.Fatal(err)
		}
		recips = append(recips, fleetHandoffRecipientOut{KeyID: k.KeyID, Fingerprint: k.Fingerprint,
			EphemeralPublicKey: w.EphemeralPublicKey, WrappedKey: w.WrappedKey, WrapNonce: w.WrapNonce})
	}
	sort.Slice(recips, func(i, j int) bool { return recips[i].Fingerprint < recips[j].Fingerprint })
	writeFixture(t, "handoff_wrapped.json", fleetHandoffGetResponse{InboxItemID: fxItemWrapped, SessionID: fxSessionID,
		SenderUserID: fxSenderUser, Mode: "wrapped", Recipients: recips, Events: rawEvs})
	// direct (legacy v1) to key A only.
	eph, _ := ecdh.X25519().GenerateKey(rand.Reader)
	pub, _ := ecdh.X25519().NewPublicKey(keys[0].PublicKey)
	shared, _ := eph.ECDH(pub)
	v1key, _ := hkdf32(shared, string(LabelHandoffKey))
	var devs []fleetHandoffEventWire
	for i, pt := range fxPlainEvents {
		ct, nonce, err := Encrypt(v1key, []byte(pt))
		if err != nil {
			t.Fatal(err)
		}
		devs = append(devs, fleetHandoffEventWire{Seq: uint64(i + 1), EncryptedPayload: ct, Nonce: nonce})
	}
	rawDev, _ := json.Marshal(devs)
	writeFixture(t, "handoff_direct.json", fleetHandoffGetResponse{InboxItemID: fxItemDirect, SessionID: fxSessionID,
		SenderUserID: fxSenderUser, Mode: "direct", EphemeralPublicKey: eph.PublicKey().Bytes(),
		Recipients: []fleetHandoffRecipientOut{{KeyID: keys[0].KeyID, Fingerprint: keys[0].Fingerprint, EphemeralPublicKey: eph.PublicKey().Bytes()}},
		Events:     rawDev})
	writeFixture(t, "handoff_not_found.json", fleetErrorResponse{Code: "handoff_not_found", Message: "handoff not found"})
}
