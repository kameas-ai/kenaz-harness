// Package fleet — bundle_v2.go
//
// Envelope v2: config bundles verified over the exact bytes Fleet signed,
// then decoded leniently (signed-bundle-bytes-01SBBYT01, spec §3/§4).
//
// The legacy path (bundle.go: VerifyWithKeySet) verifies over the harness's
// OWN re-encoding of the decoded Bundle, so any field Fleet signs that this
// build does not know is dropped by the decode, the re-marshal differs, and
// the bundle hard-rejects. v2 inverts the order: verify the received bytes P
// first, decode them second, and let unknown fields fall away harmlessly.
//
//	{"format":"kenaz-bundle-envelope-v2","key_id":"…","payload_b64":"…","signature":"…"}
//
//	signature = Ed25519(priv, "kenaz-config-bundle-v2\x00" || P)   (Fleet ruling 1)
//
// The prefix is domain separation: a v1 signature (Ed25519 over SHA-256 of
// the re-marshal) by the same key can never verify as v2, nor the reverse.
// The envelope key_id is an unsigned routing hint; the authoritative key_id
// is the one inside P, and the two must match (Fleet ruling 7).
package fleet

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// BundleEnvelopeFormatV2 is the "format" value of a v2 envelope.
const BundleEnvelopeFormatV2 = "kenaz-bundle-envelope-v2"

// BundleAcceptV2 is the Accept header value that asks Fleet for the v2
// envelope (Fleet ruling 2). Without it Fleet serves the legacy form.
const BundleAcceptV2 = "application/vnd.kenaz.config-bundle.v2+json"

// bundleV2SigningPrefix is the domain-separation prefix of the v2 signing
// input (Fleet ruling 1).
const bundleV2SigningPrefix = "kenaz-config-bundle-v2\x00"

// maxReportedUnknownFields bounds UnknownFields so a hostile-but-signed
// payload cannot balloon the status the UI renders.
const maxReportedUnknownFields = 64

// BundleEnvelope is the v2 wire envelope. Only Format, KeyID, PayloadB64 and
// Signature are read; an envelope never needs re-encoding.
type BundleEnvelope struct {
	Format     string `json:"format"`
	KeyID      string `json:"key_id"`
	PayloadB64 string `json:"payload_b64"`
	Signature  string `json:"signature"`
}

// VerifiedBundle is the result of a successful v2 verification: the decoded
// bundle, the exact signed bytes it came from, and what this build could not
// interpret in them (WP03).
type VerifiedBundle struct {
	Bundle *Bundle
	// Payload is P, the exact signed bytes. Checksum() is computed over it.
	Payload []byte
	// UnknownFields are the NAMES (never values) of keys in P this build did
	// not recognise: top-level ("new_section") and inside known sections
	// ("model_prefs.new_knob", "mandated_items[].new_key",
	// "tool_exposure.servers.*.new_key"). Sorted, de-duplicated, bounded.
	UnknownFields []string
	// MinClientVersions is P's min_client_versions map (Fleet ruling 6):
	// restriction-bearing field or section name → minimum harness semver
	// that can enforce it. nil when absent.
	MinClientVersions map[string]string
}

// Checksum is the v2 304 checksum: hex(SHA-256(P)) (Fleet ruling 3).
func (v *VerifiedBundle) Checksum() string { return hexChecksumOf(v.Payload) }

// envelopeFormat reports the "format" field of a config response body, or
// "" when it has none (an older Fleet answering with the legacy bundle).
// An error means the body is not a JSON object at all.
func envelopeFormat(body []byte) (string, error) {
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", err
	}
	return probe.Format, nil
}

// VerifyEnvelopeBytes parses a v2 envelope and verifies it (see
// VerifyEnvelope). It does NOT apply the monotonic bundle_id guard: callers
// (the poller, and the cache restore on Start) decide what lastAppliedID is.
func VerifyEnvelopeBytes(raw []byte, keys []ed25519.PublicKey) (*VerifiedBundle, error) {
	var env BundleEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: parse envelope: %v", ErrInvalidSignature, err)
	}
	return VerifyEnvelope(&env, keys)
}

// VerifyEnvelope verifies a v2 envelope, then decodes it. Order matters and
// is the point of v2 (spec §4.2/§4.3):
//
//  1. Format must be BundleEnvelopeFormatV2.
//  2. base64-decode payload_b64 into P (failure → ErrInvalidSignature).
//  3. Select candidate keys by the envelope key_id with VerifyWithKeySet's
//     rules (empty set → ErrSigningKeyNotConfigured; unmatched key_id →
//     ErrSigningKeyUnknown).
//  4. ed25519.Verify(key, prefix || P, sig); none verifies →
//     ErrInvalidSignature.
//  5. Only now: lenient json.Unmarshal(P) (unknown fields ignored).
//  6. P.key_id must equal the envelope key_id → else ErrBundleKeyIDMismatch.
//
// Every failure is a hard reject; the caller keeps the last applied bundle.
func VerifyEnvelope(env *BundleEnvelope, keys []ed25519.PublicKey) (*VerifiedBundle, error) {
	if env == nil || env.Format != BundleEnvelopeFormatV2 {
		got := ""
		if env != nil {
			got = env.Format
		}
		return nil, fmt.Errorf("%w: envelope format %q is not %q", ErrInvalidSignature, got, BundleEnvelopeFormatV2)
	}
	payload, err := base64.StdEncoding.DecodeString(env.PayloadB64)
	if err != nil {
		// Tolerate unpadded standard base64 too; anything else is a reject.
		payload, err = base64.RawStdEncoding.DecodeString(env.PayloadB64)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot decode payload_b64: %v", ErrInvalidSignature, err)
		}
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrInvalidSignature)
	}
	candidates, err := selectSigningKeys(env.KeyID, keys)
	if err != nil {
		return nil, err
	}
	sig, err := decodeBundleSignature(env.Signature)
	if err != nil {
		return nil, err
	}
	msg := make([]byte, 0, len(bundleV2SigningPrefix)+len(payload))
	msg = append(msg, bundleV2SigningPrefix...)
	msg = append(msg, payload...)
	verified := false
	for _, key := range candidates {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, msg, sig) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, ErrInvalidSignature
	}

	// Verified: decode leniently. A KNOWN field with a wrong type still
	// fails here — that is a reject, not a partial apply.
	var b Bundle
	if err := json.Unmarshal(payload, &b); err != nil {
		return nil, fmt.Errorf("fleet: decode verified bundle payload: %w", err)
	}
	if b.KeyID != env.KeyID {
		return nil, fmt.Errorf("%w: payload key_id %q, envelope key_id %q", ErrBundleKeyIDMismatch, b.KeyID, env.KeyID)
	}
	// The v1 signature field has no meaning on a v2 bundle; never let a
	// stray "signature" key inside P masquerade as one.
	b.Signature = ""

	var extra struct {
		MinClientVersions map[string]string `json:"min_client_versions"`
	}
	// Lenient on purpose: a malformed min_client_versions (wrong type) is
	// reported as an unknown field rather than failing the bundle, because
	// ignoring it can only under-report, never relax, what is applied.
	minErr := json.Unmarshal(payload, &extra)

	unknown := unknownBundleFields(payload)
	if minErr != nil {
		unknown = appendUnique(unknown, "min_client_versions")
	}
	return &VerifiedBundle{
		Bundle:            &b,
		Payload:           payload,
		UnknownFields:     unknown,
		MinClientVersions: extra.MinClientVersions,
	}, nil
}

// SignEnvelopeForTesting builds a v2 envelope over payload with priv: the
// envelope key_id is SigningKeyID(priv's public key) unless keyID is
// non-empty. Test seam ONLY (production envelopes are signed by Fleet at
// publish time); exists so cross-package tests drive the real verifier.
func SignEnvelopeForTesting(payload []byte, priv ed25519.PrivateKey, keyID string) ([]byte, error) {
	if keyID == "" {
		keyID = SigningKeyID(priv.Public().(ed25519.PublicKey))
	}
	msg := append([]byte(bundleV2SigningPrefix), payload...)
	env := BundleEnvelope{
		Format:     BundleEnvelopeFormatV2,
		KeyID:      keyID,
		PayloadB64: base64.StdEncoding.EncodeToString(payload),
		Signature:  base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)),
	}
	return json.Marshal(env)
}

// ── unknown-field reporting (WP03) ───────────────────────────────────────────

// v2KnownTopLevelExtras are top-level P keys this build understands that
// are not Bundle fields.
var v2KnownTopLevelExtras = map[string]bool{"min_client_versions": true}

// unknownBundleFields diffs P, decoded as generic JSON, against Bundle's json
// tags — recursively into every struct-typed section (struct, pointer to
// struct, slice of struct, map of struct). json.RawMessage sections are
// opaque by contract and never descended into; map KEYS are data, not field
// names, so only struct-valued map entries are inspected. Returns names
// only — values are never read into the result.
func unknownBundleFields(payload []byte) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(payload, &top); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if seen[name] || len(out) >= maxReportedUnknownFields {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	diffObject(top, reflect.TypeOf(Bundle{}), "", add, v2KnownTopLevelExtras)
	sort.Strings(out)
	return out
}

var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// jsonFields maps a struct type's json names to their field types.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	m := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			if tag == "-" {
				continue
			}
			if n, _, _ := strings.Cut(tag, ","); n != "" {
				name = n
			}
		}
		m[name] = f.Type
	}
	return m
}

func diffObject(obj map[string]json.RawMessage, t reflect.Type, prefix string, add func(string), extras map[string]bool) {
	fields := jsonFields(t)
	for key, raw := range obj {
		ft, ok := fields[key]
		if !ok {
			// encoding/json also matches keys case-insensitively; a key
			// that only differs in case is decoded, so it is not unknown.
			for name, typ := range fields {
				if strings.EqualFold(name, key) {
					ft, ok = typ, true
					break
				}
			}
		}
		if !ok {
			if !extras[key] {
				add(prefix + key)
			}
			continue
		}
		diffValue(raw, ft, prefix+key, add)
	}
}

func diffValue(raw json.RawMessage, t reflect.Type, path string, add func(string)) {
	if t == rawMessageType {
		return // opaque by contract
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t.PkgPath() != reflect.TypeOf(Bundle{}).PkgPath() {
			return // time.Time and other foreign structs: not sections
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			diffObject(obj, t, path+".", add, nil)
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil {
			for _, it := range items {
				diffValue(it, t.Elem(), path+"[]", add)
			}
		}
	case reflect.Map:
		var entries map[string]json.RawMessage
		if json.Unmarshal(raw, &entries) == nil {
			for _, v := range entries {
				diffValue(v, t.Elem(), path+".*", add)
			}
		}
	}
}

func appendUnique(list []string, name string) []string {
	for _, n := range list {
		if n == name {
			return list
		}
	}
	list = append(list, name)
	sort.Strings(list)
	return list
}
