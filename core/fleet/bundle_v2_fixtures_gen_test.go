package fleet

// bundle_v2_fixtures_gen_test.go — regenerates core/fleet/testdata/bundle_v2/*
// (signed-bundle-bytes-01SBBYT01 WP01, spec §4 "Tests" and §6a). Run with
//
//	KENAZ_REGEN_BUNDLE_V2_FIXTURES=1 go test ./core/fleet -run TestRegenBundleV2Fixtures
//
// The payloads P are encoded with encoding/json from mirror structs of the
// bundle a NEWER Fleet signs — Bundle's fields in Bundle's order, plus
// fields this build does not know (fixture b) — exactly as a Fleet that
// added a section would produce them. The signing key is a fixed fixture
// key (bundleV2FixtureSeed), never a real one. See testdata/bundle_v2/PROVENANCE.md.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const bundleV2FixtureDir = "testdata/bundle_v2"

// bundleV2FixtureKey derives the fixture signing key from a fixed seed.
func bundleV2FixtureKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("kenaz-harness bundle_v2 fixture key (test only)"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	return priv.Public().(ed25519.PublicKey), priv
}

var bundleV2FixtureIssuedAt = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// fixtureModelPrefsNewer is model_prefs as a newer Fleet signs it: the known
// fields plus one this build has never heard of.
type fixtureModelPrefsNewer struct {
	DefaultModel      string   `json:"default_model,omitempty"`
	ProviderAllowlist []string `json:"provider_allowlist,omitempty"`
	FutureKnob        bool     `json:"future_knob"`
}

// fixtureBundleNewer is the top-level bundle a newer Fleet signs (no
// "signature" field): a known prefix in Bundle order, then a new section.
type fixtureBundleNewer struct {
	BundleID      int64                   `json:"bundle_id"`
	KeyID         string                  `json:"key_id,omitempty"`
	IssuedAt      time.Time               `json:"issued_at"`
	MCPAllowlist  []string                `json:"mcp_allowlist"`
	ModelPrefs    *fixtureModelPrefsNewer `json:"model_prefs,omitempty"`
	FutureSection map[string]any          `json:"future_section,omitempty"`
}

// fixtureBundleMinVersions carries min_client_versions (fixture h).
type fixtureBundleMinVersions struct {
	BundleID          int64             `json:"bundle_id"`
	KeyID             string            `json:"key_id,omitempty"`
	IssuedAt          time.Time         `json:"issued_at"`
	MCPAllowlist      []string          `json:"mcp_allowlist"`
	ModelPrefs        *BundleModelPrefs `json:"model_prefs,omitempty"`
	MinClientVersions map[string]string `json:"min_client_versions,omitempty"`
}

func v2JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func writeV2Fixture(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bundleV2FixtureDir, name), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestRegenBundleV2Fixtures(t *testing.T) {
	if os.Getenv("KENAZ_REGEN_BUNDLE_V2_FIXTURES") != "1" {
		t.Skip("set KENAZ_REGEN_BUNDLE_V2_FIXTURES=1 to regenerate testdata/bundle_v2")
	}
	pub, priv := bundleV2FixtureKey()
	kid := SigningKeyID(pub)
	if err := os.MkdirAll(bundleV2FixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envelope := func(p []byte, envKeyID string) []byte {
		raw, err := SignEnvelopeForTesting(p, priv, envKeyID)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// (a) valid envelope.
	pa := v2JSON(t, Bundle{
		BundleID: 10, KeyID: kid, IssuedAt: bundleV2FixtureIssuedAt,
		MCPAllowlist: []string{"github"},
		ModelPrefs:   &BundleModelPrefs{DefaultModel: "claude-sonnet", ProviderAllowlist: []string{"anthropic"}},
	})
	// Bundle carries "signature":"" — a real P has no signature key. Strip
	// it by re-marshalling through the signing payload shape.
	var a Bundle
	if err := json.Unmarshal(pa, &a); err != nil {
		t.Fatal(err)
	}
	pa, err := a.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	writeV2Fixture(t, "a_valid.json", envelope(pa, ""))

	// (b) unknown top-level field AND unknown nested model_prefs field.
	newer := fixtureBundleNewer{
		BundleID: 11, KeyID: kid, IssuedAt: bundleV2FixtureIssuedAt,
		MCPAllowlist:  []string{"github", "slack"},
		ModelPrefs:    &fixtureModelPrefsNewer{DefaultModel: "claude-sonnet", ProviderAllowlist: []string{"anthropic"}, FutureKnob: true},
		FutureSection: map[string]any{"enabled": true},
	}
	pb := v2JSON(t, newer)
	writeV2Fixture(t, "b_unknown_fields.json", envelope(pb, ""))
	// The SAME bundle in the legacy form a newer Fleet serves to old builds:
	// P's object plus a v1 signature (Ed25519 over SHA-256 of Fleet's own
	// struct marshal, i.e. pb).
	h := sha256.Sum256(pb)
	var legacyB map[string]any
	if err := json.Unmarshal(pb, &legacyB); err != nil {
		t.Fatal(err)
	}
	legacyB["signature"] = base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, h[:]))
	writeV2Fixture(t, "b_unknown_fields_legacy.json", v2JSON(t, legacyB))

	// (c) one tampered payload byte: "github" → "gitHub" inside P, the
	// signature left as it was over the original P.
	var envC BundleEnvelope
	if err := json.Unmarshal(envelope(pa, ""), &envC); err != nil {
		t.Fatal(err)
	}
	pc := append([]byte(nil), pa...)
	for i := 0; i+6 <= len(pc); i++ {
		if string(pc[i:i+6]) == "github" {
			pc[i+3] = 'H'
			break
		}
	}
	envC.PayloadB64 = base64.StdEncoding.EncodeToString(pc)
	writeV2Fixture(t, "c_tampered_payload.json", v2JSON(t, envC))

	// (d) envelope key_id (the real signer) != the key_id signed inside P.
	pd := v2JSON(t, struct {
		BundleID     int64     `json:"bundle_id"`
		KeyID        string    `json:"key_id"`
		IssuedAt     time.Time `json:"issued_at"`
		MCPAllowlist []string  `json:"mcp_allowlist"`
	}{12, "ffffffffffffffff", bundleV2FixtureIssuedAt, []string{"github"}})
	writeV2Fixture(t, "d_key_id_mismatch.json", envelope(pd, kid))

	// (e) a key_id no pinned key has.
	pe := v2JSON(t, struct {
		BundleID     int64     `json:"bundle_id"`
		KeyID        string    `json:"key_id"`
		IssuedAt     time.Time `json:"issued_at"`
		MCPAllowlist []string  `json:"mcp_allowlist"`
	}{13, "0000000000000000", bundleV2FixtureIssuedAt, []string{"github"}})
	writeV2Fixture(t, "e_unknown_key.json", envelope(pe, "0000000000000000"))

	// (f) an older Fleet: the legacy bundle, no "format".
	lf := &Bundle{BundleID: 14, KeyID: kid, IssuedAt: bundleV2FixtureIssuedAt, MCPAllowlist: []string{"github"}}
	if err := SignBundleForTesting(lf, priv); err != nil {
		t.Fatal(err)
	}
	writeV2Fixture(t, "f_legacy.json", v2JSON(t, lf))

	// (g) a v1 signature by the same key presented as v2: P is the v1
	// signing payload, the signature is Ed25519(SHA-256(P)) — no prefix.
	pg, err := lf.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	writeV2Fixture(t, "g_v1_signature_as_v2.json", v2JSON(t, BundleEnvelope{
		Format: BundleEnvelopeFormatV2, KeyID: kid,
		PayloadB64: base64.StdEncoding.EncodeToString(pg),
		Signature:  lf.Signature,
	}))

	// (h) min_client_versions naming a field above any shipped version.
	no := false
	ph := v2JSON(t, fixtureBundleMinVersions{
		BundleID: 15, KeyID: kid, IssuedAt: bundleV2FixtureIssuedAt,
		MCPAllowlist:      []string{"github"},
		ModelPrefs:        &BundleModelPrefs{ProviderAllowlist: []string{"anthropic"}, AllowUserCredentials: &no},
		MinClientVersions: map[string]string{"model_prefs.allow_user_credentials": "99.0.0", "mcp_allowlist": "0.1.0"},
	})
	writeV2Fixture(t, "h_min_client_versions.json", envelope(ph, ""))

	// (i) the 406 body Fleet sends a build that asked for a retired form.
	writeV2Fixture(t, "i_406_bundle_format_unsupported.json", v2JSON(t, map[string]string{
		"code":    "bundle_format_unsupported",
		"message": "this client must update to receive config bundles",
	}))
}
