package fleet

// credential_crypto_test.go — provider-credentials-client-01PCCLI01 §8;
// kenaz-fleet contract-draft 01NPCRED01 §3/§4/§7.
//
// TestCredentialV1Vectors pins the four constructions (device credential
// key, §3 wrap, §4 seed KEK, §4 personal ciphertext) plus seed_key_check
// three independent ways:
//
//  1. golden hex constants below;
//  2. testdata/credentials/vectors.json — the file Swift and Fleet CI
//     consume; it must equal what the production helpers produce, so it
//     can never drift from this code;
//  3. an in-test reconstruction that calls NO production helper or label
//     constant: HKDF-SHA256 by hand from HMAC (RFC 5869, salt=nil ⇒ 32
//     zero bytes), X25519 via golang.org/x/crypto/curve25519 (not
//     crypto/ecdh), and XChaCha20-Poly1305 by hand from HChaCha20 + IETF
//     ChaCha20-Poly1305, with every info string and AAD spelled as a
//     literal.
//
// Regenerate the JSON (only after a deliberate, versioned label change):
//
//	KENAZ_REGEN_CREDENTIAL_VECTORS=1 go test ./core/fleet -run TestCredentialV1Vectors

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"golang.org/x/crypto/curve25519"
)

const credVectorsPath = "testdata/credentials/vectors.json"

// ── fixed inputs (none is a real key; all are low-entropy patterns) ──
const (
	cvNodeID             = "01J9TESTNODE0000000000000A"
	cvUserID             = "11111111-2222-4333-8444-555555555555"
	cvOrgCredentialID    = "aaaaaaaa-0000-4000-8000-000000000001"
	cvOrgVersion         = uint64(3)
	cvPersonalCredID     = "bbbbbbbb-0000-4000-8000-000000000002"
	cvPersonalVersion    = uint64(2)
	cvUCKVersion         = uint64(5)
	cvKeyID              = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	cvOrgSecretText      = "kenaz-test-org-secret-0001"
	cvPersonalSecretText = "kenaz-test-personal-secret-0002"
)

func cvSeed() []byte            { return seq32() }
func cvUCK() []byte             { return fill(0x33, 32) }
func cvEphSecretWrap() []byte   { return fill(0x11, 32) }
func cvEphUCKWrap() []byte      { return fill(0x12, 32) }
func cvNonceSecretWrap() []byte { return fill(0x22, 24) }
func cvNonceUCKDevice() []byte  { return fill(0x23, 24) }
func cvNonceUCKSeed() []byte    { return fill(0x24, 24) }
func cvNoncePersonal() []byte   { return fill(0x25, 24) }

// ── golden outputs ──
const (
	// Generated from this construction and cross-checked by the manual
	// reconstruction and vectors.json in the same test.
	wantCredentialPubHex  = "8d6319ae39dabbc300c0008fd007c447b9b0514d24e84da380fe10d6caeba449"
	wantWrappedSecretHex  = "738aa59e59a8369dfac1ea844e270bd1fadb0726569696164a49361d139beb777501d6d8c381b613f07b"
	wantUCKSeedWrapHex    = "0c6d2d2d4e5555d6ef2e9a41c274eaa00ac421ced019edf066cc2da5dcafda0e379bf7406c290e46b3a21ef79cb81a5c"
	wantUCKDeviceWrapHex  = "11d3798cde4342cada78b5989a9f6132ac6521f92c224ef7f04b5cd00c195a968a929892a2dffc24cec90cde7b798ee9"
	wantPersonalCTHex     = "d668e29bd6bfb16385341488c4a2640d6fd128d9b97332f29df3be37cb65f5604f32d4f80b518326556cad0efdb679"
	wantSeedKeyCheckValue = "729f801123881585ffefedaf7ffd3c1c"
)

// ── vectors.json schema ──

type cvInputs struct {
	SeedHex                   string `json:"seed_hex"`
	NodeID                    string `json:"node_id"`
	UserID                    string `json:"user_id"`
	KeyID                     string `json:"key_id"`
	OrgCredentialID           string `json:"org_credential_id"`
	OrgCredentialVersion      uint64 `json:"org_credential_version"`
	PersonalCredentialID      string `json:"personal_credential_id"`
	PersonalCredentialVersion uint64 `json:"personal_credential_version"`
	UCKVersion                uint64 `json:"uck_version"`
	UCKHex                    string `json:"uck_hex"`
	OrgSecretUTF8             string `json:"org_secret_utf8"`
	OrgSecretHex              string `json:"org_secret_hex"`
	PersonalSecretUTF8        string `json:"personal_secret_utf8"`
	PersonalSecretHex         string `json:"personal_secret_hex"`
}

type cvIdentity struct {
	Info             string `json:"info"`
	InfoHex          string `json:"info_hex"`
	HKDFOutputHex    string `json:"hkdf_output_hex"`
	ClampedScalarHex string `json:"clamped_scalar_hex"`
	PublicKeyHex     string `json:"public_key_hex"`
}

type cvDeviceWrap struct {
	Info               string `json:"info"`
	EphemeralScalarHex string `json:"ephemeral_scalar_hex"`
	EphemeralPublicHex string `json:"ephemeral_public_key_hex"`
	RecipientPublicHex string `json:"recipient_public_key_hex"`
	SharedHex          string `json:"x25519_shared_hex"`
	KEKHex             string `json:"kek_hex"`
	NonceHex           string `json:"nonce_hex"`
	AAD                string `json:"aad"`
	AADHex             string `json:"aad_hex"`
	PayloadHex         string `json:"payload_hex"`
	SealedHex          string `json:"sealed_hex"`
}

type cvSymSeal struct {
	Info       string `json:"info,omitempty"`
	KeyHex     string `json:"aead_key_hex"`
	NonceHex   string `json:"nonce_hex"`
	AAD        string `json:"aad"`
	AADHex     string `json:"aad_hex"`
	PayloadHex string `json:"payload_hex"`
	SealedHex  string `json:"sealed_hex"`
}

type cvSeedKeyCheck struct {
	Info  string `json:"info"`
	Value string `json:"value"`
}

type cvFile struct {
	Schema         string         `json:"schema"`
	Contract       string         `json:"contract"`
	Inputs         cvInputs       `json:"inputs"`
	Identity       cvIdentity     `json:"credential_identity"`
	WrappedSecret  cvDeviceWrap   `json:"wrapped_secret"`
	UCKDeviceWrap  cvDeviceWrap   `json:"uck_device_wrap"`
	UCKSeedWrap    cvSymSeal      `json:"uck_seed_wrap"`
	PersonalSecret cvSymSeal      `json:"personal_ciphertext"`
	SeedKeyCheck   cvSeedKeyCheck `json:"seed_key_check"`
}

func hx(b []byte) string { return hex.EncodeToString(b) }

func cvInputsBlock() cvInputs {
	return cvInputs{
		SeedHex: hx(cvSeed()), NodeID: cvNodeID, UserID: cvUserID, KeyID: cvKeyID,
		OrgCredentialID: cvOrgCredentialID, OrgCredentialVersion: cvOrgVersion,
		PersonalCredentialID: cvPersonalCredID, PersonalCredentialVersion: cvPersonalVersion,
		UCKVersion: cvUCKVersion, UCKHex: hx(cvUCK()),
		OrgSecretUTF8: cvOrgSecretText, OrgSecretHex: hx([]byte(cvOrgSecretText)),
		PersonalSecretUTF8: cvPersonalSecretText, PersonalSecretHex: hx([]byte(cvPersonalSecretText)),
	}
}

func cvHeader(f *cvFile) {
	f.Schema = "kenaz-credential-vectors/v1"
	f.Contract = "kenaz-fleet kitty-specs/provider-credentials-01NPCRED01/contract-draft.md §3 §4 §7; kenaz-harness provider-credentials-client-01PCCLI01 spec §8"
	f.Inputs = cvInputsBlock()
}

// productionVectors builds every vector with the PRODUCTION helpers.
func productionVectors(t *testing.T) cvFile {
	t.Helper()
	seed := cvSeed()
	var f cvFile
	cvHeader(&f)

	// identity
	idInfo := credentialIdentityV1Info + cvNodeID
	raw, err := hkdf32(seed, idInfo)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := DeriveCredentialPrivKey(seed, cvNodeID)
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.PublicKey().Bytes()
	f.Identity = cvIdentity{Info: idInfo, InfoHex: hx([]byte(idInfo)), HKDFOutputHex: hx(raw),
		ClampedScalarHex: hx(priv.Bytes()), PublicKeyHex: hx(pub)}

	deviceWrap := func(ephScalar, nonce, payload, aad []byte) cvDeviceWrap {
		eph, err := ecdh.X25519().NewPrivateKey(ephScalar)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := sealToCredentialKeyWith(eph, pub, nonce, payload, aad)
		if err != nil {
			t.Fatal(err)
		}
		shared, err := eph.ECDH(priv.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		kek, err := credentialWrapKEK(shared)
		if err != nil {
			t.Fatal(err)
		}
		return cvDeviceWrap{Info: string(LabelCredentialWrapV1), EphemeralScalarHex: hx(ephScalar),
			EphemeralPublicHex: hx(eph.PublicKey().Bytes()), RecipientPublicHex: hx(pub),
			SharedHex: hx(shared), KEKHex: hx(kek), NonceHex: hx(nonce), AAD: string(aad), AADHex: hx(aad),
			PayloadHex: hx(payload), SealedHex: hx(sealed)}
	}
	f.WrappedSecret = deviceWrap(cvEphSecretWrap(), cvNonceSecretWrap(), []byte(cvOrgSecretText),
		credentialWrapAAD(cvOrgCredentialID, cvOrgVersion, cvKeyID))
	f.UCKDeviceWrap = deviceWrap(cvEphUCKWrap(), cvNonceUCKDevice(), cvUCK(),
		uckDeviceWrapAAD(cvUserID, cvUCKVersion, cvKeyID))

	// seed path
	kekSeed, err := userCredentialsSeedKEK(seed)
	if err != nil {
		t.Fatal(err)
	}
	seedWrap, err := sealUCKWithSeedWith(seed, cvUserID, cvUCKVersion, cvUCK(), cvNonceUCKSeed())
	if err != nil {
		t.Fatal(err)
	}
	sAAD := uckSeedWrapAAD(cvUserID, cvUCKVersion)
	f.UCKSeedWrap = cvSymSeal{Info: string(LabelUserCredentialsKEKV1), KeyHex: hx(kekSeed), NonceHex: hx(cvNonceUCKSeed()),
		AAD: string(sAAD), AADHex: hx(sAAD), PayloadHex: hx(cvUCK()), SealedHex: hx(seedWrap)}

	// personal
	pct, err := sealPersonalSecretWith(cvUCK(), cvUserID, cvPersonalCredID, cvPersonalVersion, []byte(cvPersonalSecretText), cvNoncePersonal())
	if err != nil {
		t.Fatal(err)
	}
	pAAD := personalSecretAAD(cvUserID, cvPersonalCredID, cvPersonalVersion)
	f.PersonalSecret = cvSymSeal{KeyHex: hx(cvUCK()), NonceHex: hx(cvNoncePersonal()), AAD: string(pAAD), AADHex: hx(pAAD),
		PayloadHex: hx([]byte(cvPersonalSecretText)), SealedHex: hx(pct)}

	skc, err := SeedKeyCheck(seed)
	if err != nil {
		t.Fatal(err)
	}
	f.SeedKeyCheck = cvSeedKeyCheck{Info: string(LabelSeedKeyCheckV1), Value: skc}
	return f
}

// manualVectors rebuilds every vector WITHOUT production helpers or label
// constants: literals + manualHKDF32 + curve25519 + manualXSeal.
func manualVectors(t *testing.T) cvFile {
	t.Helper()
	seed := cvSeed()
	var f cvFile
	cvHeader(&f)

	idInfo := "kenaz-credential-identity-v1:" + cvNodeID
	raw := manualHKDF32(seed, idInfo)
	clamped := append([]byte(nil), raw...)
	clamped[0] &= 248
	clamped[31] &= 127
	clamped[31] |= 64
	pub, err := curve25519.X25519(clamped, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	f.Identity = cvIdentity{Info: idInfo, InfoHex: hx([]byte(idInfo)), HKDFOutputHex: hx(raw),
		ClampedScalarHex: hx(clamped), PublicKeyHex: hx(pub)}

	deviceWrap := func(ephScalar, nonce, payload []byte, aad string) cvDeviceWrap {
		ephPub, err := curve25519.X25519(ephScalar, curve25519.Basepoint)
		if err != nil {
			t.Fatal(err)
		}
		shared, err := curve25519.X25519(ephScalar, pub)
		if err != nil {
			t.Fatal(err)
		}
		// Receiver side must agree: X25519(device_sk, eph_pub).
		shared2, err := curve25519.X25519(clamped, ephPub)
		if err != nil || !bytes.Equal(shared, shared2) {
			t.Fatalf("manual ECDH asymmetry: %v", err)
		}
		kek := manualHKDF32(shared, "kenaz-credential-v1-wrap")
		return cvDeviceWrap{Info: "kenaz-credential-v1-wrap", EphemeralScalarHex: hx(ephScalar),
			EphemeralPublicHex: hx(ephPub), RecipientPublicHex: hx(pub), SharedHex: hx(shared), KEKHex: hx(kek),
			NonceHex: hx(nonce), AAD: aad, AADHex: hx([]byte(aad)), PayloadHex: hx(payload),
			SealedHex: hx(manualXSeal(t, kek, nonce, payload, []byte(aad)))}
	}
	d := func(v uint64) string { return strconv.FormatUint(v, 10) }
	f.WrappedSecret = deviceWrap(cvEphSecretWrap(), cvNonceSecretWrap(), []byte(cvOrgSecretText),
		cvOrgCredentialID+":"+d(cvOrgVersion)+":"+cvKeyID)
	f.UCKDeviceWrap = deviceWrap(cvEphUCKWrap(), cvNonceUCKDevice(), cvUCK(),
		"uck:"+cvUserID+":"+d(cvUCKVersion)+":"+cvKeyID)

	kekSeed := manualHKDF32(seed, "kenaz-user-credentials-kek-v1")
	sAAD := cvUserID + ":uck:" + d(cvUCKVersion)
	f.UCKSeedWrap = cvSymSeal{Info: "kenaz-user-credentials-kek-v1", KeyHex: hx(kekSeed), NonceHex: hx(cvNonceUCKSeed()),
		AAD: sAAD, AADHex: hx([]byte(sAAD)), PayloadHex: hx(cvUCK()),
		SealedHex: hx(manualXSeal(t, kekSeed, cvNonceUCKSeed(), cvUCK(), []byte(sAAD)))}

	pAAD := cvUserID + ":" + cvPersonalCredID + ":" + d(cvPersonalVersion)
	f.PersonalSecret = cvSymSeal{KeyHex: hx(cvUCK()), NonceHex: hx(cvNoncePersonal()), AAD: pAAD, AADHex: hx([]byte(pAAD)),
		PayloadHex: hx([]byte(cvPersonalSecretText)),
		SealedHex:  hx(manualXSeal(t, cvUCK(), cvNoncePersonal(), []byte(cvPersonalSecretText), []byte(pAAD)))}

	// HKDF with L=16 is the first 16 bytes of T(1).
	f.SeedKeyCheck = cvSeedKeyCheck{Info: "kenaz-seed-key-check-v1", Value: hx(manualHKDF32(seed, "kenaz-seed-key-check-v1")[:16])}
	return f
}

func TestCredentialV1Vectors(t *testing.T) {
	prod := productionVectors(t)
	manual := manualVectors(t)

	// (3) independent reconstruction agrees with production, field by field.
	if !reflect.DeepEqual(prod, manual) {
		pj, _ := json.MarshalIndent(prod, "", "  ")
		mj, _ := json.MarshalIndent(manual, "", "  ")
		t.Fatalf("production disagrees with manual reconstruction\nproduction:\n%s\nmanual:\n%s", pj, mj)
	}

	// Literal AADs, so a separator or encoding drift is legible.
	for _, c := range []struct{ name, got, want string }{
		{"wrapped_secret aad", prod.WrappedSecret.AAD, "aaaaaaaa-0000-4000-8000-000000000001:3:7c9e6679-7425-40de-944b-e07fc1f90ae7"},
		{"uck_device_wrap aad", prod.UCKDeviceWrap.AAD, "uck:11111111-2222-4333-8444-555555555555:5:7c9e6679-7425-40de-944b-e07fc1f90ae7"},
		{"uck_seed_wrap aad", prod.UCKSeedWrap.AAD, "11111111-2222-4333-8444-555555555555:uck:5"},
		{"personal aad", prod.PersonalSecret.AAD, "11111111-2222-4333-8444-555555555555:bbbbbbbb-0000-4000-8000-000000000002:2"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if os.Getenv("KENAZ_REGEN_CREDENTIAL_VECTORS") == "1" {
		out, err := json.MarshalIndent(prod, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(credVectorsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(credVectorsPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", credVectorsPath)
	}

	// (1) golden hex.
	for _, c := range []struct{ name, got, want string }{
		{"credential public key", prod.Identity.PublicKeyHex, wantCredentialPubHex},
		{"wrapped secret", prod.WrappedSecret.SealedHex, wantWrappedSecretHex},
		{"wrapped UCK (seed path)", prod.UCKSeedWrap.SealedHex, wantUCKSeedWrapHex},
		{"wrapped UCK (device path)", prod.UCKDeviceWrap.SealedHex, wantUCKDeviceWrapHex},
		{"personal ciphertext", prod.PersonalSecret.SealedHex, wantPersonalCTHex},
		{"seed_key_check", prod.SeedKeyCheck.Value, wantSeedKeyCheckValue},
	} {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}

	// (2) the shared vectors file matches, exactly, and has no unknown fields.
	b, err := os.ReadFile(credVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", credVectorsPath, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var onDisk cvFile
	if err := dec.Decode(&onDisk); err != nil {
		t.Fatalf("decode %s: %v", credVectorsPath, err)
	}
	if !reflect.DeepEqual(onDisk, prod) {
		t.Fatalf("%s drifted from the production construction; regenerate only for a deliberate versioned label change", credVectorsPath)
	}

	// Sizes Fleet / Swift depend on.
	if n := len(prod.UCKSeedWrap.SealedHex) / 2; n != 48 {
		t.Errorf("uck_seed_wrap sealed = %d bytes, want 48", n)
	}
	if n := len(prod.UCKDeviceWrap.SealedHex) / 2; n != 48 {
		t.Errorf("uck_device_wrap sealed = %d bytes, want 48", n)
	}
	if n := len(prod.SeedKeyCheck.Value); n != 32 {
		t.Errorf("seed_key_check = %d hex chars, want 32", n)
	}

	// Open every golden ciphertext with the production open paths.
	priv, _ := DeriveCredentialPrivKey(cvSeed(), cvNodeID)
	mustHex := func(s string) []byte {
		v, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	ws := prod.WrappedSecret
	if got, err := openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID,
		mustHex(ws.EphemeralPublicHex), mustHex(ws.NonceHex), mustHex(ws.SealedHex)); err != nil || string(got) != cvOrgSecretText {
		t.Fatalf("open wrapped secret: %v", err)
	}
	dw := prod.UCKDeviceWrap
	if got, err := openUCKDeviceWrap(priv, cvUserID, cvUCKVersion, cvKeyID,
		mustHex(dw.EphemeralPublicHex), mustHex(dw.NonceHex), mustHex(dw.SealedHex)); err != nil || !bytes.Equal(got, cvUCK()) {
		t.Fatalf("open uck device wrap: %v", err)
	}
	if got, err := openUCKSeedWrap(cvSeed(), cvUserID, cvUCKVersion, cvNonceUCKSeed(), mustHex(prod.UCKSeedWrap.SealedHex)); err != nil || !bytes.Equal(got, cvUCK()) {
		t.Fatalf("open uck seed wrap: %v", err)
	}
	if got, err := openPersonalSecret(cvUCK(), cvUserID, cvPersonalCredID, cvPersonalVersion, cvNoncePersonal(), mustHex(prod.PersonalSecret.SealedHex)); err != nil || string(got) != cvPersonalSecretText {
		t.Fatalf("open personal secret: %v", err)
	}
}

// D7: the credential key is a separate purpose — same seed + node id must
// NOT give the handoff key; same inputs re-derive; another node differs.
func TestCredentialIdentity_SeparateFromHandoff(t *testing.T) {
	c1, err := DeriveCredentialPrivKey(cvSeed(), cvNodeID)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := DeriveCredentialPrivKey(cvSeed(), cvNodeID)
	h, _ := DeriveHandoffPrivKey(cvSeed(), cvNodeID)
	other, _ := DeriveCredentialPrivKey(cvSeed(), "node-b")
	if !bytes.Equal(c1.PublicKey().Bytes(), c2.PublicKey().Bytes()) {
		t.Fatal("same seed + node id must re-derive the same credential key")
	}
	if bytes.Equal(c1.PublicKey().Bytes(), h.PublicKey().Bytes()) {
		t.Fatal("credential key must differ from the handoff key (D7)")
	}
	if bytes.Equal(c1.PublicKey().Bytes(), other.PublicKey().Bytes()) {
		t.Fatal("another node id must derive a different credential key")
	}
	if _, err := DeriveCredentialPrivKey(cvSeed(), " "); !errors.Is(err, ErrCredentialIdentityUnavailable) {
		t.Fatalf("empty node id: %v", err)
	}
	if _, err := DeriveCredentialPrivKey(make([]byte, 16), cvNodeID); err == nil {
		t.Fatal("short seed must be refused")
	}
}

func flip(b []byte, i int) []byte {
	c := append([]byte(nil), b...)
	c[i] ^= 0x01
	return c
}

func wantOpenFailed(t *testing.T, name string, _ []byte, err error) {
	t.Helper()
	if !errors.Is(err, ErrCredentialOpenFailed) {
		t.Errorf("%s: err = %v, want ErrCredentialOpenFailed", name, err)
	}
}

// Round trip with fresh randomness, then wrong-AAD, wrong-key and tamper
// failures for every construction.
func TestCredentialV1_RoundTripAndFailures(t *testing.T) {
	seed := cvSeed()
	otherSeed := fill(0x77, 32)
	priv, _ := DeriveCredentialPrivKey(seed, cvNodeID)
	otherPriv, _ := DeriveCredentialPrivKey(seed, "node-b")
	uck := make([]byte, 32)
	if _, err := rand.Read(uck); err != nil {
		t.Fatal(err)
	}
	secret := []byte(cvOrgSecretText)

	// ── §3 wrapped org/team secret ──
	w, err := sealToCredentialKey(rand.Reader, priv.PublicKey().Bytes(), cvKeyID, secret,
		credentialWrapAAD(cvOrgCredentialID, cvOrgVersion, cvKeyID))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID, w.EphemeralPublicKey, w.Nonce, w.Sealed); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("wrapped secret round trip: %v", err)
	}
	g, e := openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion+1, cvKeyID, w.EphemeralPublicKey, w.Nonce, w.Sealed)
	wantOpenFailed(t, "wrap: wrong version", g, e)
	g, e = openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, "other-key-id", w.EphemeralPublicKey, w.Nonce, w.Sealed)
	wantOpenFailed(t, "wrap: wrong key_id", g, e)
	g, e = openWrappedSecret(priv, cvPersonalCredID, cvOrgVersion, cvKeyID, w.EphemeralPublicKey, w.Nonce, w.Sealed)
	wantOpenFailed(t, "wrap: wrong credential_id", g, e)
	g, e = openWrappedSecret(otherPriv, cvOrgCredentialID, cvOrgVersion, cvKeyID, w.EphemeralPublicKey, w.Nonce, w.Sealed)
	wantOpenFailed(t, "wrap: wrong device key", g, e)
	g, e = openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID, w.EphemeralPublicKey, w.Nonce, flip(w.Sealed, 0))
	wantOpenFailed(t, "wrap: tampered sealed", g, e)
	g, e = openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID, w.EphemeralPublicKey, flip(w.Nonce, 3), w.Sealed)
	wantOpenFailed(t, "wrap: tampered nonce", g, e)
	g, e = openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID, flip(w.EphemeralPublicKey, 5), w.Nonce, w.Sealed)
	wantOpenFailed(t, "wrap: tampered ephemeral key", g, e)

	// ── §4 path (ii) UCK device wrap ──
	dw, err := wrapUCKToDevice(rand.Reader, priv.PublicKey().Bytes(), cvUserID, cvUCKVersion, cvKeyID, uck)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openUCKDeviceWrap(priv, cvUserID, cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed); err != nil || !bytes.Equal(got, uck) {
		t.Fatalf("uck device wrap round trip: %v", err)
	}
	g, e = openUCKDeviceWrap(priv, cvUserID, cvUCKVersion+1, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed)
	wantOpenFailed(t, "uck device: wrong uck_version", g, e)
	g, e = openUCKDeviceWrap(priv, "99999999-2222-4333-8444-555555555555", cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed)
	wantOpenFailed(t, "uck device: wrong user_id", g, e)
	g, e = openUCKDeviceWrap(otherPriv, cvUserID, cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed)
	wantOpenFailed(t, "uck device: wrong device key", g, e)
	g, e = openUCKDeviceWrap(priv, cvUserID, cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, flip(dw.Sealed, 47))
	wantOpenFailed(t, "uck device: tampered tag", g, e)
	// A UCK device wrap must not open as an org secret wrap for the same key (domain separation by AAD).
	g, e = openWrappedSecret(priv, cvUserID, cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed)
	wantOpenFailed(t, "uck device wrap opened as secret wrap", g, e)

	// ── §4 path (i) seed wrap ──
	sw, sn, err := sealUCKWithSeed(rand.Reader, seed, cvUserID, cvUCKVersion, uck)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openUCKSeedWrap(seed, cvUserID, cvUCKVersion, sn, sw); err != nil || !bytes.Equal(got, uck) {
		t.Fatalf("uck seed wrap round trip: %v", err)
	}
	g, e = openUCKSeedWrap(seed, cvUserID, cvUCKVersion+1, sn, sw)
	wantOpenFailed(t, "uck seed: wrong uck_version", g, e)
	g, e = openUCKSeedWrap(seed, "99999999-2222-4333-8444-555555555555", cvUCKVersion, sn, sw)
	wantOpenFailed(t, "uck seed: wrong user_id", g, e)
	g, e = openUCKSeedWrap(otherSeed, cvUserID, cvUCKVersion, sn, sw)
	wantOpenFailed(t, "uck seed: wrong seed", g, e)
	g, e = openUCKSeedWrap(seed, cvUserID, cvUCKVersion, sn, flip(sw, 10))
	wantOpenFailed(t, "uck seed: tampered", g, e)

	// ── §4 personal secret under the UCK ──
	pct, pn, err := sealPersonalSecret(rand.Reader, uck, cvUserID, cvPersonalCredID, cvPersonalVersion, []byte(cvPersonalSecretText))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openPersonalSecret(uck, cvUserID, cvPersonalCredID, cvPersonalVersion, pn, pct); err != nil || string(got) != cvPersonalSecretText {
		t.Fatalf("personal round trip: %v", err)
	}
	g, e = openPersonalSecret(uck, cvUserID, cvPersonalCredID, cvPersonalVersion+1, pn, pct)
	wantOpenFailed(t, "personal: wrong version", g, e)
	g, e = openPersonalSecret(uck, cvUserID, cvOrgCredentialID, cvPersonalVersion, pn, pct)
	wantOpenFailed(t, "personal: wrong credential_id", g, e)
	g, e = openPersonalSecret(uck, "99999999-2222-4333-8444-555555555555", cvPersonalCredID, cvPersonalVersion, pn, pct)
	wantOpenFailed(t, "personal: wrong user_id", g, e)
	g, e = openPersonalSecret(fill(0x44, 32), cvUserID, cvPersonalCredID, cvPersonalVersion, pn, pct)
	wantOpenFailed(t, "personal: wrong uck", g, e)
	g, e = openPersonalSecret(uck, cvUserID, cvPersonalCredID, cvPersonalVersion, pn, flip(pct, len(pct)-1))
	wantOpenFailed(t, "personal: tampered", g, e)

	// ── seed_key_check differs per seed ──
	a, _ := SeedKeyCheck(seed)
	b, _ := SeedKeyCheck(otherSeed)
	if a == b {
		t.Fatal("seed_key_check must differ for different seeds")
	}
}

// H3 interop: the seed path and the device path wrap the SAME UCK, so a
// personal secret written via one is readable via the other.
func TestCredentialV1_PathsInteroperate(t *testing.T) {
	seed := cvSeed()
	priv, _ := DeriveCredentialPrivKey(seed, cvNodeID)
	uck := cvUCK()
	sw, sn, err := sealUCKWithSeed(rand.Reader, seed, cvUserID, cvUCKVersion, uck)
	if err != nil {
		t.Fatal(err)
	}
	dw, err := wrapUCKToDevice(rand.Reader, priv.PublicKey().Bytes(), cvUserID, cvUCKVersion, cvKeyID, uck)
	if err != nil {
		t.Fatal(err)
	}
	viaSeed, err := openUCKSeedWrap(seed, cvUserID, cvUCKVersion, sn, sw)
	if err != nil {
		t.Fatal(err)
	}
	viaDevice, err := openUCKDeviceWrap(priv, cvUserID, cvUCKVersion, cvKeyID, dw.EphemeralPublicKey, dw.Nonce, dw.Sealed)
	if err != nil {
		t.Fatal(err)
	}
	ct, n, err := sealPersonalSecret(rand.Reader, viaSeed, cvUserID, cvPersonalCredID, cvPersonalVersion, []byte(cvPersonalSecretText))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openPersonalSecret(viaDevice, cvUserID, cvPersonalCredID, cvPersonalVersion, n, ct); err != nil || string(got) != cvPersonalSecretText {
		t.Fatalf("device-path UCK must open a seed-path-written secret: %v", err)
	}
}
