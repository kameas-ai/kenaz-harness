# testdata/handoff — provenance

Fixtures for device-keys-handoff-01DEVKH01 AC-7 (decode real fleet shapes).

**How they were produced.** `KENAZ_REGEN_HANDOFF_FIXTURES=1 go test
./core/fleet -run TestRegenHandoffFixtures` (handoff_fixtures_gen_test.go).
The JSON envelopes are encoded with `encoding/json` from VERBATIM mirrors of
kenaz-fleet's own response structs at fleet `main` 97a1c12
(`service/device_keys.go` PublicKeyResponse/PublicKeyEntry,
`service/handlers_handoff.go` HandoffSendResponse/HandoffInboxItem/
HandoffGetResponse/HandoffRecipientOut/HandoffEventWire,
`service/httpcore` ErrorResponse) — same field names, json tags and
omitempty, so `[]byte` fields are base64 std and `time.Time` is RFC3339Nano,
exactly as fleet's encoder writes them. Error envelopes copy fleet's codes,
messages and `details` keys from the handlers (`writeKeyRegErr`,
`staleKeys`, `rejectWith`).

**Deviation from spec AC-7.** The spec asks for fixtures *recorded from the
dev fleet*. They were not: recording requires a live Team-tier account's
bearer token, which the implementing agent must not handle or print. The
mirrors were taken from the deployed fleet source instead; re-record from dev
(and diff against these) when an operator can run the harness signed in.

**Key material.** Recipient keys derive from a fixed fixture seed
(`fxSeed`, 0xA0..0xBF) and node ids `01J9FIXTURENODE{A,B}…`; the handoff
ciphertexts are produced with the pinned construction so the fixture tests
also decrypt them. Nothing here is a real user's key.
