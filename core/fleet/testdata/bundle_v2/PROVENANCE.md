# testdata/bundle_v2 — provenance

Golden envelopes for signed-bundle-bytes-01SBBYT01 (spec §4 "Tests" a–f,
§6a g–i).

**How they were produced.** `KENAZ_REGEN_BUNDLE_V2_FIXTURES=1 go test
./core/fleet -run TestRegenBundleV2Fixtures`
(`bundle_v2_fixtures_gen_test.go`). Each payload P is `encoding/json` output
of a mirror struct of the bundle a Fleet signs: Bundle's fields in Bundle's
order and, for fixture (b), extra fields this build does not know, as a
newer Fleet that added a section would produce them. Envelopes follow spec
§3: `signature = Ed25519(priv, "kenaz-config-bundle-v2\x00" || P)` (Fleet
ruling 1), `payload_b64` is standard base64 of P, and the envelope `key_id`
is the routing hint that must equal P's `key_id` (ruling 7).

**Fleet commit.** None yet. The Fleet half (§5) had not landed when these
were written (2026-10-15), so they were not recorded from a Fleet
deployment. They encode the agreed rulings 1–7 of 2026-10-15. When kenaz-fleet
ships the envelope, record one from dev, diff it against `a_valid.json`'s
shape, and note the Fleet commit here.

**Key material.** A fixed fixture key: Ed25519 from seed
`SHA-256("kenaz-harness bundle_v2 fixture key (test only)")`, key_id
`4191d9af5539d92b`. It is not pinned in any build and is not a real key.

| File | Case | Expected |
|---|---|---|
| `a_valid.json` | (a) valid envelope, bundle 10 | verifies; decodes; checksum = hex(SHA-256(P)) |
| `b_unknown_fields.json` | (b) P has unknown top-level `future_section` and unknown `model_prefs.future_knob`, bundle 11 | v2 verifies and applies the known fields; reports both names |
| `b_unknown_fields_legacy.json` | (b) the same bundle in the legacy form: P's object plus a v1 signature over SHA-256(P) | **fails** today's re-marshal path with `ErrInvalidSignature` |
| `c_tampered_payload.json` | (c) one byte of P changed (`github` → `gitHub`), signature unchanged | `ErrInvalidSignature` |
| `d_key_id_mismatch.json` | (d) envelope key_id is the signer, P says `ffffffffffffffff` | `ErrBundleKeyIDMismatch` |
| `e_unknown_key.json` | (e) key_id `0000000000000000` | `ErrSigningKeyUnknown` |
| `f_legacy.json` | (f) an older Fleet's answer: legacy bundle, no `format`, bundle 14 | legacy path verifies |
| `g_v1_signature_as_v2.json` | (g) the v1 signature of `f_legacy.json` presented as a v2 envelope | `ErrInvalidSignature` (domain separation) |
| `h_min_client_versions.json` | (h) `min_client_versions` names `model_prefs.allow_user_credentials` at 99.0.0, bundle 15 | known fields applied; "can't enforce" status raised |
| `i_406_bundle_format_unsupported.json` | (i) the 406 body | "update required"; last bundle kept |
