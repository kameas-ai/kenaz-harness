package fleet

// Multi-key pinning + key_id routing (bundle-key-rotation WP01).
//
// Wire contract (cross-repo with kenaz-fleet): a bundle carries a signed
// top-level "key_id" = lowercase hex of the first 8 bytes of SHA-256 over the
// RAW 32-byte ed25519 public key. The harness pins a SET of keys
// (fleetSigningPublicKeys, comma-separated 64-hex entries) and routes
// verification by key_id.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// keyIDVectorPub is the fixed key_id test vector: the 32 raw bytes
// 0x00, 0x01, …, 0x1f interpreted as an ed25519 public key. key_id derivation
// is a pure hash of the raw bytes, so the bytes need not be a valid curve
// point for the vector to pin the formula.
func keyIDVectorPub() ed25519.PublicKey {
	b := make([]byte, ed25519.PublicKeySize)
	for i := range b {
		b[i] = byte(i)
	}
	return ed25519.PublicKey(b)
}

// TestSigningKeyID_ContractVector pins the key_id formula against a fixed
// vector. The expected value is computed here, independently, straight from
// the contract text (hex of SHA-256(raw pubkey)[:8]) rather than hardcoded,
// so the test states the formula instead of a magic string.
func TestSigningKeyID_ContractVector(t *testing.T) {
	pub := keyIDVectorPub()
	sum := sha256.Sum256([]byte(pub))
	want := fmt.Sprintf("%x", sum[:8])

	got := SigningKeyID(pub)
	if got != want {
		t.Fatalf("SigningKeyID(0x00..0x1f) = %q, want %q", got, want)
	}
	if len(got) != 16 {
		t.Errorf("key_id length = %d, want 16", len(got))
	}
	if got != strings.ToLower(got) {
		t.Errorf("key_id %q is not lowercase", got)
	}
	t.Logf("key_id test vector: pubkey=%s key_id=%s", hex.EncodeToString(pub), got)
}

func hexOf(pub ed25519.PublicKey) string { return hex.EncodeToString(pub) }

// TestParseSigningKeySet covers the pin-list grammar: comma-separated,
// whitespace-tolerant, empty entries skipped, duplicates collapsed, and ANY
// malformed entry rejects the whole set (never a silent skip).
func TestParseSigningKeySet(t *testing.T) {
	a, _ := newTestKeyPair(t)
	b, _ := newTestKeyPair(t)
	tests := []struct {
		name    string
		in      string
		want    []ed25519.PublicKey
		wantErr bool
	}{
		{name: "empty", in: "", want: nil},
		{name: "only whitespace and commas", in: " , ,\t", want: nil},
		{name: "one key", in: hexOf(a), want: []ed25519.PublicKey{a}},
		{name: "two keys with whitespace", in: "  " + hexOf(a) + " ,\n " + hexOf(b) + "  ", want: []ed25519.PublicKey{a, b}},
		{name: "trailing comma", in: hexOf(a) + ",", want: []ed25519.PublicKey{a}},
		{name: "uppercase hex accepted", in: strings.ToUpper(hexOf(a)), want: []ed25519.PublicKey{a}},
		{name: "duplicate collapses", in: hexOf(a) + "," + hexOf(a), want: []ed25519.PublicKey{a}},
		{name: "short entry rejects set", in: hexOf(a) + "," + hexOf(b)[:62], wantErr: true},
		{name: "long entry rejects set", in: hexOf(a) + "00", wantErr: true},
		{name: "non-hex entry rejects set", in: hexOf(a) + "," + strings.Repeat("zz", 32), wantErr: true},
		{name: "bad entry first rejects set", in: "nope," + hexOf(a), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSigningKeySet(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected the whole set to be rejected, got %d key(s)", len(got))
				}
				if !errors.Is(err, ErrSigningKeyNotConfigured) {
					t.Errorf("error should wrap ErrSigningKeyNotConfigured, got: %v", err)
				}
				if got != nil {
					t.Errorf("rejected set must return nil keys, got %d", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d keys, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if !got[i].Equal(tc.want[i]) {
					t.Errorf("key[%d] mismatch", i)
				}
			}
		})
	}
}

// setPinVars sets both ldflag pin vars for one test and restores them.
func setPinVars(t *testing.T, set, legacy string) {
	t.Helper()
	savedSet, savedLegacy := fleetSigningPublicKeys, fleetSigningPublicKeyBytes
	fleetSigningPublicKeys, fleetSigningPublicKeyBytes = set, legacy
	t.Cleanup(func() {
		fleetSigningPublicKeys, fleetSigningPublicKeyBytes = savedSet, savedLegacy
	})
}

// TestFleetSigningKeys_LegacyVarMerged: the one-release compatibility path —
// the old single-key ldflag is merged into (and deduplicated against) the set.
func TestFleetSigningKeys_LegacyVarMerged(t *testing.T) {
	a, _ := newTestKeyPair(t)
	b, _ := newTestKeyPair(t)

	setPinVars(t, hexOf(a), hexOf(b))
	if got := FleetSigningKeys(); len(got) != 2 || !got[0].Equal(a) || !got[1].Equal(b) {
		t.Fatalf("set+legacy: got %d keys, want [a b]", len(got))
	}

	setPinVars(t, hexOf(a), hexOf(a))
	if got := FleetSigningKeys(); len(got) != 1 {
		t.Fatalf("legacy duplicate of a set entry: got %d keys, want 1", len(got))
	}

	setPinVars(t, "", hexOf(b))
	if got := FleetSigningKeys(); len(got) != 1 || !got[0].Equal(b) {
		t.Fatalf("legacy only: want [b]")
	}
}

// TestFleetSigningKeys_MalformedRejectsWholeSet: one bad entry disables
// config distribution entirely and FleetSigningKeySetError says why.
func TestFleetSigningKeys_MalformedRejectsWholeSet(t *testing.T) {
	a, _ := newTestKeyPair(t)
	setPinVars(t, hexOf(a)+",deadbeef", "")

	if got := FleetSigningKeys(); got != nil {
		t.Fatalf("malformed set must yield nil keys, got %d", len(got))
	}
	if ConfigDistributionEnabled() {
		t.Error("ConfigDistributionEnabled must be false for a rejected set")
	}
	err := FleetSigningKeySetError()
	if err == nil || !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Fatalf("FleetSigningKeySetError = %v, want an ErrSigningKeyNotConfigured-wrapping error", err)
	}
	if !strings.Contains(err.Error(), "entry #2") {
		t.Errorf("error should name the bad entry, got: %v", err)
	}

	// A bad LEGACY value rejects the set too.
	setPinVars(t, hexOf(a), "not-hex")
	if FleetSigningKeys() != nil {
		t.Error("malformed legacy pin must reject the whole set")
	}

	// Empty is not an error — it is the deliberate unkeyed build.
	setPinVars(t, "", "")
	if err := FleetSigningKeySetError(); err != nil {
		t.Errorf("empty pin set is not malformed, got: %v", err)
	}
}

// TestVerifyWithKeySet_KeyIDMatrix is the {no keys, one key, two keys} ×
// {key_id matching, key_id unknown, key_id absent, bad signature, replayed
// bundle_id} table, plus the routing-specific adversarial rows.
func TestVerifyWithKeySet_KeyIDMatrix(t *testing.T) {
	pubA, privA := newTestKeyPair(t)
	pubB, privB := newTestKeyPair(t)
	_, privC := newTestKeyPair(t) // never pinned

	// signed builds a bundle with the given key_id (explicit, may be "")
	// signed by priv.
	signed := func(keyID string, priv ed25519.PrivateKey, mutate func(*Bundle)) *Bundle {
		b := sampleBundle() // BundleID 42
		b.KeyID = keyID
		signBundle(t, priv, b)
		if mutate != nil {
			mutate(b)
		}
		return b
	}
	idA := SigningKeyID(pubA)
	idB := SigningKeyID(pubB)
	idC := SigningKeyID(privC.Public().(ed25519.PublicKey))

	keySets := []struct {
		name string
		keys []ed25519.PublicKey
	}{
		{"no keys", nil},
		{"one key", []ed25519.PublicKey{pubA}},
		{"two keys", []ed25519.PublicKey{pubA, pubB}},
	}

	type row struct {
		name   string
		bundle func() *Bundle
		lastID int64
		// want per key set, indexed like keySets; nil = accept.
		want [3]error
	}
	rows := []row{
		{
			name:   "key_id matching",
			bundle: func() *Bundle { return signed(idA, privA, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, nil, nil},
		},
		{
			name:   "key_id matching second pinned key",
			bundle: func() *Bundle { return signed(idB, privB, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrSigningKeyUnknown, nil},
		},
		{
			name:   "key_id unknown",
			bundle: func() *Bundle { return signed(idC, privC, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrSigningKeyUnknown, ErrSigningKeyUnknown},
		},
		{
			name:   "key_id absent, signed by first key",
			bundle: func() *Bundle { return signed("", privA, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, nil, nil},
		},
		{
			name:   "key_id absent, signed by second key",
			bundle: func() *Bundle { return signed("", privB, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrInvalidSignature, nil},
		},
		{
			name:   "key_id absent, signed by unpinned key",
			bundle: func() *Bundle { return signed("", privC, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrInvalidSignature, ErrInvalidSignature},
		},
		{
			name: "bad signature (payload tampered after signing)",
			bundle: func() *Bundle {
				return signed(idA, privA, func(b *Bundle) { b.MCPAllowlist = append(b.MCPAllowlist, "evil") })
			},
			want: [3]error{ErrSigningKeyNotConfigured, ErrInvalidSignature, ErrInvalidSignature},
		},
		{
			// Routing must NOT fall back to other pinned keys: a bundle that
			// claims key A but is signed by (also-pinned) key B is invalid.
			name:   "key_id names A but signed by pinned B",
			bundle: func() *Bundle { return signed(idA, privB, nil) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrInvalidSignature, ErrInvalidSignature},
		},
		{
			// key_id is inside the signed payload: rewriting it in transit
			// either selects no key or breaks the signature.
			name:   "key_id rewritten after signing",
			bundle: func() *Bundle { return signed(idA, privA, func(b *Bundle) { b.KeyID = idB }) },
			want:   [3]error{ErrSigningKeyNotConfigured, ErrSigningKeyUnknown, ErrInvalidSignature},
		},
		{
			name:   "replayed bundle_id (equal) with valid key_id signature",
			bundle: func() *Bundle { return signed(idA, privA, nil) },
			lastID: 42,
			want:   [3]error{ErrSigningKeyNotConfigured, ErrBundleIDNonMonotonic, ErrBundleIDNonMonotonic},
		},
		{
			name:   "replayed lower bundle_id with valid key_id signature",
			bundle: func() *Bundle { return signed(idB, privB, nil) },
			lastID: 100,
			want:   [3]error{ErrSigningKeyNotConfigured, ErrSigningKeyUnknown, ErrBundleIDNonMonotonic},
		},
		{
			name:   "replayed lower bundle_id, key_id absent",
			bundle: func() *Bundle { return signed("", privA, nil) },
			lastID: 100,
			want:   [3]error{ErrSigningKeyNotConfigured, ErrBundleIDNonMonotonic, ErrBundleIDNonMonotonic},
		},
	}

	for _, r := range rows {
		for i, ks := range keySets {
			t.Run(r.name+"/"+ks.name, func(t *testing.T) {
				err := VerifyWithKeySet(r.bundle(), ks.keys, r.lastID)
				want := r.want[i]
				if want == nil {
					if err != nil {
						t.Fatalf("expected accept, got: %v", err)
					}
					return
				}
				if !errors.Is(err, want) {
					t.Fatalf("expected %v, got: %v", want, err)
				}
				// The distinct sentinels must not alias each other.
				for _, other := range []error{ErrSigningKeyNotConfigured, ErrSigningKeyUnknown, ErrInvalidSignature, ErrBundleIDNonMonotonic} {
					if other != want && errors.Is(err, other) {
						t.Errorf("error %v also matches %v", err, other)
					}
				}
			})
		}
	}
}

// TestSigningPayload_KeyIDFieldOrder pins key_id's position in the signed
// payload (directly after bundle_id, matching kenaz-fleet's struct order) and
// that a bundle WITHOUT key_id marshals exactly as before the field existed.
func TestSigningPayload_KeyIDFieldOrder(t *testing.T) {
	b := sampleBundle()
	b.KeyID = "0123456789abcdef"
	p, err := b.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(p), `{"bundle_id":42,"key_id":"0123456789abcdef","issued_at":`) {
		t.Errorf("key_id must sit directly after bundle_id in the signing payload, got: %s", p)
	}

	b.KeyID = ""
	p, err = b.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(p), "key_id") {
		t.Errorf("a bundle without key_id must not emit key_id in its signing payload, got: %s", p)
	}

	// Byte equality against the PRE-key_id payload shape: a bundle with an
	// empty KeyID must sign exactly the bytes a pre-key_id signer signed, for
	// a minimal and a fully populated bundle.
	type legacyBundleSigningPayload struct {
		BundleID           int64                      `json:"bundle_id"`
		IssuedAt           time.Time                  `json:"issued_at"`
		CedarDelta         json.RawMessage            `json:"cedar_delta,omitempty"`
		MCPAllowlist       []string                   `json:"mcp_allowlist"`
		ModelPrefs         *BundleModelPrefs          `json:"model_prefs,omitempty"`
		KameasMLWeightURLs []string                   `json:"kameas_ml_weight_urls,omitempty"`
		MandatedSkills     []json.RawMessage          `json:"mandated_skills,omitempty"`
		ProvisionedMCP     []ProvisionedMCP           `json:"provisioned_mcp,omitempty"`
		ProviderSetups     []ProviderSetup            `json:"provider_setups,omitempty"`
		OrgConfig          map[string]json.RawMessage `json:"org_config,omitempty"`
	}
	legacyOf := func(b *Bundle) []byte {
		out, err := json.Marshal(legacyBundleSigningPayload{
			BundleID: b.BundleID, IssuedAt: b.IssuedAt, CedarDelta: b.CedarDelta,
			MCPAllowlist: b.MCPAllowlist, ModelPrefs: b.ModelPrefs,
			KameasMLWeightURLs: b.KameasMLWeightURLs, MandatedSkills: b.MandatedSkills,
			ProvisionedMCP: b.ProvisionedMCP, ProviderSetups: b.ProviderSetups,
			OrgConfig: b.OrgConfig,
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	populated := sampleBundle()
	populated.KameasMLWeightURLs = []string{"https://example.invalid/w"}
	populated.MandatedSkills = []json.RawMessage{json.RawMessage(`{"id":"s1"}`)}
	populated.ProvisionedMCP = []ProvisionedMCP{{RecipeID: "slack", PrimaryAuth: "oauth"}}
	populated.ProviderSetups = []ProviderSetup{{Provider: "anthropic", AccessMode: "byo_key"}}
	populated.OrgConfig = map[string]json.RawMessage{"b": json.RawMessage(`{}`), "a": json.RawMessage(`[]`)}
	for name, bb := range map[string]*Bundle{"minimal": {BundleID: 1}, "populated": populated} {
		got, err := bb.signingPayload()
		if err != nil {
			t.Fatal(err)
		}
		if want := legacyOf(bb); string(got) != string(want) {
			t.Errorf("%s: empty-KeyID payload differs from the pre-key_id payload:\n got %s\nwant %s", name, got, want)
		}
	}
}

// TestConfigPoller_UnknownKeyID_SurfacesInStatus drives the REAL poller:
// a bundle signed by a key this build never pinned is hard-rejected,
// Status().SigningKeyUnknown is set, LastError says "bundle signed with an
// unknown key", and bundle_id does not advance. A following bundle under a
// pinned key clears the flag.
func TestConfigPoller_UnknownKeyID_SurfacesInStatus(t *testing.T) {
	pinned, pinnedPriv := newTestKeyPair(t)
	_, foreignPriv := newTestKeyPair(t)
	restore := SetSigningKeyForTesting(pinned)
	t.Cleanup(restore)

	foreign := sampleBundle()
	foreign.BundleID = 1
	foreign.KeyID = SigningKeyID(foreignPriv.Public().(ed25519.PublicKey))
	signBundle(t, foreignPriv, foreign)

	fake := &fakeFleetConfigServer{response: bundleToJSON(t, foreign)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	p := newPollerForTest(t, srv, applier, t.TempDir())
	if err := p.poll(context.Background()); !errors.Is(err, ErrSigningKeyUnknown) {
		t.Fatalf("poll: expected ErrSigningKeyUnknown, got: %v", err)
	}
	st := p.Status()
	if !st.SigningKeyUnknown {
		t.Error("Status().SigningKeyUnknown = false after an unknown-key rejection")
	}
	if !strings.Contains(st.LastError, "bundle signed with an unknown key") {
		t.Errorf("LastError should say the bundle was signed with an unknown key, got: %q", st.LastError)
	}
	if st.LastAppliedID != 0 {
		t.Errorf("LastAppliedID advanced to %d on a rejected bundle", st.LastAppliedID)
	}
	if n := len(applier.snapshot()); n != 0 {
		t.Errorf("applier ran %d time(s) for a bundle signed with an unknown key", n)
	}

	// A transient failure (fleet 500) must NOT flap the unknown-key state off.
	fake.mu.Lock()
	fake.status = http.StatusInternalServerError
	fake.mu.Unlock()
	if err := p.poll(context.Background()); err == nil {
		t.Fatal("poll against a 500 should fail")
	}
	if st := p.Status(); !st.SigningKeyUnknown || !strings.Contains(st.LastError, "500") {
		t.Errorf("after a transient 500: SigningKeyUnknown=%v LastError=%q, want true and the 500", st.SigningKeyUnknown, st.LastError)
	}
	fake.mu.Lock()
	fake.status = 0
	fake.mu.Unlock()

	// A different hard rejection of a served body clears it.
	fake.setResponse([]byte("{not json"))
	_ = p.poll(context.Background())
	if p.Status().SigningKeyUnknown {
		t.Error("an unparseable served bundle is a different rejection; SigningKeyUnknown should clear")
	}
	fake.setResponse(bundleToJSON(t, foreign))
	if err := p.poll(context.Background()); !errors.Is(err, ErrSigningKeyUnknown) || !p.Status().SigningKeyUnknown {
		t.Fatalf("re-serving the foreign bundle: err=%v flag=%v", err, p.Status().SigningKeyUnknown)
	}

	// Next bundle is signed by the pinned key → flag clears.
	good := sampleBundle()
	good.BundleID = 2
	good.KeyID = SigningKeyID(pinned)
	signBundle(t, pinnedPriv, good)
	fake.setResponse(bundleToJSON(t, good))
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll (pinned key): %v", err)
	}
	if st := p.Status(); st.SigningKeyUnknown || st.LastAppliedID != 2 {
		t.Errorf("after a pinned-key bundle: SigningKeyUnknown=%v LastAppliedID=%d, want false/2", st.SigningKeyUnknown, st.LastAppliedID)
	}
}

// ── F2: degenerate / small-order pins are rejected at parse ────────────────

// identityPointEnc is the canonical encoding of the edwards25519 identity
// (x=0, y=1).
func identityPointEnc() []byte {
	b := make([]byte, 32)
	b[0] = 1
	return b
}

// TestParseSigningKeySet_IdentityPinRejected is the review probe: with the
// identity point as a "public key", the forged signature (R = identity,
// S = 0) verifies under ed25519.Verify for ANY bundle — so pinning it would
// let anyone sign config. Parsing a set containing it must reject the whole
// set.
func TestParseSigningKeySet_IdentityPinRejected(t *testing.T) {
	ident := ed25519.PublicKey(identityPointEnc())

	// The hazard, demonstrated: a forged signature verifies under identity.
	b := sampleBundle()
	b.KeyID = SigningKeyID(ident)
	forged := make([]byte, ed25519.SignatureSize)
	copy(forged, identityPointEnc()) // R = identity, S = 0
	b.Signature = base64.RawStdEncoding.EncodeToString(forged)
	if err := VerifyWithKeySet(b, []ed25519.PublicKey{ident}, 0); err != nil {
		t.Logf("note: forged identity signature did not verify on this Go version (%v); the parse guard is still required", err)
	}

	good, _ := newTestKeyPair(t)
	setPinVars(t, hexOf(good)+","+hex.EncodeToString(ident), "")
	if keys := FleetSigningKeys(); keys != nil {
		t.Fatalf("a set pinning the identity point must be rejected whole, got %d key(s)", len(keys))
	}
	if err := FleetSigningKeySetError(); err == nil || !errors.Is(err, ErrSigningKeyNotConfigured) || !strings.Contains(err.Error(), "small-order") {
		t.Fatalf("FleetSigningKeySetError = %v, want a small-order rejection wrapping ErrSigningKeyNotConfigured", err)
	}
	// And the forged bundle therefore cannot verify through the real path.
	if err := VerifyWithKeySet(b, FleetSigningKeys(), 0); !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Fatalf("forged bundle against the parsed (rejected) set: %v, want ErrSigningKeyNotConfigured", err)
	}
}

// TestCheckPinnableEd25519Key covers the degenerate encodings and confirms
// real keys are never falsely rejected.
func TestCheckPinnableEd25519Key(t *testing.T) {
	mustHex := func(s string) []byte {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	bad := map[string][]byte{
		"identity (order 1)":           identityPointEnc(),
		"all-zero (order 4)":           make([]byte, 32),
		"y=-1 (order 2)":               mustHex("ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f"),
		"order-8 point":                mustHex("26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05"),
		"order-8 point (other)":        mustHex("c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a"),
		"non-canonical identity y=p+1": mustHex("eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f"),
		"non-canonical -0 identity":    append(identityPointEnc()[:31:31], 0x80),
		"all-ff (y>=p)":                mustHex(strings.Repeat("ff", 32)),
	}
	for name, enc := range bad {
		if err := checkPinnableEd25519Key(enc); err == nil {
			t.Errorf("%s: accepted, want rejection", name)
		}
	}
	for i := 0; i < 200; i++ {
		pub, _ := newTestKeyPair(t)
		if err := checkPinnableEd25519Key(pub); err != nil {
			t.Fatalf("real key %x falsely rejected: %v", []byte(pub), err)
		}
	}
	// The ed25519 base point is a valid prime-order key.
	if err := checkPinnableEd25519Key(mustHex("5866666666666666666666666666666666666666666666666666666666666666")); err != nil {
		t.Errorf("base point rejected: %v", err)
	}
}
