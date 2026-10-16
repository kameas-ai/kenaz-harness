package fleet

// credential_crypto_test.go — provider-credentials-client-01PCCLI01 §8,
// §10.1, §10.3; kenaz-fleet contract-draft 01NPCRED01 §3/§4/§7 and Fleet
// PR #268 docs/contract-harness-credentials.md §5, §6.1, §6.2.
//
// TestCredentialVectors pins every construction (device credential key,
// §3 anonymous org/team wrap, §4 seed KEK, §4 personal ciphertext,
// seed_key_check, and the v2 additions: authenticated UCK device wrap,
// pairing commit, pairing SAS, uck_auth) three independent ways:
//
//  1. golden hex constants below;
//  2. testdata/credentials/vectors.json — the file Swift and Fleet CI
//     consume; it must equal what the production helpers produce, so it
//     can never drift from this code;
//  3. an in-test reconstruction that calls NO production helper or label
//     constant: HKDF-SHA256 by hand from HMAC (RFC 5869, salt=nil ⇒ 32
//     zero bytes), X25519 via golang.org/x/crypto/curve25519 (not
//     crypto/ecdh), XChaCha20-Poly1305 by hand from HChaCha20 + IETF
//     ChaCha20-Poly1305, SHA-256 from crypto/sha256, with every info
//     string and AAD spelled as a literal.
//
// Regenerate the JSON (only after a deliberate, versioned label change):
//
//	KENAZ_REGEN_CREDENTIAL_VECTORS=1 go test ./core/fleet -run TestCredentialVectors

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	// cvNodeID predates the ULID rule and is NOT a canonical ULID (it
	// contains "O"). It is kept so the v1 identity and org-wrap goldens stay
	// byte-identical; DeriveCredentialPrivKey takes the literal node id
	// string (§5.1). The pairing vectors use the canonical ULIDs below.
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

	// Pairing / authwrap: N is the new device (its own fresh seed), W the
	// paired sender that already holds the UCK (seed = cvSeed()).
	cvNodeN  = "01JA2PZ8NEWDEV1CE000000000"
	cvNodeW  = "01JA2PZ8WRAPPER00000000000"
	cvKeyIDN = "4f1c2b3a-5d6e-4a7b-8c9d-0e1f2a3b4c5d"
	cvKeyIDW = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func cvSeed() []byte            { return seq32() }
func cvUCK() []byte             { return fill(0x33, 32) }
func cvEphSecretWrap() []byte   { return fill(0x11, 32) }
func cvEphAuthWrap() []byte     { return fill(0x13, 32) }
func cvNonceSecretWrap() []byte { return fill(0x22, 24) }
func cvNonceUCKSeed() []byte    { return fill(0x24, 24) }
func cvNoncePersonal() []byte   { return fill(0x25, 24) }
func cvPairNA() []byte          { return fill(0x26, 32) }
func cvPairNB() []byte          { return fill(0x27, 32) }
func cvNonceAuthWrap() []byte   { return fill(0x28, 24) }

// cvSeedN is the new device's seed: 0x40..0x5f.
func cvSeedN() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(0x40 + i)
	}
	return s
}

// cvSeedW is the sender's seed (the same seed as the v1 vectors).
func cvSeedW() []byte { return cvSeed() }

// ── golden outputs ──
const (
	// v1 — byte-identical to the first #417 vectors; must never change.
	wantCredentialPubHex  = "8d6319ae39dabbc300c0008fd007c447b9b0514d24e84da380fe10d6caeba449"
	wantWrappedSecretHex  = "738aa59e59a8369dfac1ea844e270bd1fadb0726569696164a49361d139beb777501d6d8c381b613f07b"
	wantUCKSeedWrapHex    = "0c6d2d2d4e5555d6ef2e9a41c274eaa00ac421ced019edf066cc2da5dcafda0e379bf7406c290e46b3a21ef79cb81a5c"
	wantPersonalCTHex     = "d668e29bd6bfb16385341488c4a2640d6fd128d9b97332f29df3be37cb65f5604f32d4f80b518326556cad0efdb679"
	wantSeedKeyCheckValue = "729f801123881585ffefedaf7ffd3c1c"

	// v2 additions. Generated from these constructions and cross-checked
	// by the manual reconstruction and vectors.json in the same test.
	wantPkNHex         = "d4d68ee679096c0ae8ca128fdebe44c4db919075e5cf7cd89860f67f518ebc36"
	wantPkWHex         = "2cc08a6dc89a5bb3b844e753a634f9fec667e4faf197dd26a170a580b4c24b78"
	wantPairCommitHex  = "cb41f34aa0ffd51fd8e427c165276c6fed10b05aaacfcf8d9435a957e07de7bd"
	wantPairingSAS     = "19606638"
	wantAuthWrapHex    = "6e781b5a8351cbe975892f3ec57b6104dedc5ed2e447836b3e3b0d915f7dc09815a4aeb3be70bcf30e29c894472717b2"
	wantUCKAuthHex     = "b35a80c7016fd9691dad18b5f9abfee6b5ac10532028307d9ff450d6266b02ba"
	wantUCKAuthHashHex = "0148c2b1913de3bc2d45497e6f3e76fa55e6fe82492f96119fb7ade64f62d813"
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
	SeedNHex                  string `json:"seed_n_hex"`
	SeedWHex                  string `json:"seed_w_hex"`
	NodeN                     string `json:"node_n"`
	NodeW                     string `json:"node_w"`
	KeyIDN                    string `json:"key_id_n"`
	KeyIDW                    string `json:"key_id_w"`
	PairNAHex                 string `json:"pair_na_hex"`
	PairNBHex                 string `json:"pair_nb_hex"`
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

type cvPairingIdentities struct {
	N cvIdentity `json:"n"`
	W cvIdentity `json:"w"`
}

type cvPairCommit struct {
	Label     string `json:"label"`
	PkNHex    string `json:"pk_n_hex"`
	NAHex     string `json:"na_hex"`
	InputHex  string `json:"sha256_input_hex"`
	CommitHex string `json:"commit_hex"`
}

type cvPairingSAS struct {
	Info          string `json:"info"`
	InfoHex       string `json:"info_hex"`
	IKMHex        string `json:"ikm_hex"`
	HKDFOutputHex string `json:"hkdf_output_hex"`
	Uint64        string `json:"big_endian_uint64"`
	SASDigits     string `json:"sas_digits"`
}

type cvAuthWrap struct {
	Label              string `json:"label"`
	InfoHex            string `json:"info_hex"`
	EphemeralScalarHex string `json:"ephemeral_scalar_hex"`
	EphemeralPublicHex string `json:"ephemeral_public_key_hex"`
	RecipientPublicHex string `json:"recipient_public_key_hex"`
	SenderPublicHex    string `json:"sender_public_key_hex"`
	DH1Hex             string `json:"x25519_eph_n_hex"`
	DH2Hex             string `json:"x25519_w_n_hex"`
	IKMHex             string `json:"ikm_hex"`
	KEKHex             string `json:"kek_hex"`
	NonceHex           string `json:"nonce_hex"`
	AAD                string `json:"aad"`
	AADHex             string `json:"aad_hex"`
	PayloadHex         string `json:"payload_hex"`
	SealedHex          string `json:"sealed_hex"`
}

type cvUCKAuth struct {
	Info        string `json:"info"`
	UCKHex      string `json:"uck_hex"`
	UCKAuthHex  string `json:"uck_auth_hex"`
	UCKAuthHash string `json:"uck_auth_hash_hex"`
}

type cvFile struct {
	Schema         string              `json:"schema"`
	Contract       string              `json:"contract"`
	Inputs         cvInputs            `json:"inputs"`
	Identity       cvIdentity          `json:"credential_identity"`
	WrappedSecret  cvDeviceWrap        `json:"wrapped_secret"`
	UCKSeedWrap    cvSymSeal           `json:"uck_seed_wrap"`
	PersonalSecret cvSymSeal           `json:"personal_ciphertext"`
	SeedKeyCheck   cvSeedKeyCheck      `json:"seed_key_check"`
	PairingIDs     cvPairingIdentities `json:"pairing_identities"`
	PairCommit     cvPairCommit        `json:"pairing_commit"`
	PairingSAS     cvPairingSAS        `json:"pairing_sas"`
	UCKAuthWrap    cvAuthWrap          `json:"uck_auth_wrap"`
	UCKAuth        cvUCKAuth           `json:"uck_auth"`
}

func hx(b []byte) string { return hex.EncodeToString(b) }

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func cvInputsBlock() cvInputs {
	return cvInputs{
		SeedHex: hx(cvSeed()), NodeID: cvNodeID, UserID: cvUserID, KeyID: cvKeyID,
		OrgCredentialID: cvOrgCredentialID, OrgCredentialVersion: cvOrgVersion,
		PersonalCredentialID: cvPersonalCredID, PersonalCredentialVersion: cvPersonalVersion,
		UCKVersion: cvUCKVersion, UCKHex: hx(cvUCK()),
		OrgSecretUTF8: cvOrgSecretText, OrgSecretHex: hx([]byte(cvOrgSecretText)),
		PersonalSecretUTF8: cvPersonalSecretText, PersonalSecretHex: hx([]byte(cvPersonalSecretText)),
		SeedNHex: hx(cvSeedN()), SeedWHex: hx(cvSeedW()), NodeN: cvNodeN, NodeW: cvNodeW,
		KeyIDN: cvKeyIDN, KeyIDW: cvKeyIDW, PairNAHex: hx(cvPairNA()), PairNBHex: hx(cvPairNB()),
	}
}

func cvHeader(f *cvFile) {
	f.Schema = "kenaz-credential-vectors/v2"
	f.Contract = "kenaz-fleet PR #268 docs/contract-harness-credentials.md §5 §6.1 §6.2 (+ kitty-specs/provider-credentials-01NPCRED01/contract-draft.md §3 §4 §7); kenaz-harness provider-credentials-client-01PCCLI01 spec §8 §10.1 §10.3"
	f.Inputs = cvInputsBlock()
}

func mustCredKey(t *testing.T, seed []byte, nodeID string) *ecdh.PrivateKey {
	t.Helper()
	k, err := DeriveCredentialPrivKey(seed, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// productionVectors builds every vector with the PRODUCTION helpers.
func productionVectors(t *testing.T) cvFile {
	t.Helper()
	seed := cvSeed()
	var f cvFile
	cvHeader(&f)

	identity := func(seed []byte, nodeID string) (cvIdentity, *ecdh.PrivateKey) {
		info := credentialIdentityV1Info + nodeID
		raw, err := hkdf32(seed, info)
		if err != nil {
			t.Fatal(err)
		}
		priv := mustCredKey(t, seed, nodeID)
		return cvIdentity{Info: info, InfoHex: hx([]byte(info)), HKDFOutputHex: hx(raw),
			ClampedScalarHex: hx(priv.Bytes()), PublicKeyHex: hx(priv.PublicKey().Bytes())}, priv
	}

	var priv *ecdh.PrivateKey
	f.Identity, priv = identity(seed, cvNodeID)
	pub := priv.PublicKey().Bytes()

	// §3 anonymous org/team wrap.
	eph, err := ecdh.X25519().NewPrivateKey(cvEphSecretWrap())
	if err != nil {
		t.Fatal(err)
	}
	wAAD := credentialWrapAAD(cvOrgCredentialID, cvOrgVersion, cvKeyID)
	sealed, err := sealToCredentialKeyWith(eph, pub, cvNonceSecretWrap(), []byte(cvOrgSecretText), wAAD)
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
	f.WrappedSecret = cvDeviceWrap{Info: string(LabelCredentialWrapV1), EphemeralScalarHex: hx(cvEphSecretWrap()),
		EphemeralPublicHex: hx(eph.PublicKey().Bytes()), RecipientPublicHex: hx(pub),
		SharedHex: hx(shared), KEKHex: hx(kek), NonceHex: hx(cvNonceSecretWrap()), AAD: string(wAAD), AADHex: hx(wAAD),
		PayloadHex: hx([]byte(cvOrgSecretText)), SealedHex: hx(sealed)}

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

	// ── v2: pairing identities ──
	var privN, privW *ecdh.PrivateKey
	f.PairingIDs.N, privN = identity(cvSeedN(), cvNodeN)
	f.PairingIDs.W, privW = identity(cvSeedW(), cvNodeW)
	pkN, pkW := privN.PublicKey().Bytes(), privW.PublicKey().Bytes()

	// pairing commit
	commit, err := PairingCommit(pkN, cvPairNA())
	if err != nil {
		t.Fatal(err)
	}
	f.PairCommit = cvPairCommit{Label: string(LabelPairCommitV1), PkNHex: hx(pkN), NAHex: hx(cvPairNA()),
		InputHex: hx(cat([]byte(LabelPairCommitV1), pkN, cvPairNA())), CommitHex: hx(commit)}

	// pairing SAS
	sas, err := PairingSAS(cvUserID, cvNodeN, cvNodeW, pkN, pkW, cvPairNA(), cvPairNB())
	if err != nil {
		t.Fatal(err)
	}
	sasInfo := string(LabelCredentialKeySASV2) + cvUserID + ":" + cvNodeN + ":" + cvNodeW
	sasIKM := cat(pkN, pkW, cvPairNA(), cvPairNB())
	sasRaw, err := hkdf32(sasIKM, sasInfo) // L=8 is the first 8 bytes of T(1)
	if err != nil {
		t.Fatal(err)
	}
	f.PairingSAS = cvPairingSAS{Info: sasInfo, InfoHex: hx([]byte(sasInfo)), IKMHex: hx(sasIKM),
		HKDFOutputHex: hx(sasRaw[:8]), Uint64: strconv.FormatUint(beUint64(sasRaw[:8]), 10), SASDigits: sas}

	// authenticated UCK device wrap (W → N)
	ephA, err := ecdh.X25519().NewPrivateKey(cvEphAuthWrap())
	if err != nil {
		t.Fatal(err)
	}
	aw, err := sealUCKAuthWrapWith(ephA, privW, pkN, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, cvUCK(), cvNonceAuthWrap())
	if err != nil {
		t.Fatal(err)
	}
	dh1, err := ephA.ECDH(privN.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	dh2, err := privW.ECDH(privN.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	ephPubA := ephA.PublicKey().Bytes()
	awKEK, err := uckAuthWrapKEK(dh1, dh2, ephPubA, pkN, pkW)
	if err != nil {
		t.Fatal(err)
	}
	awAAD, err := uckAuthWrapAAD(cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW)
	if err != nil {
		t.Fatal(err)
	}
	f.UCKAuthWrap = cvAuthWrap{Label: string(LabelCredentialAuthWrapV1),
		InfoHex:            hx(cat([]byte(LabelCredentialAuthWrapV1), ephPubA, pkN, pkW)),
		EphemeralScalarHex: hx(cvEphAuthWrap()), EphemeralPublicHex: hx(ephPubA),
		RecipientPublicHex: hx(pkN), SenderPublicHex: hx(pkW), DH1Hex: hx(dh1), DH2Hex: hx(dh2),
		IKMHex: hx(cat(dh1, dh2)), KEKHex: hx(awKEK), NonceHex: hx(cvNonceAuthWrap()),
		AAD: string(awAAD), AADHex: hx(awAAD), PayloadHex: hx(cvUCK()), SealedHex: hx(aw)}

	// uck_auth
	ua, err := UCKAuth(cvUCK())
	if err != nil {
		t.Fatal(err)
	}
	uah, err := UCKAuthHash(cvUCK())
	if err != nil {
		t.Fatal(err)
	}
	f.UCKAuth = cvUCKAuth{Info: string(LabelUCKAuthV1), UCKHex: hx(cvUCK()), UCKAuthHex: hx(ua), UCKAuthHash: hx(uah)}
	return f
}

// beUint64 is a hand-rolled big-endian decode (no encoding/binary, so the
// reconstruction does not share the production decode).
func beUint64(b []byte) uint64 {
	var v uint64
	for _, c := range b[:8] {
		v = v<<8 | uint64(c)
	}
	return v
}

// manualVectors rebuilds every vector WITHOUT production helpers or label
// constants: literals + manualHKDF32 + curve25519 + manualXSeal + sha256.
func manualVectors(t *testing.T) cvFile {
	t.Helper()
	seed := cvSeed()
	var f cvFile
	cvHeader(&f)

	identity := func(seed []byte, nodeID string) (cvIdentity, []byte, []byte) {
		info := "kenaz-credential-identity-v1:" + nodeID
		raw := manualHKDF32(seed, info)
		clamped := append([]byte(nil), raw...)
		clamped[0] &= 248
		clamped[31] &= 127
		clamped[31] |= 64
		pub, err := curve25519.X25519(clamped, curve25519.Basepoint)
		if err != nil {
			t.Fatal(err)
		}
		return cvIdentity{Info: info, InfoHex: hx([]byte(info)), HKDFOutputHex: hx(raw),
			ClampedScalarHex: hx(clamped), PublicKeyHex: hx(pub)}, clamped, pub
	}
	x := func(k, p []byte) []byte {
		out, err := curve25519.X25519(k, p)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	var clamped, pub []byte
	f.Identity, clamped, pub = identity(seed, cvNodeID)

	d := func(v uint64) string { return strconv.FormatUint(v, 10) }

	// §3 anonymous org/team wrap.
	ephPub := x(cvEphSecretWrap(), curve25519.Basepoint)
	shared := x(cvEphSecretWrap(), pub)
	if !bytes.Equal(shared, x(clamped, ephPub)) {
		t.Fatal("manual ECDH asymmetry")
	}
	kek := manualHKDF32(shared, "kenaz-credential-v1-wrap")
	wAAD := cvOrgCredentialID + ":" + d(cvOrgVersion) + ":" + cvKeyID
	f.WrappedSecret = cvDeviceWrap{Info: "kenaz-credential-v1-wrap", EphemeralScalarHex: hx(cvEphSecretWrap()),
		EphemeralPublicHex: hx(ephPub), RecipientPublicHex: hx(pub), SharedHex: hx(shared), KEKHex: hx(kek),
		NonceHex: hx(cvNonceSecretWrap()), AAD: wAAD, AADHex: hx([]byte(wAAD)), PayloadHex: hx([]byte(cvOrgSecretText)),
		SealedHex: hx(manualXSeal(t, kek, cvNonceSecretWrap(), []byte(cvOrgSecretText), []byte(wAAD)))}

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

	// ── v2 ──
	var skN, pkN, skW, pkW []byte
	f.PairingIDs.N, skN, pkN = identity(cvSeedN(), cvNodeN)
	f.PairingIDs.W, skW, pkW = identity(cvSeedW(), cvNodeW)

	commitIn := cat([]byte("kenaz-pair-commit-v1"), pkN, cvPairNA())
	commit := sha256.Sum256(commitIn)
	f.PairCommit = cvPairCommit{Label: "kenaz-pair-commit-v1", PkNHex: hx(pkN), NAHex: hx(cvPairNA()),
		InputHex: hx(commitIn), CommitHex: hx(commit[:])}

	sasInfo := "kenaz-credential-key-sas-v2:" + cvUserID + ":" + cvNodeN + ":" + cvNodeW
	sasIKM := cat(pkN, pkW, cvPairNA(), cvPairNB())
	b8 := manualHKDF32(sasIKM, sasInfo)[:8] // HKDF L=8 = first 8 bytes of T(1)
	u := beUint64(b8)
	f.PairingSAS = cvPairingSAS{Info: sasInfo, InfoHex: hx([]byte(sasInfo)), IKMHex: hx(sasIKM),
		HKDFOutputHex: hx(b8), Uint64: d(u), SASDigits: fmt.Sprintf("%08d", u%100000000)}

	ephPubA := x(cvEphAuthWrap(), curve25519.Basepoint)
	dh1 := x(cvEphAuthWrap(), pkN)
	dh2 := x(skW, pkN)
	// Receiver side must agree: X25519(sk_N, eph_pub) ‖ X25519(sk_N, pk_W).
	if !bytes.Equal(dh1, x(skN, ephPubA)) || !bytes.Equal(dh2, x(skN, pkW)) {
		t.Fatal("manual authwrap ECDH asymmetry")
	}
	awInfo := cat([]byte("kenaz-credential-v1-authwrap"), ephPubA, pkN, pkW)
	awIKM := cat(dh1, dh2)
	awKEK := manualHKDF32(awIKM, string(awInfo))
	awAAD := "uck:" + cvUserID + ":" + d(cvUCKVersion) + ":" + cvKeyIDN + ":" + cvKeyIDW
	f.UCKAuthWrap = cvAuthWrap{Label: "kenaz-credential-v1-authwrap", InfoHex: hx(awInfo),
		EphemeralScalarHex: hx(cvEphAuthWrap()), EphemeralPublicHex: hx(ephPubA),
		RecipientPublicHex: hx(pkN), SenderPublicHex: hx(pkW), DH1Hex: hx(dh1), DH2Hex: hx(dh2),
		IKMHex: hx(awIKM), KEKHex: hx(awKEK), NonceHex: hx(cvNonceAuthWrap()),
		AAD: awAAD, AADHex: hx([]byte(awAAD)), PayloadHex: hx(cvUCK()),
		SealedHex: hx(manualXSeal(t, awKEK, cvNonceAuthWrap(), cvUCK(), []byte(awAAD)))}

	ua := manualHKDF32(cvUCK(), "kenaz-uck-auth-v1")
	uah := sha256.Sum256(ua)
	f.UCKAuth = cvUCKAuth{Info: "kenaz-uck-auth-v1", UCKHex: hx(cvUCK()), UCKAuthHex: hx(ua), UCKAuthHash: hx(uah[:])}
	return f
}

func cvMustHex(t *testing.T, s string) []byte {
	t.Helper()
	v, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCredentialVectors(t *testing.T) {
	prod := productionVectors(t)
	manual := manualVectors(t)

	// (3) independent reconstruction agrees with production, field by field.
	if !reflect.DeepEqual(prod, manual) {
		pj, _ := json.MarshalIndent(prod, "", "  ")
		mj, _ := json.MarshalIndent(manual, "", "  ")
		t.Fatalf("production disagrees with manual reconstruction\nproduction:\n%s\nmanual:\n%s", pj, mj)
	}

	// Literal AADs / infos, so a separator or encoding drift is legible.
	for _, c := range []struct{ name, got, want string }{
		{"wrapped_secret aad", prod.WrappedSecret.AAD, "aaaaaaaa-0000-4000-8000-000000000001:3:7c9e6679-7425-40de-944b-e07fc1f90ae7"},
		{"uck_seed_wrap aad", prod.UCKSeedWrap.AAD, "11111111-2222-4333-8444-555555555555:uck:5"},
		{"personal aad", prod.PersonalSecret.AAD, "11111111-2222-4333-8444-555555555555:bbbbbbbb-0000-4000-8000-000000000002:2"},
		{"uck_auth_wrap aad", prod.UCKAuthWrap.AAD, "uck:11111111-2222-4333-8444-555555555555:5:4f1c2b3a-5d6e-4a7b-8c9d-0e1f2a3b4c5d:9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"},
		{"pairing_sas info", prod.PairingSAS.Info, "kenaz-credential-key-sas-v2:11111111-2222-4333-8444-555555555555:01JA2PZ8NEWDEV1CE000000000:01JA2PZ8WRAPPER00000000000"},
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
		{"personal ciphertext", prod.PersonalSecret.SealedHex, wantPersonalCTHex},
		{"seed_key_check", prod.SeedKeyCheck.Value, wantSeedKeyCheckValue},
		{"pk_N", prod.PairingIDs.N.PublicKeyHex, wantPkNHex},
		{"pk_W", prod.PairingIDs.W.PublicKeyHex, wantPkWHex},
		{"pairing commit", prod.PairCommit.CommitHex, wantPairCommitHex},
		{"pairing sas", prod.PairingSAS.SASDigits, wantPairingSAS},
		{"wrapped UCK (authenticated device path)", prod.UCKAuthWrap.SealedHex, wantAuthWrapHex},
		{"uck_auth", prod.UCKAuth.UCKAuthHex, wantUCKAuthHex},
		{"uck_auth_hash", prod.UCKAuth.UCKAuthHash, wantUCKAuthHashHex},
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
	if n := len(prod.UCKAuthWrap.SealedHex) / 2; n != 48 {
		t.Errorf("uck_auth_wrap sealed = %d bytes, want 48", n)
	}
	if n := len(prod.SeedKeyCheck.Value); n != 32 {
		t.Errorf("seed_key_check = %d hex chars, want 32", n)
	}
	if n := len(prod.PairingSAS.SASDigits); n != 8 {
		t.Errorf("sas = %d digits, want 8", n)
	}

	// Open every golden ciphertext with the production open paths.
	priv := mustCredKey(t, cvSeed(), cvNodeID)
	ws := prod.WrappedSecret
	if got, err := openWrappedSecret(priv, cvOrgCredentialID, cvOrgVersion, cvKeyID,
		cvMustHex(t, ws.EphemeralPublicHex), cvMustHex(t, ws.NonceHex), cvMustHex(t, ws.SealedHex)); err != nil || string(got) != cvOrgSecretText {
		t.Fatalf("open wrapped secret: %v", err)
	}
	privN := mustCredKey(t, cvSeedN(), cvNodeN)
	aw := prod.UCKAuthWrap
	if got, err := openUCKAuthWrap(privN, cvMustHex(t, aw.SenderPublicHex), cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW,
		cvMustHex(t, aw.EphemeralPublicHex), cvMustHex(t, aw.NonceHex), cvMustHex(t, aw.SealedHex)); err != nil || !bytes.Equal(got, cvUCK()) {
		t.Fatalf("open uck auth wrap: %v", err)
	}
	if !pairingCommitMatches(cvMustHex(t, prod.PairCommit.CommitHex), privN.PublicKey().Bytes(), cvPairNA()) {
		t.Fatal("golden pairing commit does not verify")
	}
	if got, err := openUCKSeedWrap(cvSeed(), cvUserID, cvUCKVersion, cvNonceUCKSeed(), cvMustHex(t, prod.UCKSeedWrap.SealedHex)); err != nil || !bytes.Equal(got, cvUCK()) {
		t.Fatalf("open uck seed wrap: %v", err)
	}
	if got, err := openPersonalSecret(cvUCK(), cvUserID, cvPersonalCredID, cvPersonalVersion, cvNoncePersonal(), cvMustHex(t, prod.PersonalSecret.SealedHex)); err != nil || string(got) != cvPersonalSecretText {
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
// failures for every v1 construction.
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

// The authenticated UCK device wrap: round trip, sender authentication,
// AAD binding, tamper, and the rejected cross-construction / self cases.
func TestCredentialAuthWrap_RoundTripAndFailures(t *testing.T) {
	privN := mustCredKey(t, cvSeedN(), cvNodeN)
	privW := mustCredKey(t, cvSeedW(), cvNodeW)
	mallory := mustCredKey(t, fill(0x66, 32), cvNodeW)
	pkN, pkW := privN.PublicKey().Bytes(), privW.PublicKey().Bytes()
	uck := make([]byte, 32)
	if _, err := rand.Read(uck); err != nil {
		t.Fatal(err)
	}

	aw, err := sealUCKAuthWrap(rand.Reader, privW, pkN, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, uck)
	if err != nil {
		t.Fatal(err)
	}
	open := func(recipient *ecdh.PrivateKey, expPkW []byte, user string, ver uint64, kN, kW string, ephPub, nonce, sealed []byte) ([]byte, error) {
		return openUCKAuthWrap(recipient, expPkW, user, ver, kN, kW, ephPub, nonce, sealed)
	}
	if got, err := open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed); err != nil || !bytes.Equal(got, uck) {
		t.Fatalf("authwrap round trip: %v", err)
	}

	// Wrong pk_W: N expects a different sender than the one who sealed.
	g, e := open(privN, mallory.PublicKey().Bytes(), cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong expected pk_W", g, e)
	// A forger without sk_W (Fleet) wraps its own UCK to N; N, expecting W, rejects.
	forged, err := sealUCKAuthWrap(rand.Reader, mallory, pkN, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, fill(0x99, 32))
	if err != nil {
		t.Fatal(err)
	}
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, forged.EphemeralPublicKey, forged.Nonce, forged.Sealed)
	wantOpenFailed(t, "authwrap: forged by non-paired sender", g, e)

	// Cross-construction: the authwrap must not open via the anonymous §3
	// path (same recipient key, same AAD bytes) …
	awAAD, err := uckAuthWrapAAD(cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW)
	if err != nil {
		t.Fatal(err)
	}
	g, e = openFromCredentialKey(privN, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed, awAAD)
	wantOpenFailed(t, "authwrap opened with the anonymous construction", g, e)
	// … and an anonymous wrap of a UCK (what a malicious Fleet can make
	// from pk_N alone) must not open as an authwrap.
	anon, err := sealToCredentialKey(rand.Reader, pkN, cvKeyIDN, fill(0x99, 32), awAAD)
	if err != nil {
		t.Fatal(err)
	}
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, anon.EphemeralPublicKey, anon.Nonce, anon.Sealed)
	wantOpenFailed(t, "anonymous UCK wrap opened as authwrap", g, e)

	// AAD binding.
	g, e = open(privN, pkW, cvUserID, cvUCKVersion+1, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong uck_version", g, e)
	g, e = open(privN, pkW, "99999999-2222-4333-8444-555555555555", cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong user_id", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyID, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong key_id_N", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyID, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong key_id_W", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDW, cvKeyIDN, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: key ids swapped", g, e)

	// Wrong recipient, tampering.
	g, e = open(mustCredKey(t, cvSeedN(), "01JA2PZ8NEWDEV1CE000000001"), pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: wrong recipient", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, flip(aw.Sealed, 47))
	wantOpenFailed(t, "authwrap: tampered tag", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, flip(aw.Nonce, 2), aw.Sealed)
	wantOpenFailed(t, "authwrap: tampered nonce", g, e)
	g, e = open(privN, pkW, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, flip(aw.EphemeralPublicKey, 5), aw.Nonce, aw.Sealed)
	wantOpenFailed(t, "authwrap: tampered ephemeral key", g, e)

	// pk_W == pk_N is refused on both sides.
	if _, err := sealUCKAuthWrap(rand.Reader, privN, pkN, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, uck); !errors.Is(err, ErrCredentialSelfPairing) {
		t.Errorf("seal with pk_W == pk_N: err = %v, want ErrCredentialSelfPairing", err)
	}
	if _, err := open(privN, pkN, cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed); !errors.Is(err, ErrCredentialSelfPairing) {
		t.Errorf("open with pk_W == pk_N: err = %v, want ErrCredentialSelfPairing", err)
	}

	// Non-canonical ids / zero version are refused before any crypto.
	for _, c := range []struct {
		name, user, kN, kW string
		ver                uint64
	}{
		{"uppercase user_id", "11111111-2222-4333-8444-55555555555A", cvKeyIDN, cvKeyIDW, cvUCKVersion},
		{"user_id with colon", "uck:1111-2222-4333-8444-555555555555", cvKeyIDN, cvKeyIDW, cvUCKVersion},
		{"non-uuid key_id_N", cvUserID, "other-key-id", cvKeyIDW, cvUCKVersion},
		{"braced key_id_W", cvUserID, cvKeyIDN, "{" + cvKeyIDW[1:35] + "}", cvUCKVersion},
		{"zero uck_version", cvUserID, cvKeyIDN, cvKeyIDW, 0},
	} {
		if _, err := sealUCKAuthWrap(rand.Reader, privW, pkN, c.user, c.ver, c.kN, c.kW, uck); !errors.Is(err, ErrCredentialIDInvalid) {
			t.Errorf("seal %s: err = %v, want ErrCredentialIDInvalid", c.name, err)
		}
		if _, err := open(privN, pkW, c.user, c.ver, c.kN, c.kW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed); !errors.Is(err, ErrCredentialIDInvalid) {
			t.Errorf("open %s: err = %v, want ErrCredentialIDInvalid", c.name, err)
		}
	}
}

func TestCredentialPairing_CommitAndSAS(t *testing.T) {
	privN := mustCredKey(t, cvSeedN(), cvNodeN)
	privW := mustCredKey(t, cvSeedW(), cvNodeW)
	pkN, pkW := privN.PublicKey().Bytes(), privW.PublicKey().Bytes()
	nA, nB := cvPairNA(), cvPairNB()

	commit, err := PairingCommit(pkN, nA)
	if err != nil {
		t.Fatal(err)
	}
	if !pairingCommitMatches(commit, pkN, nA) {
		t.Fatal("commit must verify with the committed pk_N and nA")
	}
	if pairingCommitMatches(commit, pkN, flip(nA, 0)) {
		t.Error("commit verified with a wrong nA")
	}
	if pairingCommitMatches(commit, pkW, nA) {
		t.Error("commit verified with a wrong pk_N")
	}
	if pairingCommitMatches(commit, pkN, nA[:31]) {
		t.Error("commit verified with a short nA")
	}
	if pairingCommitMatches(commit[:31], pkN, nA) {
		t.Error("truncated commit verified")
	}
	if _, err := PairingCommit(pkN, nA[:16]); err == nil {
		t.Error("PairingCommit accepted a 16-byte nA")
	}

	sas, err := PairingSAS(cvUserID, cvNodeN, cvNodeW, pkN, pkW, nA, nB)
	if err != nil {
		t.Fatal(err)
	}
	// Every input is bound: changing any one changes the code.
	for _, c := range []struct {
		name               string
		user, nodeN, nodeW string
		pkN, pkW, nA, nB   []byte
	}{
		{"nB", cvUserID, cvNodeN, cvNodeW, pkN, pkW, nA, flip(nB, 0)},
		{"nA", cvUserID, cvNodeN, cvNodeW, pkN, pkW, flip(nA, 0), nB},
		{"pk_W", cvUserID, cvNodeN, cvNodeW, pkN, mustCredKey(t, fill(0x66, 32), cvNodeW).PublicKey().Bytes(), nA, nB},
		{"node order", cvUserID, cvNodeW, cvNodeN, pkN, pkW, nA, nB},
		{"user_id", "99999999-2222-4333-8444-555555555555", cvNodeN, cvNodeW, pkN, pkW, nA, nB},
	} {
		got, err := PairingSAS(c.user, c.nodeN, c.nodeW, c.pkN, c.pkW, c.nA, c.nB)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got == sas {
			t.Errorf("changing %s did not change the SAS", c.name)
		}
	}

	// Bad ULIDs are rejected.
	for _, bad := range []string{
		cvNodeID,                      // contains "O" (the v1 vector id)
		"01ja2pz8newdev1ce000000000",  // lower case
		"01JA2PZ8NEWDEV1CE00000000",   // 25 chars
		"01JA2PZ8NEWDEV1CE0000000000", // 27 chars
		"01JA2PZ8NEWDEV1CE00000000I",  // I
		"01JA2PZ8NEWDEV1CE00000000L",  // L
		"01JA2PZ8NEWDEV1CE00000000U",  // U
		"01JA2PZ8NEWDEV1CE0000000:0",  // separator
		"",
	} {
		if IsCanonicalNodeULID(bad) {
			t.Errorf("IsCanonicalNodeULID(%q) = true", bad)
		}
		if _, err := PairingSAS(cvUserID, bad, cvNodeW, pkN, pkW, nA, nB); !errors.Is(err, ErrCredentialIDInvalid) {
			t.Errorf("node_N %q: err = %v, want ErrCredentialIDInvalid", bad, err)
		}
		if _, err := PairingSAS(cvUserID, cvNodeN, bad, pkN, pkW, nA, nB); !errors.Is(err, ErrCredentialIDInvalid) {
			t.Errorf("node_W %q: err = %v, want ErrCredentialIDInvalid", bad, err)
		}
	}
	if !IsCanonicalNodeULID(cvNodeN) || !IsCanonicalNodeULID(cvNodeW) {
		t.Fatal("vector node ids must be canonical ULIDs")
	}
	if _, err := PairingSAS("11111111-2222-4333-8444-55555555555A", cvNodeN, cvNodeW, pkN, pkW, nA, nB); !errors.Is(err, ErrCredentialIDInvalid) {
		t.Errorf("uppercase user_id: err = %v, want ErrCredentialIDInvalid", err)
	}

	// Self-pairing is refused.
	if _, err := PairingSAS(cvUserID, cvNodeN, cvNodeW, pkN, pkN, nA, nB); !errors.Is(err, ErrCredentialSelfPairing) {
		t.Errorf("pk_W == pk_N: err = %v, want ErrCredentialSelfPairing", err)
	}
	if _, err := PairingSAS(cvUserID, cvNodeN, cvNodeN, pkN, pkW, nA, nB); !errors.Is(err, ErrCredentialSelfPairing) {
		t.Errorf("node_W == node_N: err = %v, want ErrCredentialSelfPairing", err)
	}
	// Nonce lengths are exactly 32.
	if _, err := PairingSAS(cvUserID, cvNodeN, cvNodeW, pkN, pkW, nA, nB[:31]); err == nil {
		t.Error("31-byte nB accepted")
	}
}

func TestCredentialUCKAuth(t *testing.T) {
	a1, err := UCKAuth(cvUCK())
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := UCKAuth(fill(0x44, 32))
	if bytes.Equal(a1, a2) {
		t.Fatal("uck_auth must differ per UCK")
	}
	if bytes.Equal(a1, cvUCK()) {
		t.Fatal("uck_auth must not be the UCK")
	}
	h, _ := UCKAuthHash(cvUCK())
	want := sha256.Sum256(a1)
	if !bytes.Equal(h, want[:]) {
		t.Fatal("uck_auth_hash must be SHA-256(uck_auth)")
	}
	if _, err := UCKAuth(make([]byte, 16)); err == nil {
		t.Fatal("short UCK must be refused")
	}
}

// H3 interop: the seed path and the authenticated device path carry the
// SAME UCK, so a personal secret written via one is readable via the other.
func TestCredentialV2_PathsInteroperate(t *testing.T) {
	seedW := cvSeedW()
	privW := mustCredKey(t, seedW, cvNodeW)
	privN := mustCredKey(t, cvSeedN(), cvNodeN)
	uck := cvUCK()
	sw, sn, err := sealUCKWithSeed(rand.Reader, seedW, cvUserID, cvUCKVersion, uck)
	if err != nil {
		t.Fatal(err)
	}
	viaSeed, err := openUCKSeedWrap(seedW, cvUserID, cvUCKVersion, sn, sw)
	if err != nil {
		t.Fatal(err)
	}
	aw, err := sealUCKAuthWrap(rand.Reader, privW, privN.PublicKey().Bytes(), cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, viaSeed)
	if err != nil {
		t.Fatal(err)
	}
	viaDevice, err := openUCKAuthWrap(privN, privW.PublicKey().Bytes(), cvUserID, cvUCKVersion, cvKeyIDN, cvKeyIDW, aw.EphemeralPublicKey, aw.Nonce, aw.Sealed)
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
