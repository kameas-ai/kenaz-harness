package fleet

// handoff_crypto.go — the PINNED team-handoff construction
// (device-keys-handoff-01DEVKH01; kenaz-fleet docs/contract-harness-sync.md
// §10.3 "PINNED v2 wrap construction", fleet-confirmed 2026-10-06).
// Implemented VERBATIM — fleet stores these bytes opaquely, every client
// MUST produce exactly this, and it is NOT NaCl box:
//
//	key wrap, per recipient device key:
//	  shared      = X25519(ephemeral_priv, recipient_handoff_pub)
//	  kek         = HKDF-SHA256(ikm=shared, salt=nil, info="kenaz-handoff-v2-wrap", L=32)
//	  wrapped_key = XChaCha20-Poly1305.Seal(kek, wrap_nonce[24], content_key[32], aad=key_id)  → 48 B
//	events:
//	  encrypted_payload = XChaCha20-Poly1305.Seal(content_key, nonce[24], plaintext,
//	                                              aad = session_id + ":" + decimal(seq))
//	  content_key = 32 random bytes, no KDF over it.
//	v1 (legacy direct mode, info "handoff-v1"): NOT implemented — the v1
//	  send never produced a real item; send and accept arms are both deleted.
//
// TestHandoffV2Vectors pins every info string and AAD against golden bytes
// AND an independent in-test reconstruction (manual HKDF + HChaCha20), so a
// drift in either fails loudly instead of producing items nobody can open.
//
// Privacy invariant: no key material or plaintext is ever logged here.

import (
	"crypto/ecdh"
	"errors"
	"fmt"
	"io"
	"strconv"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// handoffContentKeyLen is the random content key length (no KDF).
	handoffContentKeyLen = 32
	// handoffWrappedKeyLen is fleet's exact wrapped_key length: the 32 B
	// content key plus the 16 B Poly1305 tag.
	handoffWrappedKeyLen = handoffContentKeyLen + chacha20poly1305.Overhead
	// handoffNonceLen is fleet's exact wrap_nonce / event nonce length.
	handoffNonceLen = chacha20poly1305.NonceSizeX
)

// ErrHandoffUnwrapFailed means the wrapped content key did not open under
// our key — wrong device key, tampered wrap, or a key_id/AAD mismatch.
var ErrHandoffUnwrapFailed = errors.New("fleet: handoff key unwrap failed")

// handoffEventAAD is the pinned per-event AAD: session_id + ":" + decimal(seq).
func handoffEventAAD(sessionID string, seq uint64) []byte {
	return []byte(sessionID + ":" + strconv.FormatUint(seq, 10))
}

// handoffWrapKEK derives the v2 key-encryption key from an X25519 shared
// secret.
func handoffWrapKEK(shared []byte) ([]byte, error) {
	return hkdf32(shared, string(LabelHandoffWrapV2))
}

// wrapContentKeyWith wraps contentKey for one recipient device key with an
// explicit ephemeral private key and wrap nonce (deterministic core — the
// golden vectors drive it). Returns the 48-byte wrapped key.
func wrapContentKeyWith(eph *ecdh.PrivateKey, recipientPub []byte, keyID string, contentKey, wrapNonce []byte) ([]byte, error) {
	if len(contentKey) != handoffContentKeyLen {
		return nil, fmt.Errorf("fleet: handoff wrap: content key must be %d bytes", handoffContentKeyLen)
	}
	if len(wrapNonce) != handoffNonceLen {
		return nil, fmt.Errorf("fleet: handoff wrap: wrap nonce must be %d bytes", handoffNonceLen)
	}
	pub, err := ecdh.X25519().NewPublicKey(recipientPub)
	if err != nil {
		return nil, fmt.Errorf("fleet: handoff wrap: recipient key: %w", err)
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		// crypto/ecdh refuses an all-zero shared secret (low-order point).
		return nil, fmt.Errorf("fleet: handoff wrap: ECDH: %w", err)
	}
	kek, err := handoffWrapKEK(shared)
	if err != nil {
		return nil, err
	}
	wrapped, err := sealXAAD(kek, wrapNonce, contentKey, []byte(keyID))
	if err != nil {
		return nil, err
	}
	if len(wrapped) != handoffWrappedKeyLen {
		return nil, fmt.Errorf("fleet: handoff wrap: got %d bytes, want %d", len(wrapped), handoffWrappedKeyLen)
	}
	return wrapped, nil
}

// handoffWrap is one recipients[] entry.
type handoffWrap struct {
	KeyID              string `json:"key_id"`
	EphemeralPublicKey []byte `json:"ephemeral_public_key"`
	WrappedKey         []byte `json:"wrapped_key"`
	WrapNonce          []byte `json:"wrap_nonce"`
}

// wrapContentKey wraps contentKey to one recipient key with a FRESH
// ephemeral key pair and wrap nonce drawn from rnd.
func wrapContentKey(rnd io.Reader, recipientPub []byte, keyID string, contentKey []byte) (handoffWrap, error) {
	eph, err := ecdh.X25519().GenerateKey(rnd)
	if err != nil {
		return handoffWrap{}, fmt.Errorf("fleet: handoff wrap: ephemeral key: %w", err)
	}
	nonce := make([]byte, handoffNonceLen)
	if _, err := io.ReadFull(rnd, nonce); err != nil {
		return handoffWrap{}, fmt.Errorf("fleet: handoff wrap: nonce: %w", err)
	}
	wrapped, err := wrapContentKeyWith(eph, recipientPub, keyID, contentKey, nonce)
	if err != nil {
		return handoffWrap{}, err
	}
	return handoffWrap{
		KeyID:              keyID,
		EphemeralPublicKey: eph.PublicKey().Bytes(),
		WrappedKey:         wrapped,
		WrapNonce:          nonce,
	}, nil
}

// unwrapContentKey opens a wrapped content key with our device private key.
func unwrapContentKey(priv *ecdh.PrivateKey, ephPub, wrapped, wrapNonce []byte, keyID string) ([]byte, error) {
	if len(wrapped) != handoffWrappedKeyLen || len(wrapNonce) != handoffNonceLen {
		return nil, ErrHandoffUnwrapFailed
	}
	pub, err := ecdh.X25519().NewPublicKey(ephPub)
	if err != nil {
		return nil, ErrHandoffUnwrapFailed
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, ErrHandoffUnwrapFailed
	}
	kek, err := handoffWrapKEK(shared)
	if err != nil {
		return nil, err
	}
	ck, err := DecryptAAD(kek, wrapped, wrapNonce, []byte(keyID))
	if err != nil || len(ck) != handoffContentKeyLen {
		return nil, ErrHandoffUnwrapFailed
	}
	return ck, nil
}

// sealHandoffEvent encrypts one event under the content key, bound to
// (sessionID, seq) by the pinned AAD.
func sealHandoffEvent(contentKey []byte, sessionID string, seq uint64, plaintext []byte) (ciphertext, nonce []byte, err error) {
	return EncryptAAD(contentKey, plaintext, handoffEventAAD(sessionID, seq))
}

// openHandoffEvent decrypts one v2 event; a different session_id or seq
// than it was sealed under fails authentication (no reorder/transplant).
func openHandoffEvent(contentKey []byte, sessionID string, seq uint64, ciphertext, nonce []byte) ([]byte, error) {
	return DecryptAAD(contentKey, ciphertext, nonce, handoffEventAAD(sessionID, seq))
}
