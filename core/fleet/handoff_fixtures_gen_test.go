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
	"encoding/json"
	"os"
	"path/filepath"
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
func regenHandoffSendFixtures(t *testing.T, keys []fleetPublicKeyEntry)    {}
func regenHandoffReceiveFixtures(t *testing.T, keys []fleetPublicKeyEntry) {}
