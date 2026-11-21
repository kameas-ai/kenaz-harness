# testdata/credentials — provenance

Golden vectors for the pinned provider-credential constructions, for Go
(harness and Fleet) and Swift (kenaz-ios, swift-sodium) CI to consume.
Schema `kenaz-credential-vectors/v2`.

**Contract.** kenaz-fleet PR #268, `docs/contract-harness-credentials.md`
§2 (id rules), §5 (device credential key, anonymous WrappedSecret wrap),
§6.1 (seed path, personal ciphertext, `seed_key_check`, `uck_auth`) and
§6.2 (pairing commit/reveal, pairing SAS, authenticated UCK device wrap).
It was read at branch `feat/credentials-contract-crypto`, commit
`376d70ea7fc73a1c38e4c2a5846b06d93e2838ec` (2026-10-15). The v1
constructions were first pinned against the local kitty-spec draft
`kitty-specs/provider-credentials-01NPCRED01/contract-draft.md` §3/§4/§7
at kenaz-fleet commit `8bfe6a31de567b470db0345eff97c2dce684a16a`. Harness
counterpart: `kitty-specs/provider-credentials-client-01PCCLI01/spec.md`
§8, §10.1, §10.3. The harness spec and the PR #268 contract agree on every
byte-level detail.

**Date.** 2026-10-16 (v2). v1: 2026-10-15.

**What changed in v2.**
- **Removed:** the anonymous UCK device wrap
  (`aad = "uck:" user_id ":" uck_version ":" key_id`). The contract
  supersedes it, and it must not be used.
- **Added:** `pairing_identities`, `pairing_commit`, `pairing_sas`,
  `uck_auth_wrap` and `uck_auth`, plus their inputs.
- **Unchanged, byte for byte:** `credential_identity`, `wrapped_secret`,
  `uck_seed_wrap`, `personal_ciphertext` and `seed_key_check`.

**Producer.** `core/fleet/credential_crypto.go`, via `TestCredentialVectors`
in `core/fleet/credential_crypto_test.go`. The test checks the file three
ways on every run: it must equal what the production helpers produce, the
golden hex constants in the test, and an independent reconstruction. The
reconstruction uses manual RFC 5869 HKDF from HMAC, `x/crypto/curve25519`
rather than `crypto/ecdh`, HChaCha20 plus IETF ChaCha20-Poly1305,
`crypto/sha256`, and literal info and AAD strings. Unknown JSON fields fail
the decode.

## Vectors

All byte values are lowercase hex. Text fields such as ids, `info` and `aad`
are given as UTF-8 text, and as `*_hex` where they feed a KDF or an AEAD.
The wire is different: there, every byte string is standard base64 with
padding, and only `seed_key_check` is hex. Versions are base-10 ASCII
integers with no padding. `‖` is byte concatenation.

| Key | Construction |
|---|---|
| `credential_identity` | `clamp(HKDF-SHA256(ikm=seed, salt=nil, info="kenaz-credential-identity-v1:"‖node_id, L=32))` → X25519. `hkdf_output_hex` is the raw HKDF output, `clamped_scalar_hex` is the RFC 7748 clamped private scalar, and `public_key_hex` is the key to register. |
| `wrapped_secret` | §5 anonymous wrap of an org/team secret (UTF-8). `shared = X25519(eph, device_pub)`, then `kek = HKDF-SHA256(shared, nil, "kenaz-credential-v1-wrap", 32)`, then `XChaCha20-Poly1305(kek, nonce24, secret, aad = credential_id ":" version ":" key_id)`. |
| `uck_seed_wrap` | §6.1 path (i): `kek_seed = HKDF-SHA256(seed, nil, "kenaz-user-credentials-kek-v1", 32)`, then `XChaCha20-Poly1305(kek_seed, nonce24, UCK, aad = user_id ":uck:" uck_version)`. |
| `personal_ciphertext` | `XChaCha20-Poly1305(UCK, nonce24, secret_utf8, aad = user_id ":" credential_id ":" version)`. |
| `seed_key_check` | `hex(HKDF-SHA256(seed, nil, "kenaz-seed-key-check-v1", 16))`. |
| `pairing_identities` | `credential_identity` for N (seed `seed_n_hex`, node `node_n`) and W (seed `seed_w_hex`, node `node_w`). |
| `pairing_commit` | `commit = SHA-256("kenaz-pair-commit-v1" ‖ pk_N ‖ nA)`. `nA` is 32 bytes. `sha256_input_hex` is the exact hash input. |
| `pairing_sas` | `b = HKDF-SHA256(ikm = pk_N ‖ pk_W ‖ nA ‖ nB, salt=nil, info = "kenaz-credential-key-sas-v2:" ‖ user_id ‖ ":" ‖ node_N ‖ ":" ‖ node_W, L=8)`. `big_endian_uint64` is `b` read as a big-endian uint64. `sas_digits` is that value mod 10^8, zero-padded to 8 digits; it is displayed as `#### ####`. |
| `uck_auth_wrap` | §6.2 authenticated wrap, from sender W to recipient N. `ikm = X25519(eph, pk_N) ‖ X25519(sk_W, pk_N)`, and `kek = HKDF-SHA256(ikm, nil, info = "kenaz-credential-v1-authwrap" ‖ eph_pub ‖ pk_N ‖ pk_W, 32)`. Then `XChaCha20-Poly1305(kek, nonce24, UCK, aad = "uck:" user_id ":" uck_version ":" key_id_N ":" key_id_W)`. N unwraps with `X25519(sk_N, eph_pub) ‖ X25519(sk_N, pk_W)`, where `pk_W` is the key it paired with. |
| `uck_auth` | `uck_auth = HKDF-SHA256(UCK, nil, "kenaz-uck-auth-v1", 32)` and `uck_auth_hash = SHA-256(uck_auth)`. Both are base64 on the wire. |

`sealed_hex` is ciphertext followed by the 16-byte Poly1305 tag, as
libsodium's `crypto_aead_xchacha20poly1305_ietf_encrypt` writes it.
`ephemeral_scalar_hex` is the unclamped 32-byte input; X25519 clamps it.

**Ids.**
- `user_id`, `credential_id` and every `key_id` are canonical lowercase
  hyphenated UUIDs.
- `node_n` and `node_w` are canonical ULIDs: 26 characters,
  `[0-9A-HJKMNP-TV-Z]`. The v2 production API rejects any other form.
- **Exception:** the v1 `node_id` (`01J9TESTNODE0000000000000A`) is *not* a
  canonical ULID, because it contains `O`. It is kept so the v1 identity and
  `wrapped_secret` goldens stay byte-identical. The identity derivation uses
  the literal node id string and does not validate it.

**Key material.** Every input is a fixed low-entropy pattern:
- seeds: 0x00..0x1f (also W's seed) and 0x40..0x5f (N's seed);
- UCK: 0x33×32;
- ephemerals: 0x11×32 and 0x13×32;
- nonces: 0x22, 0x24, 0x25 and 0x28 ×24;
- nA / nB: 0x26 and 0x27 ×32.

The secrets are `kenaz-test-*` strings. None of these is a real key.

## Regenerating

Regenerate only after a deliberate construction change. That always means
a **new versioned label**; never edit an existing label.

```bash
KENAZ_REGEN_CREDENTIAL_VECTORS=1 go test ./core/fleet -run TestCredentialVectors
```

Then update the `want*` golden constants in `credential_crypto_test.go` and
this file. Send Fleet and iOS the new file.
