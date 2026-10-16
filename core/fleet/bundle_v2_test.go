package fleet

// bundle_v2_test.go — signed-bundle-bytes-01SBBYT01 WP01: verify-then-decode
// over the golden envelopes in testdata/bundle_v2 (spec §4 a–f, §6a g/i).

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func readV2Fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundleV2FixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func v2FixtureKeys() []ed25519.PublicKey {
	pub, _ := bundleV2FixtureKey()
	return []ed25519.PublicKey{pub}
}

// (a) a valid envelope verifies, decodes, and checksums over P.
func TestBundleV2Fixture_A_Valid(t *testing.T) {
	raw := readV2Fixture(t, "a_valid.json")
	vb, err := VerifyEnvelopeBytes(raw, v2FixtureKeys())
	if err != nil {
		t.Fatalf("VerifyEnvelopeBytes: %v", err)
	}
	b := vb.Bundle
	if b.BundleID != 10 || !reflect.DeepEqual(b.MCPAllowlist, []string{"github"}) {
		t.Fatalf("decoded bundle = %+v", b)
	}
	if b.ModelPrefs == nil || b.ModelPrefs.DefaultModel != "claude-sonnet" {
		t.Fatalf("model_prefs = %+v", b.ModelPrefs)
	}
	if len(vb.UnknownFields) != 0 {
		t.Errorf("UnknownFields = %v, want none", vb.UnknownFields)
	}
	sum := sha256.Sum256(vb.Payload)
	if vb.Checksum() != fmt.Sprintf("%x", sum[:]) {
		t.Errorf("Checksum is not hex(SHA-256(P))")
	}
}

// (b) unknown top-level AND nested model_prefs fields: v2 verifies and
// keeps every known field; the SAME bundle in the legacy form fails today's
// re-marshal verification. This is the regression test for spec §1.1/§1.2.
func TestBundleV2Fixture_B_UnknownFields_V2PassesLegacyFails(t *testing.T) {
	keys := v2FixtureKeys()

	vb, err := VerifyEnvelopeBytes(readV2Fixture(t, "b_unknown_fields.json"), keys)
	if err != nil {
		t.Fatalf("v2 path must verify a bundle with unknown fields: %v", err)
	}
	b := vb.Bundle
	if b.BundleID != 11 || !reflect.DeepEqual(b.MCPAllowlist, []string{"github", "slack"}) {
		t.Errorf("known top-level fields not applied: %+v", b)
	}
	if b.ModelPrefs == nil || b.ModelPrefs.DefaultModel != "claude-sonnet" ||
		!reflect.DeepEqual(b.ModelPrefs.ProviderAllowlist, []string{"anthropic"}) {
		t.Errorf("known model_prefs fields not applied: %+v", b.ModelPrefs)
	}
	if want := []string{"future_section", "model_prefs.future_knob"}; !reflect.DeepEqual(vb.UnknownFields, want) {
		t.Errorf("UnknownFields = %v, want %v", vb.UnknownFields, want)
	}

	// Legacy path (today's code): decode drops the unknown fields, the
	// re-marshal differs from what Fleet signed → ErrInvalidSignature.
	var legacy Bundle
	if err := json.Unmarshal(readV2Fixture(t, "b_unknown_fields_legacy.json"), &legacy); err != nil {
		t.Fatalf("decode legacy form: %v", err)
	}
	if err := VerifyWithKeySet(&legacy, keys, 0); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("legacy path on fixture b: err = %v, want ErrInvalidSignature (the §1.1 defect)", err)
	}
	// Control: the legacy path is not broken in general — a legacy bundle
	// with only known fields, same key, verifies.
	var f Bundle
	if err := json.Unmarshal(readV2Fixture(t, "f_legacy.json"), &f); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWithKeySet(&f, keys, 0); err != nil {
		t.Fatalf("control: legacy fixture f must verify on the legacy path: %v", err)
	}
}

func TestBundleV2Fixture_Rejects(t *testing.T) {
	cases := []struct {
		file string
		want error
	}{
		{"c_tampered_payload.json", ErrInvalidSignature},
		{"d_key_id_mismatch.json", ErrBundleKeyIDMismatch},
		{"e_unknown_key.json", ErrSigningKeyUnknown},
		{"g_v1_signature_as_v2.json", ErrInvalidSignature},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			vb, err := VerifyEnvelopeBytes(readV2Fixture(t, tc.file), v2FixtureKeys())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if vb != nil {
				t.Fatal("a rejected envelope must not return a bundle")
			}
		})
	}
	if _, err := VerifyEnvelopeBytes(readV2Fixture(t, "a_valid.json"), nil); !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("no pinned keys: err = %v, want ErrSigningKeyNotConfigured", err)
	}
}

// (g, reverse direction) a v2 signature over P can never pass as a v1
// signature over the same bytes: domain separation holds both ways.
func TestBundleV2_V2SignatureNeverVerifiesAsV1(t *testing.T) {
	_, priv := bundleV2FixtureKey()
	var f Bundle
	if err := json.Unmarshal(readV2Fixture(t, "f_legacy.json"), &f); err != nil {
		t.Fatal(err)
	}
	p, err := f.signingPayload()
	if err != nil {
		t.Fatal(err)
	}
	f.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, append([]byte(bundleV2SigningPrefix), p...)))
	if err := VerifyWithKeySet(&f, v2FixtureKeys(), 0); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("v2 signature on the legacy path: err = %v, want ErrInvalidSignature", err)
	}
}

// (f) an older Fleet's answer has no "format".
func TestBundleV2Fixture_F_LegacyHasNoFormat(t *testing.T) {
	f, err := envelopeFormat(readV2Fixture(t, "f_legacy.json"))
	if err != nil || f != "" {
		t.Fatalf("envelopeFormat(legacy) = %q, %v; want \"\", nil", f, err)
	}
	f, err = envelopeFormat(readV2Fixture(t, "a_valid.json"))
	if err != nil || f != BundleEnvelopeFormatV2 {
		t.Fatalf("envelopeFormat(v2) = %q, %v", f, err)
	}
}

// ── poller-level: the real poll() against a negotiating fake Fleet ─────────

// fakeEnvelopeFleet serves v2 envelopes to requests carrying the v2 Accept header
// and the legacy body otherwise (Fleet ruling 2); 304 compares the v2
// checksum (hex SHA-256 of P, ruling 3) or the legacy body checksum.
type fakeEnvelopeFleet struct {
	mu       sync.Mutex
	envelope []byte // served on Accept: v2 (nil → fall back to legacy)
	legacy   []byte // served without the Accept header, or as an "old Fleet"
	oldFleet bool   // ignore Accept entirely (pre-v2 Fleet)
	status   int    // non-zero forces this status
	accepts  []string
	acks     []int64
}

func (f *fakeEnvelopeFleet) set(fn func(*fakeEnvelopeFleet)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeEnvelopeFleet) snapshot() (accepts []string, acks []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.accepts...), append([]int64(nil), f.acks...)
}

func (f *fakeEnvelopeFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost {
		var payload configACKPayload
		if json.NewDecoder(r.Body).Decode(&payload) == nil {
			f.acks = append(f.acks, payload.BundleID)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	f.accepts = append(f.accepts, r.Header.Get("Accept"))
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write(readV2FixtureNoT("i_406_bundle_format_unsupported.json"))
		return
	}
	body, checksum := f.legacy, ""
	if !f.oldFleet && r.Header.Get("Accept") == BundleAcceptV2 && f.envelope != nil {
		body = f.envelope
		var env BundleEnvelope
		_ = json.Unmarshal(body, &env)
		p, _ := base64.StdEncoding.DecodeString(env.PayloadB64)
		checksum = hexChecksumOf(p)
	} else if body != nil {
		checksum = hexChecksumOf(body)
	}
	if body == nil || r.URL.Query().Get("checksum") == checksum {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func readV2FixtureNoT(name string) []byte {
	raw, _ := os.ReadFile(filepath.Join(bundleV2FixtureDir, name))
	return raw
}

func newV2Poller(t *testing.T, fake *fakeEnvelopeFleet, applier ConfigApplier, dataDir string) *ConfigPoller {
	t.Helper()
	pub, _ := bundleV2FixtureKey()
	setTestSigningKey(t, pub)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return newPollerForTest(t, srv, applier, dataDir)
}

// The poller always asks for v2, applies fixture (b)'s known fields, and
// records the v2 checksum so the next round is a 304.
func TestConfigPoller_V2_AppliesUnknownFieldBundle(t *testing.T) {
	fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "b_unknown_fields.json")}
	applier := &fakeApplier{}
	p := newV2Poller(t, fake, applier, t.TempDir())

	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	got := applier.snapshot()
	if len(got) != 1 || got[0].BundleID != 11 || len(got[0].MCPAllowlist) != 2 {
		t.Fatalf("applied = %+v, want bundle 11 with both allowlisted recipes", got)
	}
	st := p.Status()
	if st.LastAppliedID != 11 || st.LastError != "" || st.Source != "fleet" {
		t.Fatalf("status = %+v", st)
	}
	var env BundleEnvelope
	_ = json.Unmarshal(fake.envelope, &env)
	pBytes, _ := base64.StdEncoding.DecodeString(env.PayloadB64)
	if st.BundleChecksum != hexChecksumOf(pBytes) {
		t.Errorf("checksum = %s, want hex(SHA-256(P))", st.BundleChecksum)
	}
	// Second round: 304 → no re-apply.
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if n := len(applier.snapshot()); n != 1 {
		t.Errorf("a 304 must not re-apply; applied %d times", n)
	}
	accepts, acks := fake.snapshot()
	for _, a := range accepts {
		if a != BundleAcceptV2 {
			t.Errorf("Accept = %q, want %q on every config request", a, BundleAcceptV2)
		}
	}
	if len(acks) != 1 || acks[0] != 11 {
		t.Errorf("acks = %v, want [11]", acks)
	}
}

// (c)/(d)/(e)/(g) through the poller: a hard reject keeps the last applied
// bundle and its checksum.
func TestConfigPoller_V2_RejectKeepsLastApplied(t *testing.T) {
	for _, bad := range []string{"c_tampered_payload.json", "d_key_id_mismatch.json", "e_unknown_key.json", "g_v1_signature_as_v2.json"} {
		t.Run(bad, func(t *testing.T) {
			fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "a_valid.json")}
			applier := &fakeApplier{}
			p := newV2Poller(t, fake, applier, t.TempDir())
			if err := p.poll(context.Background()); err != nil {
				t.Fatalf("first poll: %v", err)
			}
			before := p.Status()
			fake.set(func(f *fakeEnvelopeFleet) { f.envelope = readV2Fixture(t, bad) })
			if err := p.poll(context.Background()); err == nil {
				t.Fatal("poll of a bad envelope returned nil")
			}
			st := p.Status()
			if st.LastAppliedID != 10 || st.BundleChecksum != before.BundleChecksum {
				t.Errorf("last applied bundle not kept: %+v", st)
			}
			if st.LastError == "" {
				t.Error("rejection left no LastError")
			}
			if want := bad == "e_unknown_key.json"; st.SigningKeyUnknown != want {
				t.Errorf("SigningKeyUnknown = %v, want %v", st.SigningKeyUnknown, want)
			}
			if n := len(applier.snapshot()); n != 1 {
				t.Errorf("rejected envelope was applied (%d applies)", n)
			}
		})
	}
}

// A replayed (older) v2 envelope is refused by the monotonic guard.
func TestConfigPoller_V2_MonotonicGuard(t *testing.T) {
	fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "b_unknown_fields.json")} // id 11
	applier := &fakeApplier{}
	p := newV2Poller(t, fake, applier, t.TempDir())
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.set(func(f *fakeEnvelopeFleet) { f.envelope = readV2Fixture(t, "a_valid.json") }) // id 10
	if err := p.poll(context.Background()); !errors.Is(err, ErrBundleIDNonMonotonic) {
		t.Fatalf("err = %v, want ErrBundleIDNonMonotonic", err)
	}
	if st := p.Status(); st.LastAppliedID != 11 {
		t.Errorf("LastAppliedID = %d, want 11", st.LastAppliedID)
	}
}

// (f) an older Fleet ignores the Accept header: the legacy path applies.
func TestConfigPoller_V2_OldFleetFallsBackToLegacy(t *testing.T) {
	legacy := readV2Fixture(t, "f_legacy.json")
	fake := &fakeEnvelopeFleet{legacy: legacy, oldFleet: true}
	applier := &fakeApplier{}
	p := newV2Poller(t, fake, applier, t.TempDir())
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if st := p.Status(); st.LastAppliedID != 14 || st.BundleChecksum != hexChecksumOf(legacy) {
		t.Fatalf("status = %+v, want bundle 14 with the legacy body checksum", st)
	}
}

// (i) 406 bundle_format_unsupported → "update required"; last bundle kept.
func TestConfigPoller_V2_406UpdateRequired(t *testing.T) {
	fake := &fakeEnvelopeFleet{envelope: readV2Fixture(t, "a_valid.json")}
	applier := &fakeApplier{}
	p := newV2Poller(t, fake, applier, t.TempDir())
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.set(func(f *fakeEnvelopeFleet) { f.status = http.StatusNotAcceptable })
	if err := p.poll(context.Background()); !errors.Is(err, ErrBundleFormatUnsupported) {
		t.Fatalf("err = %v, want ErrBundleFormatUnsupported", err)
	}
	st := p.Status()
	if !st.UpdateRequired || st.LastAppliedID != 10 || st.LastError == "" {
		t.Fatalf("status = %+v, want UpdateRequired with bundle 10 kept", st)
	}
	// Fleet serves again (e.g. the build updated): the flag clears.
	fake.set(func(f *fakeEnvelopeFleet) { f.status = 0; f.envelope = readV2Fixture(t, "b_unknown_fields.json") })
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := p.Status(); st.UpdateRequired {
		t.Error("UpdateRequired survived a verified bundle")
	}
}
