package mlsidecar

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// bakedReleaseKeyFile is the compiled-in Kameas ML engine release public
// key (engine-publication-01ENPUB01 WP-H2, owner decision 2026-10-03 #4:
// "a baked-in kameas release public key seeded as an anchor at boot, so
// open-source/local-first installs verify too"). The checked-in file
// holds the real kenaz-ml release key since #388 (2026-10-07); the
// release.yml engine-pin step fails the build unless the pinned engine's
// key_id is this key.
//
//go:embed release_signing_key.pub
var bakedReleaseKeyFile string

// releaseSigningPubKeyHex, when non-empty, overrides the embedded file —
// set per environment build with
//
//	-ldflags "-X github.com/kameas-ai/kenaz-harness/core/mlsidecar.releaseSigningPubKeyHex=<64 hex>"
//
// mirroring core/fleet.fleetSigningPublicKeyBytes. Empty at source time.
var releaseSigningPubKeyHex string

// ErrBadBakedReleaseKey reports a baked key that is present but
// malformed (wrong length / not hex). Boot logs it and seeds nothing.
var ErrBadBakedReleaseKey = errors.New("mlsidecar: baked release signing key is malformed")

// BakedReleaseAnchorOrigin is the Metadata["origin"] value a seeded
// release anchor carries, distinguishing it from operator- or
// fleet-installed anchors in the trust store.
const BakedReleaseAnchorOrigin = "baked_release_key"

// bakedReleaseKey returns the effective baked public key. ok=false (and
// nil error) means "placeholder / not configured": nothing to seed.
func bakedReleaseKey() (ed25519.PublicKey, bool, error) {
	text := releaseSigningPubKeyHex
	if strings.TrimSpace(text) == "" {
		text = bakedReleaseKeyFile
	}
	var keyLine string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if keyLine != "" {
			return nil, false, fmt.Errorf("%w: more than one key line", ErrBadBakedReleaseKey)
		}
		keyLine = line
	}
	if keyLine == "" {
		return nil, false, nil
	}
	raw, err := hex.DecodeString(keyLine)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, false, fmt.Errorf("%w: want %d bytes of hex", ErrBadBakedReleaseKey, ed25519.PublicKeySize)
	}
	allZero := true
	for _, b := range raw {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, false, nil
	}
	return ed25519.PublicKey(raw), true, nil
}

// BakedReleaseAnchor returns the trust anchor the harness seeds at boot
// for the compiled-in release key, or ok=false when this build carries
// only the placeholder. The anchor id is scoped by KeyID
// ("kameas-ml-release-<first 16 hex of KeyID>") so a later build that
// bakes a ROTATED key seeds a second anchor rather than colliding with
// (and being blocked by, or overwriting) the old one; retiring the old
// key is an explicit trust-store action (RemoveAnchor / fleet), never a
// side effect of an upgrade.
func BakedReleaseAnchor() (trust.Anchor, bool, error) {
	pub, ok, err := bakedReleaseKey()
	if err != nil || !ok {
		return trust.Anchor{}, false, err
	}
	keyID := trust.ComputeFingerprint(pub)
	return trust.Anchor{
		AnchorID:    "kameas-ml-release-" + keyID[:16],
		Kind:        trust.AnchorRawPublicKey,
		Algorithm:   trust.AlgEd25519,
		PublicKey:   trust.PublicKey{Algorithm: trust.AlgEd25519, Bytes: append([]byte(nil), pub...), Fingerprint: keyID},
		InstalledBy: "system:" + BakedReleaseAnchorOrigin,
		Metadata: map[string]string{
			"origin":  BakedReleaseAnchorOrigin,
			"purpose": "kameas-ml engine release signatures",
		},
	}, true, nil
}

// SetBakedReleaseKeyForTesting overrides the baked key (hex, as the
// -ldflags override takes it; "" restores placeholder behaviour) and
// returns a restore func. Test seam only — production sets the key
// exclusively through the embedded file or -ldflags. Mirrors
// core/fleet.SetSigningKeyForTesting; exported because the boot-seeding
// wiring under test lives in core/rpc.
func SetBakedReleaseKeyForTesting(hexKey string) (restore func()) {
	savedHex, savedFile := releaseSigningPubKeyHex, bakedReleaseKeyFile
	releaseSigningPubKeyHex = hexKey
	if hexKey == "" {
		bakedReleaseKeyFile = ""
	}
	return func() { releaseSigningPubKeyHex, bakedReleaseKeyFile = savedHex, savedFile }
}
