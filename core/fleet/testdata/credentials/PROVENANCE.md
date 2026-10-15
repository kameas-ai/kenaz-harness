# testdata/credentials — provenance

Golden vectors for the pinned provider-credential constructions, for Go
(harness and Fleet) and Swift (kenaz-ios, swift-sodium) CI to consume.

**Contract.** kenaz-fleet
`kitty-specs/provider-credentials-01NPCRED01/contract-draft.md` §3
(device credential key, WrappedSecret wrap), §4 (UCK seed path, UCK device
path, personal ciphertext, `seed_key_check`) and §7.1–§7.2 (pinned labels,
XChaCha20-Poly1305). It was read at kenaz-fleet commit
`8bfe6a31de567b470db0345eff97c2dce684a16a`. The file is a **local
kitty-spec draft**: `kitty-specs/` is git-ignored in kenaz-fleet, so the
commit identifies the checkout, not a committed copy of the draft. Harness
counterpart: `kitty-specs/provider-credentials-client-01PCCLI01/spec.md`
§8. The two agree on every byte-level detail.

**Date.** 2026-10-15.

**Producer.** `core/fleet/credential_crypto.go`, via `TestCredentialV1Vectors`
in `core/fleet/credential_crypto_test.go`. The test checks the file three
ways on every run: it must equal what the production helpers produce, the
golden hex constants in the test, and an independent reconstruction. The
reconstruction uses manual RFC 5869 HKDF from HMAC, `x/crypto/curve25519`
rather than `crypto/ecdh`, HChaCha20 plus IETF ChaCha20-Poly1305, and
literal info and AAD strings. Unknown JSON fields fail the decode.

## Vectors

All byte values are lowercase hex. Text fields such as ids, `info` and `aad`
are given as UTF-8 text, and as `*_hex` where they feed a KDF or an AEAD.
The wire is different: there, every byte string is standard base64 with
padding, and only `seed_key_check` is hex. Versions are base-10 ASCII
integers with no padding.

| Key | Construction |
|---|---|
| `credential_identity` | `clamp(HKDF-SHA256(ikm=seed, salt=nil, info="kenaz-credential-identity-v1:"‖node_id, L=32))` → X25519. `hkdf_output_hex` is the raw HKDF output, `clamped_scalar_hex` is the RFC 7748 clamped private scalar, and `public_key_hex` is the key to register. |
| `wrapped_secret` | §3 Wrap of an org/team secret (UTF-8). `shared = X25519(eph, device_pub)`, then `kek = HKDF-SHA256(shared, nil, "kenaz-credential-v1-wrap", 32)`, then `XChaCha20-Poly1305(kek, nonce24, secret, aad = credential_id ":" version ":" key_id)`. |
| `uck_device_wrap` | §4 path (ii): the same Wrap with payload = the 32-byte UCK and `aad = "uck:" user_id ":" uck_version ":" key_id`. |
| `uck_seed_wrap` | §4 path (i): `kek_seed = HKDF-SHA256(seed, nil, "kenaz-user-credentials-kek-v1", 32)`, then `XChaCha20-Poly1305(kek_seed, nonce24, UCK, aad = user_id ":uck:" uck_version)`. |
| `personal_ciphertext` | §4: `XChaCha20-Poly1305(UCK, nonce24, secret_utf8, aad = user_id ":" credential_id ":" version)`. |
| `seed_key_check` | `hex(HKDF-SHA256(seed, nil, "kenaz-seed-key-check-v1", 16))`. |

`sealed_hex` is ciphertext followed by the 16-byte Poly1305 tag, as
libsodium's `crypto_aead_xchacha20poly1305_ietf_encrypt` writes it.
`ephemeral_scalar_hex` is the unclamped 32-byte input; X25519 clamps it.

**The UCK device-wrap AAD is pinned here.** Contract §4 path (ii) asked for
it to be confirmed against the harness golden vector. The value is
`"uck:" user_id` as credential_id and `uck_version` as version, so the AAD
is `uck:<user_id>:<uck_version>:<key_id>`.

**Key material.** Every input is a fixed low-entropy pattern: seed
0x00..0x1f, UCK 0x33×32, ephemerals 0x11×32 and 0x12×32, nonces
0x22/0x23/0x24/0x25×24. The secrets are `kenaz-test-*` strings. None of them
is a real key.

## Regenerating

Regenerate only after a deliberate construction change. That always means
a **new versioned label**; never edit a v1 label.

```bash
KENAZ_REGEN_CREDENTIAL_VECTORS=1 go test ./core/fleet -run TestCredentialV1Vectors
```

Then update the `want*` golden constants in `credential_crypto_test.go` and
this file. Send Fleet and iOS the new file.
