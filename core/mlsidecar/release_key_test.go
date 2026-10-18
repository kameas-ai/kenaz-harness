package mlsidecar

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

const placeholderKeyFile = "# NOT-A-REAL-KEY\n# swap me\n"

func withBakedKey(t *testing.T, file, override string) {
	t.Helper()
	savedFile, savedHex := bakedReleaseKeyFile, releaseSigningPubKeyHex
	bakedReleaseKeyFile, releaseSigningPubKeyHex = file, override
	t.Cleanup(func() { bakedReleaseKeyFile, releaseSigningPubKeyHex = savedFile, savedHex })
}

func TestBakedReleaseAnchor_PlaceholderSeedsNothing(t *testing.T) {
	for name, file := range map[string]string{
		"comment-only placeholder": placeholderKeyFile,
		"empty":                    "",
		"all-zero key":             "# zero\n" + strings.Repeat("00", 32) + "\n",
	} {
		withBakedKey(t, file, "")
		if a, ok, err := BakedReleaseAnchor(); ok || err != nil || a.AnchorID != "" {
			t.Errorf("%s: BakedReleaseAnchor = (%+v, %v, %v), want nothing to seed", name, a, ok, err)
		}
	}
}

func TestBakedReleaseAnchor_CheckedInFileParses(t *testing.T) {
	// Whatever is checked in (placeholder today, the real key after the
	// owner's swap) must parse — never a malformed boot warning.
	if _, _, err := bakedReleaseKey(); err != nil {
		t.Fatalf("checked-in release_signing_key.pub does not parse: %v", err)
	}
}

func TestBakedReleaseAnchor_MalformedIsAnError(t *testing.T) {
	for name, file := range map[string]string{
		"not hex":   "zz" + strings.Repeat("0", 62),
		"short":     "abcd",
		"two lines": strings.Repeat("ab", 32) + "\n" + strings.Repeat("cd", 32),
	} {
		withBakedKey(t, file, "")
		if _, ok, err := BakedReleaseAnchor(); ok || !errors.Is(err, ErrBadBakedReleaseKey) {
			t.Errorf("%s: ok=%v err=%v, want ErrBadBakedReleaseKey", name, ok, err)
		}
	}
}

// TestBakedReleaseAnchor_FreshInstallVerifiesBakedKeySignature is the
// WP-H2 headline pin: a fresh trust store + the boot seed is enough for
// the engine verifier (SigningRequired, the install path's) to accept a
// manifest signed by the baked key — no operator step, no fleet.
func TestBakedReleaseAnchor_FreshInstallVerifiesBakedKeySignature(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// The -ldflags override wins over the placeholder file.
	withBakedKey(t, placeholderKeyFile, hex.EncodeToString(pub))
	anchor, ok, err := BakedReleaseAnchor()
	if err != nil || !ok {
		t.Fatalf("BakedReleaseAnchor ok=%v err=%v", ok, err)
	}
	keyID := trust.ComputeFingerprint(pub)
	if anchor.PublicKey.Fingerprint != keyID || anchor.AnchorID != "kameas-ml-release-"+keyID[:16] ||
		anchor.Metadata["origin"] != BakedReleaseAnchorOrigin {
		t.Fatalf("anchor = %+v", anchor)
	}

	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := trust.SeedAnchor(ctx, eng, anchor); err != nil || out != trust.SeedInstalled {
		t.Fatalf("seed = %q, %v", out, err)
	}

	dir := t.TempDir()
	artifact := filepath.Join(dir, "kenaz-ml-1.0.0-darwin-arm64.dmg")
	if err := os.WriteFile(artifact, []byte("dmg bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha, _ := HashFileSHA256(artifact)
	name := filepath.Base(artifact)
	sig := ed25519.Sign(priv, EngineManifest("1.0.0", name, sha, nil).SigningPayload())
	m := EngineManifest("1.0.0", name, sha, &manifest.SignatureRef{
		Kind: "ed25519_detached", Locator: name + ".sig", Algorithm: "ed25519", KeyID: keyID,
	})

	tv, _ := trust.NewEngineVerifier(eng)
	anchors, _ := eng.ListAnchors(ctx)
	v := DefaultEngineVerifier(tv, anchors)
	resolve := func(string) ([]byte, error) { return sig, nil }
	verified, err := v.VerifyEngineArtifact(ctx, m, artifact, resolve)
	if err != nil || !verified {
		t.Fatalf("fresh install did not verify a baked-key signature: verified=%v err=%v", verified, err)
	}

	// Control: the same flow WITHOUT the seed fails closed.
	bare, _ := trust.NewEngine(trust.Config{})
	tv2, _ := trust.NewEngineVerifier(bare)
	if ok, err := DefaultEngineVerifier(tv2, nil).VerifyEngineArtifact(ctx, m, artifact, resolve); ok || err == nil {
		t.Fatalf("unseeded store verified: ok=%v err=%v", ok, err)
	}
}
