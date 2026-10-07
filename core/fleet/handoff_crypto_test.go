package fleet

// handoff_crypto_test.go — device-keys-handoff-01DEVKH01 WP01 (FR-1, AC-8).
//
// TestHandoffV2Vectors pins the PINNED construction (fleet contract
// §10.3) two independent ways:
//
//  1. golden bytes: fixed seed / node id / ephemeral / nonces / content
//     key → exact hex of the handoff public key, wrapped_key and an event
//     ciphertext. Any drift in an info string or an AAD changes the bytes.
//  2. an in-test reconstruction that does NOT call the production helpers:
//     HKDF-SHA256 by hand from HMAC (RFC 5869, salt=nil ⇒ 32 zero bytes)
//     and XChaCha20-Poly1305 by hand from HChaCha20 + IETF
//     ChaCha20-Poly1305 (draft-irtf-cfrg-xchacha §2.3). If the golden hex
//     were regenerated from a wrong implementation, (2) still disagrees.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"
)

func fill(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func seq32() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(i)
	}
	return s
}

// manualHKDF32 is RFC 5869 HKDF-SHA256 with salt=nil, L=32 (one block).
func manualHKDF32(ikm []byte, info string) []byte {
	ext := hmac.New(sha256.New, make([]byte, sha256.Size))
	ext.Write(ikm)
	prk := ext.Sum(nil)
	exp := hmac.New(sha256.New, prk)
	exp.Write([]byte(info))
	exp.Write([]byte{0x01})
	return exp.Sum(nil)
}

// manualXSeal is XChaCha20-Poly1305 built from HChaCha20 + IETF AEAD.
func manualXSeal(t *testing.T, key, nonce24, pt, aad []byte) []byte {
	t.Helper()
	sub, err := chacha20.HChaCha20(key, nonce24[:16])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := chacha20poly1305.New(sub)
	if err != nil {
		t.Fatal(err)
	}
	n12 := append(make([]byte, 4), nonce24[16:]...)
	return aead.Seal(nil, n12, pt, aad)
}

const (
	vecNodeID    = "01J9TESTNODE0000000000000A"
	vecKeyID     = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	vecSessionID = "sess_01J9VECTOR"
	vecSeq       = uint64(7)

	// Golden bytes (AC-8). Generated from this construction and
	// cross-checked by the manual reconstruction in the same test.
	wantHandoffPubHex = "d7e2ae10aed065c2f17b4da6163751e004bb4882a43a878eb997c992e2573d48"
	wantWrappedHex    = "8b138215fcdf1c85fa531eb77353af58d0e9a2122476130d9f92489bda6b5b592a4427e8001318c90b8e8c034d95d2f0"
	wantEventCTHex    = "638406f60ad31ba1e01f277b32ede0d24d67901a0cf0242e6bc4b23dc6257c21bcd427b0f64770befeaa68b1a7f78c41a4760cc1d86c062a7360e32e06079161e6b0a1459f76ac"
)

func TestHandoffV2Vectors(t *testing.T) {
	seed := seq32()

	// ── identity: HKDF(seed, "kenaz-handoff-identity-v2:"+node_id) ──
	priv, err := DeriveHandoffPrivKey(seed, vecNodeID)
	if err != nil {
		t.Fatal(err)
	}
	manualScalar := manualHKDF32(seed, "kenaz-handoff-identity-v2:"+vecNodeID)
	manualPriv, err := ecdh.X25519().NewPrivateKey(manualScalar)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.PublicKey().Bytes()
	if !bytes.Equal(pub, manualPriv.PublicKey().Bytes()) {
		t.Fatal("identity derivation disagrees with manual HKDF(seed, kenaz-handoff-identity-v2:<node_id>)")
	}
	if got := hex.EncodeToString(pub); got != wantHandoffPubHex {
		t.Errorf("handoff pub = %s, want %s", got, wantHandoffPubHex)
	}

	// ── wrap: kek = HKDF(X25519(eph, pub), "kenaz-handoff-v2-wrap"); Seal(kek, n, ck, aad=key_id) ──
	eph, err := ecdh.X25519().NewPrivateKey(fill(0x11, 32))
	if err != nil {
		t.Fatal(err)
	}
	wrapNonce := fill(0x22, 24)
	contentKey := fill(0x33, 32)
	wrapped, err := wrapContentKeyWith(eph, pub, vecKeyID, contentKey, wrapNonce)
	if err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != 48 || len(wrapNonce) != 24 {
		t.Fatalf("interop sizes: wrapped=%d (want 48) wrap_nonce=%d (want 24)", len(wrapped), len(wrapNonce))
	}
	shared, err := eph.ECDH(priv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	manualWrap := manualXSeal(t, manualHKDF32(shared, "kenaz-handoff-v2-wrap"), wrapNonce, contentKey, []byte(vecKeyID))
	if !bytes.Equal(wrapped, manualWrap) {
		t.Fatal("wrap disagrees with the manual pinned construction")
	}
	if got := hex.EncodeToString(wrapped); got != wantWrappedHex {
		t.Errorf("wrapped_key = %s, want %s", got, wantWrappedHex)
	}
	// Receive side: our private key unwraps it.
	ck, err := unwrapContentKey(priv, eph.PublicKey().Bytes(), wrapped, wrapNonce, vecKeyID)
	if err != nil || !bytes.Equal(ck, contentKey) {
		t.Fatalf("unwrap: %v", err)
	}

	// ── event: Seal(ck, nonce, pt, aad=session_id+":"+decimal(seq)) ──
	evNonce := fill(0x44, 24)
	pt := []byte(`{"v":1,"role":"user","content":"hello","event_count":1}`)
	ct, err := sealXAAD(contentKey, evNonce, pt, handoffEventAAD(vecSessionID, vecSeq))
	if err != nil {
		t.Fatal(err)
	}
	if string(handoffEventAAD(vecSessionID, vecSeq)) != "sess_01J9VECTOR:7" {
		t.Fatalf("event AAD = %q", handoffEventAAD(vecSessionID, vecSeq))
	}
	if !bytes.Equal(ct, manualXSeal(t, contentKey, evNonce, pt, []byte("sess_01J9VECTOR:7"))) {
		t.Fatal("event seal disagrees with the manual pinned construction")
	}
	if got := hex.EncodeToString(ct); got != wantEventCTHex {
		t.Errorf("encrypted_payload = %s, want %s", got, wantEventCTHex)
	}
	got, err := openHandoffEvent(contentKey, vecSessionID, vecSeq, ct, evNonce)
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("open event: %v", err)
	}
}

// AC-3 tamper: a wrap only opens for the key it was made for (AAD=key_id
// and ECDH), and an event only opens at its own (session, seq).
func TestHandoffV2_Tamper(t *testing.T) {
	a, _ := DeriveHandoffPrivKey(seq32(), "node-a")
	b, _ := DeriveHandoffPrivKey(seq32(), "node-b")
	ck := fill(0x55, 32)
	wa, err := wrapContentKey(rand.Reader, a.PublicKey().Bytes(), "11111111-1111-4111-8111-111111111111", ck)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := wrapContentKey(rand.Reader, b.PublicKey().Bytes(), "22222222-2222-4222-8222-222222222222", ck)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(wa.EphemeralPublicKey, wb.EphemeralPublicKey) {
		t.Fatal("each recipient key must get a FRESH ephemeral")
	}
	// Each device opens its own.
	if _, err := unwrapContentKey(a, wa.EphemeralPublicKey, wa.WrappedKey, wa.WrapNonce, wa.KeyID); err != nil {
		t.Fatalf("a own wrap: %v", err)
	}
	if _, err := unwrapContentKey(b, wb.EphemeralPublicKey, wb.WrappedKey, wb.WrapNonce, wb.KeyID); err != nil {
		t.Fatalf("b own wrap: %v", err)
	}
	// Swapped wraps fail.
	if _, err := unwrapContentKey(a, wb.EphemeralPublicKey, wb.WrappedKey, wb.WrapNonce, wb.KeyID); !errors.Is(err, ErrHandoffUnwrapFailed) {
		t.Fatalf("a opening b's wrap: %v", err)
	}
	// Right key, wrong key_id (AAD) fails.
	if _, err := unwrapContentKey(a, wa.EphemeralPublicKey, wa.WrappedKey, wa.WrapNonce, wb.KeyID); !errors.Is(err, ErrHandoffUnwrapFailed) {
		t.Fatalf("a's wrap under b's key_id: %v", err)
	}
	ct, nonce, err := sealHandoffEvent(ck, "sess-1", 3, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openHandoffEvent(ck, "sess-1", 4, ct, nonce); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("replay under another seq: %v", err)
	}
	if _, err := openHandoffEvent(ck, "sess-2", 3, ct, nonce); !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("transplant to another session: %v", err)
	}
}

// FR-1 / spec §4 consequences (a)-(c).
func TestHandoffIdentity_DeviceSalted(t *testing.T) {
	seed := seq32()
	a1, _ := DeriveHandoffPrivKey(seed, "node-a")
	a2, _ := DeriveHandoffPrivKey(seed, "node-a")
	b, _ := DeriveHandoffPrivKey(seed, "node-b")
	if !bytes.Equal(a1.PublicKey().Bytes(), a2.PublicKey().Bytes()) {
		t.Fatal("same seed + node id must re-derive the same key (re-enroll is a no-op)")
	}
	if bytes.Equal(a1.PublicKey().Bytes(), b.PublicKey().Bytes()) {
		t.Fatal("same seed on another node id must derive a DIFFERENT key (recovery import ≠ same key)")
	}
	if _, err := DeriveHandoffPrivKey(seed, " "); !errors.Is(err, ErrHandoffIdentityUnavailable) {
		t.Fatalf("empty node id: %v", err)
	}
	if _, err := LoadOwnHandoffPrivKey(""); !errors.Is(err, ErrHandoffIdentityUnavailable) {
		t.Fatalf("no data dir must refuse rather than derive from a transient node id: %v", err)
	}
}

// LoadOwnHandoffPrivKey reads the REAL node_id.txt: stable across loads,
// and a regenerated node id (node_removed) yields a fresh key.
func TestLoadOwnHandoffPrivKey_FollowsNodeIDFile(t *testing.T) {
	if err := StoreContextSeed(seq32()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	k1, err := LoadOwnHandoffPrivKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOwnHandoffPrivKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1.PublicKey().Bytes(), k2.PublicKey().Bytes()) {
		t.Fatal("key must be stable while node_id.txt is")
	}
	if err := ClearNodeID(dir); err != nil {
		t.Fatal(err)
	}
	k3, err := LoadOwnHandoffPrivKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(k1.PublicKey().Bytes(), k3.PublicKey().Bytes()) {
		t.Fatal("a fresh node id must derive a fresh key")
	}
}

func TestKeyFingerprint_MatchesSigningFormat(t *testing.T) {
	s, err := NewDeviceSigner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := KeyFingerprint(s.PublicKey()); got != s.PubkeyFingerprint() {
		t.Fatalf("KeyFingerprint = %s, signer = %s", got, s.PubkeyFingerprint())
	}
	if len(s.PublicKey()) != 32 {
		t.Fatalf("signing public key = %d bytes", len(s.PublicKey()))
	}
}
