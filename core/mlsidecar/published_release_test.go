package mlsidecar

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// The engine release the harness release build pins (release.yml's
// KENAZ_ML_ENGINE_VERSION / KENAZ_ML_ENGINE_CHANNEL_BASE), and the
// fixtures fetched from it (testdata/engine-release-0.1.1/PROVENANCE.md).
const (
	publishedEngineVersion     = "0.1.1"
	publishedEngineChannelBase = "https://downloads.kameas.ai"
	publishedEngineFixtureDir  = "testdata/engine-release-0.1.1"
)

type publishedIndexEntry struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	SigURL    string `json:"sig_url"`
	KeyID     string `json:"key_id"`
}

// publishedPin rebuilds, from the fixture index, exactly the pin the
// release.yml engine-pin step generates: same derivations, same asserts.
func publishedPin(t *testing.T) (EngineRelease, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(publishedEngineFixtureDir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx struct {
		Releases []struct {
			Version     string              `json:"version"`
			DarwinArm64 publishedIndexEntry `json:"darwin_arm64"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("fixture index.json: %v", err)
	}
	var e *publishedIndexEntry
	for i := range idx.Releases {
		if idx.Releases[i].Version == publishedEngineVersion {
			e = &idx.Releases[i].DarwinArm64
		}
	}
	if e == nil {
		t.Fatalf("version %s not in fixture index", publishedEngineVersion)
	}
	channel := publishedEngineChannelBase + "/kenaz-ml/" + publishedEngineVersion
	artifact := "kenaz-ml-" + publishedEngineVersion + "-darwin-arm64.dmg"
	if e.URL != channel+"/"+artifact || path.Base(e.URL) != artifact || e.SigURL != e.URL+".sig" {
		t.Fatalf("index url %q / sig_url %q disagree with the derived %s/%s(.sig)", e.URL, e.SigURL, channel, artifact)
	}
	pin, err := NewHTTPMirrorPin(publishedEngineVersion, channel, artifact, e.SHA256, e.SizeBytes, e.KeyID)
	if err != nil {
		t.Fatalf("published release does not make a valid pin: %v", err)
	}
	sig, err := os.ReadFile(filepath.Join(publishedEngineFixtureDir, artifact+".sig"))
	if err != nil {
		t.Fatal(err)
	}
	return pin, sig
}

// publishedVerifier is the production install verifier for a FRESH
// trust store: only the boot-seeded baked release anchor (the real
// checked-in release_signing_key.pub), SigningRequired.
func publishedVerifier(t *testing.T) Verifier {
	t.Helper()
	ctx := context.Background()
	anchor, ok, err := BakedReleaseAnchor()
	if err != nil || !ok {
		t.Fatalf("checked-in release_signing_key.pub must be a real key: ok=%v err=%v", ok, err)
	}
	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trust.SeedAnchor(ctx, eng, anchor); err != nil {
		t.Fatal(err)
	}
	tv, err := trust.NewEngineVerifier(eng)
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := eng.ListAnchors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return DefaultEngineVerifier(tv, anchors)
}

// TestPublishedEngineRelease_VerifiesUnderBakedKey is the hermetic proof
// behind enabling the release.yml engine pin: the published 0.1.1
// darwin-arm64 signature verifies through the harness's own verifier
// stack under the compiled-in key, and the index's key_id is that key.
// (The sha256-over-DMG-bytes half needs the 189 MB artifact; release.yml
// re-checks it on every build, and PROVENANCE.md records the one-off.)
func TestPublishedEngineRelease_VerifiesUnderBakedKey(t *testing.T) {
	pin, sig := publishedPin(t)
	anchor, _, _ := BakedReleaseAnchor()
	if pin.Signature.KeyID != anchor.PublicKey.Fingerprint {
		t.Fatalf("index key_id %s != baked release key %s", pin.Signature.KeyID, anchor.PublicKey.Fingerprint)
	}
	v := publishedVerifier(t)
	m := EngineManifest(pin.Version, pin.ArtifactPath, pin.ExpectedSHA256, pin.Signature)
	resolve := func(loc string) ([]byte, error) {
		if loc != pin.ArtifactPath+".sig" {
			t.Fatalf("resolver asked for %q", loc)
		}
		return sig, nil
	}
	ok, err := integrity.VerifyManifestSignatures(context.Background(), m, v.TrustVerifier, v.Anchors, v.Policy, resolve)
	if err != nil || !ok {
		t.Fatalf("published 0.1.1 signature did not verify: ok=%v err=%v", ok, err)
	}
}

// TestPublishedEngineRelease_TamperFailsClosed: every field the
// signature binds is load-bearing — a flipped signature byte, another
// digest, or another version must all be refused.
func TestPublishedEngineRelease_TamperFailsClosed(t *testing.T) {
	pin, sig := publishedPin(t)
	v := publishedVerifier(t)
	verify := func(version, sha string, s []byte) error {
		m := EngineManifest(version, pin.ArtifactPath, sha, pin.Signature)
		ok, err := integrity.VerifyManifestSignatures(context.Background(), m, v.TrustVerifier, v.Anchors, v.Policy,
			func(string) ([]byte, error) { return s, nil })
		if err == nil && !ok {
			t.Fatal("verifier returned ok=false with no error under SigningRequired")
		}
		return err
	}
	flipped := append([]byte(nil), sig...)
	flipped[0] ^= 0x01
	if verify(pin.Version, pin.ExpectedSHA256, flipped) == nil {
		t.Error("a flipped signature byte verified")
	}
	if verify(pin.Version, "sha256:"+fakeHex64, sig) == nil {
		t.Error("another digest verified")
	}
	if verify("0.1.0", pin.ExpectedSHA256, sig) == nil {
		t.Error("another version verified")
	}
}

const fakeHex64 = "0000000000000000000000000000000000000000000000000000000000000001"

// TestPublishedEngineRelease_MatchesReleaseWorkflowPin keeps these
// fixtures and the release.yml pin in lock-step: bumping
// KENAZ_ML_ENGINE_VERSION (or the channel base) without refreshing the
// fixtures fails here, so the pinned release is always one the suite has
// verified hermetically.
func TestPublishedEngineRelease_MatchesReleaseWorkflowPin(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"KENAZ_ML_ENGINE_VERSION":      publishedEngineVersion,
		"KENAZ_ML_ENGINE_CHANNEL_BASE": publishedEngineChannelBase,
	} {
		m := regexp.MustCompile(`(?m)^  ` + name + `: '([^']*)'`).FindSubmatch(wf)
		if m == nil {
			t.Errorf("release.yml has no workflow-level %s: '<value>'", name)
			continue
		}
		if string(m[1]) != want {
			t.Errorf("release.yml %s = %q, fixtures are for %q — refresh %s (see PROVENANCE.md)", name, m[1], want, publishedEngineFixtureDir)
		}
	}
}
