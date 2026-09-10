package fleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// signBundle signs a Bundle the same way the fleet server would:
// JSON-marshal the signing payload → SHA-256 → ed25519.Sign.
func signBundle(t *testing.T, priv ed25519.PrivateKey, b *Bundle) {
	t.Helper()
	payload, err := b.signingPayload()
	if err != nil {
		t.Fatalf("sign: marshal payload: %v", err)
	}
	hash := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, hash[:])
	b.Signature = base64.RawStdEncoding.EncodeToString(sig)
}

func newTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func sampleBundle() *Bundle {
	return &Bundle{
		BundleID: 42,
		IssuedAt: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		MCPAllowlist: []string{"github", "slack"},
		ModelPrefs: &BundleModelPrefs{
			DefaultModel:      "anthropic/claude-opus-4",
			ProviderAllowlist: []string{"anthropic", "openai"},
		},
		CedarDelta: json.RawMessage(`{"rules":[]}`),
	}
}

// TestVerify_SignedRoundTrip verifies that a properly signed bundle passes.
func TestVerify_SignedRoundTrip(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	if err := Verify(b, pub, 0); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// TestVerify_TamperedSignature verifies that altering the signature fails.
func TestVerify_TamperedSignature(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	// Corrupt the last byte of the signature.
	sigBytes, _ := base64.RawStdEncoding.DecodeString(b.Signature)
	sigBytes[len(sigBytes)-1] ^= 0xff
	b.Signature = base64.RawStdEncoding.EncodeToString(sigBytes)

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerify_TamperedPayload verifies that altering a payload field fails.
func TestVerify_TamperedPayload(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	// Mutate a payload field after signing.
	b.MCPAllowlist = append(b.MCPAllowlist, "evil-recipe")

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerify_ReplayRejected verifies the monotonic bundle_id guard.
func TestVerify_ReplayRejected(t *testing.T) {
	pub, priv := newTestKeyPair(t)

	// First bundle: ID=5, last applied = 0 → accepted.
	b1 := sampleBundle()
	b1.BundleID = 5
	signBundle(t, priv, b1)
	if err := Verify(b1, pub, 0); err != nil {
		t.Fatalf("b1 should pass: %v", err)
	}

	// Same bundle replayed against last=5 → rejected.
	if err := Verify(b1, pub, 5); !errors.Is(err, ErrBundleIDNonMonotonic) {
		t.Errorf("replay: expected ErrBundleIDNonMonotonic, got: %v", err)
	}

	// Lower bundle ID replayed → also rejected.
	b2 := sampleBundle()
	b2.BundleID = 3
	signBundle(t, priv, b2)
	if err := Verify(b2, pub, 5); !errors.Is(err, ErrBundleIDNonMonotonic) {
		t.Errorf("lower ID: expected ErrBundleIDNonMonotonic, got: %v", err)
	}

	// Next sequential bundle → accepted.
	b3 := sampleBundle()
	b3.BundleID = 6
	signBundle(t, priv, b3)
	if err := Verify(b3, pub, 5); err != nil {
		t.Errorf("b3 should pass: %v", err)
	}
}

// TestVerify_NilKey verifies that a nil signing key returns ErrSigningKeyNotConfigured.
func TestVerify_NilKey(t *testing.T) {
	_, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	err := Verify(b, nil, 0)
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("expected ErrSigningKeyNotConfigured, got: %v", err)
	}
}

// TestVerify_EmptySignature verifies that an empty signature field fails cleanly.
func TestVerify_EmptySignature(t *testing.T) {
	pub, _ := newTestKeyPair(t)
	b := sampleBundle()
	b.Signature = ""

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestFleetSigningKey_EmptyBytes verifies FleetSigningKey returns nil when
// the ldflags variable is not populated (dev build path).
func TestFleetSigningKey_EmptyBytes(t *testing.T) {
	// The package var is empty by default in tests.
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = ""
	defer func() { fleetSigningPublicKeyBytes = saved }()

	if k := FleetSigningKey(); k != nil {
		t.Errorf("expected nil key when bytes empty, got %v", k)
	}
}

// TestFleetSigningKey_InvalidHex verifies FleetSigningKey returns nil on
// malformed hex input.
func TestFleetSigningKey_InvalidHex(t *testing.T) {
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = "not-valid-hex!!"
	defer func() { fleetSigningPublicKeyBytes = saved }()

	if k := FleetSigningKey(); k != nil {
		t.Errorf("expected nil key on bad hex, got %v", k)
	}
}

// TestFleetSigningKey_ValidHex verifies that a proper 64-char hex string
// decodes to a non-nil ed25519.PublicKey.
func TestFleetSigningKey_ValidHex(t *testing.T) {
	pub, _ := newTestKeyPair(t)
	// Encode pub as hex.
	hexStr := ""
	for _, b := range []byte(pub) {
		hexStr += string([]byte{hexEncNibble(b >> 4), hexEncNibble(b & 0x0f)})
	}
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = hexStr
	defer func() { fleetSigningPublicKeyBytes = saved }()

	got := FleetSigningKey()
	if got == nil {
		t.Fatal("expected non-nil key")
	}
	if !got.Equal(pub) {
		t.Errorf("decoded key != expected public key")
	}
}

func hexEncNibble(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

// ── Accept-set (key rotation) tests ─────────────────────────────────────────

// TestVerifyWithKeySet_EmptySet verifies that an empty accept-set always rejects.
func TestVerifyWithKeySet_EmptySet(t *testing.T) {
	_, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	err := VerifyWithKeySet(b, nil, 0)
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("empty set: expected ErrSigningKeyNotConfigured, got: %v", err)
	}
}

// TestVerifyWithKeySet_ForeignKeyRejected verifies that a bundle signed by
// an unknown key is rejected even when the accept-set is non-empty.
func TestVerifyWithKeySet_ForeignKeyRejected(t *testing.T) {
	pub1, _ := newTestKeyPair(t) // the accepted key
	_, priv2 := newTestKeyPair(t) // the foreign key used to sign

	b := sampleBundle()
	signBundle(t, priv2, b) // signed by foreign key

	err := VerifyWithKeySet(b, []ed25519.PublicKey{pub1}, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("foreign key: expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerifyWithKeySet_RotationOverlap verifies that during a key rotation
// window, a bundle signed by either the old or the new key is accepted.
func TestVerifyWithKeySet_RotationOverlap(t *testing.T) {
	pubOld, privOld := newTestKeyPair(t)
	pubNew, privNew := newTestKeyPair(t)

	// Accept-set contains BOTH keys (rotation overlap window).
	acceptSet := []ed25519.PublicKey{pubOld, pubNew}

	// Bundle signed by old key → accepted.
	bOld := sampleBundle()
	bOld.BundleID = 10
	signBundle(t, privOld, bOld)
	if err := VerifyWithKeySet(bOld, acceptSet, 0); err != nil {
		t.Errorf("old key during overlap: expected nil, got: %v", err)
	}

	// Bundle signed by new key → also accepted.
	bNew := sampleBundle()
	bNew.BundleID = 11
	signBundle(t, privNew, bNew)
	if err := VerifyWithKeySet(bNew, acceptSet, 10); err != nil {
		t.Errorf("new key during overlap: expected nil, got: %v", err)
	}
}

// TestVerifyWithKeySet_AfterRotation verifies that after retiring the old key,
// only the new key is accepted.
func TestVerifyWithKeySet_AfterRotation(t *testing.T) {
	_, privOld := newTestKeyPair(t)
	pubNew, privNew := newTestKeyPair(t)

	// Old key retired: accept-set contains only the new key.
	acceptSet := []ed25519.PublicKey{pubNew}

	// Bundle signed by old key → now rejected.
	bOld := sampleBundle()
	bOld.BundleID = 20
	signBundle(t, privOld, bOld)
	if err := VerifyWithKeySet(bOld, acceptSet, 0); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("old key after retirement: expected ErrInvalidSignature, got: %v", err)
	}

	// Bundle signed by new key → still accepted.
	bNew := sampleBundle()
	bNew.BundleID = 21
	signBundle(t, privNew, bNew)
	if err := VerifyWithKeySet(bNew, acceptSet, 0); err != nil {
		t.Errorf("new key after rotation: expected nil, got: %v", err)
	}
}

// ── ProvisionedMCP / ProviderSetups (fleet-org-config-inheritance-01NORGX01 WP01) ──

// TestBundle_ProvisionedMCPAndProviderSetups_RoundTrip verifies that both
// new sections survive a marshal/unmarshal cycle intact.
func TestBundle_ProvisionedMCPAndProviderSetups_RoundTrip(t *testing.T) {
	b := sampleBundle()
	b.ProvisionedMCP = []ProvisionedMCP{
		{
			RecipeID:    "slack",
			Transport:   "http",
			URL:         "https://mcp.slack.com",
			PrimaryAuth: "oauth",
			OAuth: &ProvisionedMCPOAuth{
				ClientID: "org-public-client-id",
				Scopes:   []string{"channels:read", "chat:write"},
			},
			Config: json.RawMessage(`{"workspace":"acme"}`),
		},
	}
	b.ProviderSetups = []ProviderSetup{
		{
			Provider:   "anthropic",
			AccessMode: "org_shared_key",
			Models:     []string{"claude-opus-4-8", "claude-sonnet-4-6"},
			Default:    true,
		},
	}

	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Bundle
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(b.ProvisionedMCP, got.ProvisionedMCP) {
		t.Errorf("ProvisionedMCP did not round-trip:\n  want %+v\n  got  %+v", b.ProvisionedMCP, got.ProvisionedMCP)
	}
	if !reflect.DeepEqual(b.ProviderSetups, got.ProviderSetups) {
		t.Errorf("ProviderSetups did not round-trip:\n  want %+v\n  got  %+v", b.ProviderSetups, got.ProviderSetups)
	}
}

// TestVerify_TamperedProvisionedMCP verifies that mutating ProvisionedMCP
// after signing invalidates the signature — the concrete proof that this
// section is actually covered by the ed25519 signature and not merely
// carried alongside it. An org-provisioned MCP entry is a command line the
// harness spawns or a URL it connects to; if this test ever passes with
// ErrInvalidSignature NOT returned, the section is silently unsigned and
// attacker-modifiable in transit.
func TestVerify_TamperedProvisionedMCP(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	b.ProvisionedMCP = []ProvisionedMCP{{RecipeID: "slack", PrimaryAuth: "oauth"}}
	signBundle(t, priv, b)

	// Tamper: swap the URL an attacker-controlled bundle-in-transit might
	// redirect a spawned/connected MCP server to.
	b.ProvisionedMCP[0].URL = "https://evil.example.com"

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature after tampering with ProvisionedMCP, got: %v", err)
	}
}

// TestVerify_TamperedProviderSetups verifies that mutating ProviderSetups
// after signing invalidates the signature.
func TestVerify_TamperedProviderSetups(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	b.ProviderSetups = []ProviderSetup{{Provider: "anthropic", AccessMode: "org_shared_key", Default: true}}
	signBundle(t, priv, b)

	// Tamper: flip the provider after signing.
	b.ProviderSetups[0].Provider = "some-other-provider"

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature after tampering with ProviderSetups, got: %v", err)
	}
}

// TestBundle_ProvisionedMCP_EmptyVsAbsentVsNil documents and pins the
// nil/empty/absent semantics WP01 deliberately chose for the two new
// sections. Unlike MCPAllowlist (where nil vs. an explicit empty slice is
// a meaningful "no restriction" vs. "block all" distinction the field is
// deliberately NOT omitempty to preserve), ProvisionedMCP/ProviderSetups
// have no "block" semantic — there is nothing to restrict, only entries to
// add — so both are omitempty and nil/empty/absent all mean exactly one
// thing: "no org-provisioned entries". The corollary (and the thing this
// test actually pins) is Go's well-known asymmetry: an explicit non-nil
// empty slice does NOT round-trip as an empty slice — omitempty drops it
// from the wire, so it comes back nil. That asymmetry is fine here because
// nil and empty are semantically identical for these two fields; it would
// NOT be fine for a field like MCPAllowlist, which is exactly why that
// field stays non-omitempty.
func TestBundle_ProvisionedMCP_EmptyVsAbsentVsNil(t *testing.T) {
	t.Run("nil field is omitted from the wire", func(t *testing.T) {
		b := sampleBundle() // ProvisionedMCP/ProviderSetups left nil
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), `"provisioned_mcp"`) {
			t.Errorf("expected provisioned_mcp key absent for nil slice, got: %s", raw)
		}
		if strings.Contains(string(raw), `"provider_setups"`) {
			t.Errorf("expected provider_setups key absent for nil slice, got: %s", raw)
		}
	})

	t.Run("explicit empty non-nil slice is ALSO omitted (Go omitempty asymmetry)", func(t *testing.T) {
		b := sampleBundle()
		b.ProvisionedMCP = []ProvisionedMCP{} // non-nil, len 0
		b.ProviderSetups = []ProviderSetup{}  // non-nil, len 0
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(raw), `"provisioned_mcp"`) {
			t.Errorf("expected provisioned_mcp key absent for empty-non-nil slice (omitempty), got: %s", raw)
		}
		if strings.Contains(string(raw), `"provider_setups"`) {
			t.Errorf("expected provider_setups key absent for empty-non-nil slice (omitempty), got: %s", raw)
		}

		// The asymmetry: unmarshaling the result does NOT reproduce the
		// original empty-non-nil slice — it comes back nil.
		var got Bundle
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.ProvisionedMCP != nil {
			t.Errorf("expected ProvisionedMCP to come back nil (not empty-non-nil) after round-trip, got: %#v", got.ProvisionedMCP)
		}
		if got.ProviderSetups != nil {
			t.Errorf("expected ProviderSetups to come back nil (not empty-non-nil) after round-trip, got: %#v", got.ProviderSetups)
		}
	})

	t.Run("absent key on the wire unmarshals to nil", func(t *testing.T) {
		var got Bundle
		if err := json.Unmarshal([]byte(`{"bundle_id":1,"issued_at":"2026-05-16T12:00:00Z","mcp_allowlist":[],"signature":""}`), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.ProvisionedMCP != nil {
			t.Errorf("expected nil ProvisionedMCP for absent key, got: %#v", got.ProvisionedMCP)
		}
		if got.ProviderSetups != nil {
			t.Errorf("expected nil ProviderSetups for absent key, got: %#v", got.ProviderSetups)
		}
	})

	t.Run("nil and empty-non-nil signing payloads are identical (both mean no entries)", func(t *testing.T) {
		bNil := sampleBundle()
		bEmpty := sampleBundle()
		bEmpty.ProvisionedMCP = []ProvisionedMCP{}
		bEmpty.ProviderSetups = []ProviderSetup{}

		pNil, err := bNil.signingPayload()
		if err != nil {
			t.Fatalf("signingPayload (nil): %v", err)
		}
		pEmpty, err := bEmpty.signingPayload()
		if err != nil {
			t.Fatalf("signingPayload (empty): %v", err)
		}
		if string(pNil) != string(pEmpty) {
			t.Errorf("expected nil and empty-non-nil ProvisionedMCP/ProviderSetups to produce identical signing payloads (both mean 'no entries'):\n  nil:   %s\n  empty: %s", pNil, pEmpty)
		}
	})
}

// TestProvisionedMCP_NoSecretField and TestProviderSetup_NoSecretField are
// the negative-shape checks backing FR-008 ("no secret ever enters a
// bundle"): reflect over every exported field of the new wire types and
// fail if any field name suggests it could carry credential material. This
// is deliberately conservative (substring match, not an exhaustive
// semantic audit) — it exists to catch an obviously-named regression like
// a future `APIKey`/`BotToken`/`BearerToken` field being added to either
// struct, which per spec §2/§7 must never happen (org-shared provider keys
// ride a dedicated encrypted channel straight into the device credstore,
// never a bundle payload).
func TestProvisionedMCP_NoSecretField(t *testing.T) {
	assertNoSecretLikeFieldNames(t, reflect.TypeOf(ProvisionedMCP{}))
	assertNoSecretLikeFieldNames(t, reflect.TypeOf(ProvisionedMCPOAuth{}))
}

func TestProviderSetup_NoSecretField(t *testing.T) {
	assertNoSecretLikeFieldNames(t, reflect.TypeOf(ProviderSetup{}))
}

func assertNoSecretLikeFieldNames(t *testing.T, typ reflect.Type) {
	t.Helper()
	forbidden := []string{"key", "token", "secret", "password", "bearer", "credential"}
	// ClientID is a deliberate, spec-documented exception: it is a PUBLIC
	// PKCE OAuth client identifier, not a secret (§3.1/§3.3 of the spec).
	allow := map[string]bool{"ClientID": true}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if allow[name] {
			continue
		}
		lower := strings.ToLower(name)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("%s.%s looks secret-shaped (contains %q) — org-shared keys must ride the dedicated encrypted credstore channel, never a bundle payload field (spec §2/§7)", typ, name, bad)
			}
		}
	}
}

// ── The general "keep in sync" proof ────────────────────────────────────────
//
// TestSigningPayload_EveryBundleFieldAffectsPayload is the test that
// proves bundleSigningPayload is actually kept in sync with Bundle, in a
// way that survives future edits: it walks every exported Bundle field by
// reflection (so it automatically picks up any field added after this
// commit, not just the two WP01 adds) and asserts that changing that
// field's value changes signingPayload()'s output. Signature is the one
// deliberate exception (excluded from the signing input by design).
//
// If a future field is added to Bundle and NOT wired into
// bundleSigningPayload / signingPayload(), this test fails on that field's
// subtest — it does not depend on anyone remembering to add a
// field-specific test. If the new field's Go type isn't yet handled by
// mutateFieldForTest below, the test still fails loudly (via t.Fatalf)
// rather than silently skipping — see that function's doc.
func TestSigningPayload_EveryBundleFieldAffectsPayload(t *testing.T) {
	typ := reflect.TypeOf(Bundle{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "Signature" {
			continue // deliberately excluded from the signing input
		}
		t.Run(field.Name, func(t *testing.T) {
			base := sampleBundle()
			mutated := sampleBundle()

			mv := reflect.ValueOf(mutated).Elem().FieldByName(field.Name)
			mutateFieldForTest(t, field.Name, mv)

			basePayload, err := base.signingPayload()
			if err != nil {
				t.Fatalf("signingPayload (base): %v", err)
			}
			mutatedPayload, err := mutated.signingPayload()
			if err != nil {
				t.Fatalf("signingPayload (mutated): %v", err)
			}
			if string(basePayload) == string(mutatedPayload) {
				t.Errorf("mutating Bundle.%s did not change signingPayload() output — the field is carried but NOT covered by the ed25519 signature (bundleSigningPayload/signingPayload() is out of sync with Bundle)", field.Name)
			}
		})
	}
}

// mutateFieldForTest sets an addressable reflect.Value for a Bundle field
// (on a fresh sampleBundle()) to a value that is guaranteed to differ from
// sampleBundle()'s own value for that field, so
// TestSigningPayload_EveryBundleFieldAffectsPayload's before/after
// comparison is meaningful. It intentionally covers only the Go types
// Bundle's fields use today. Adding a Bundle field of a new type without
// extending this switch makes the corresponding subtest above fail with
// this message — that is deliberate: it forces whoever adds the field to
// either extend this switch (keeping the "would fail" property honest) or
// notice the gap. It must never silently no-op.
func mutateFieldForTest(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	switch v.Interface().(type) {
	case int64:
		v.SetInt(v.Int() + 1)
	case string:
		v.SetString(v.String() + "-mutated-for-test")
	case time.Time:
		v.Set(reflect.ValueOf(v.Interface().(time.Time).Add(time.Hour)))
	case []string:
		v.Set(reflect.Append(v, reflect.ValueOf("mutated-for-test")))
	case json.RawMessage:
		v.Set(reflect.ValueOf(json.RawMessage(`{"mutated_for_test":true}`)))
	case []json.RawMessage:
		v.Set(reflect.ValueOf([]json.RawMessage{json.RawMessage(`{"mutated_for_test":true}`)}))
	case *BundleModelPrefs:
		v.Set(reflect.ValueOf(&BundleModelPrefs{DefaultModel: "mutated-for-test"}))
	case []ProvisionedMCP:
		v.Set(reflect.ValueOf([]ProvisionedMCP{{RecipeID: "mutated-for-test"}}))
	case []ProviderSetup:
		v.Set(reflect.ValueOf([]ProviderSetup{{Provider: "mutated-for-test", AccessMode: "byo_key"}}))
	default:
		t.Fatalf("mutateFieldForTest: Bundle field %q has type %s with no mutation rule — extend the switch in mutateFieldForTest (core/fleet/bundle_test.go) instead of letting this subtest vacuously pass", name, v.Type())
	}
}

// TestConfigDistributionEnabled verifies the helper gates on key presence.
func TestConfigDistributionEnabled(t *testing.T) {
	saved := fleetSigningPublicKeyBytes
	defer func() { fleetSigningPublicKeyBytes = saved }()

	fleetSigningPublicKeyBytes = ""
	if ConfigDistributionEnabled() {
		t.Error("expected false when no key bytes set")
	}

	// Set a valid key.
	pub, _ := newTestKeyPair(t)
	hexStr := ""
	for _, b := range []byte(pub) {
		hexStr += string([]byte{hexEncNibble(b >> 4), hexEncNibble(b & 0x0f)})
	}
	fleetSigningPublicKeyBytes = hexStr
	if !ConfigDistributionEnabled() {
		t.Error("expected true when valid key bytes set")
	}
}
