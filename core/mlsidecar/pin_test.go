package mlsidecar

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	testPinURL  = "https://downloads.kameas.ai/kenaz-ml/1.2.0"
	testPinDMG  = "kenaz-ml-1.2.0-darwin-arm64.dmg"
	testPinSHA  = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testPinKey  = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	testPinSize = int64(132_120_576)
)

// TestPinnedReleaseGen_NoDrift is the drift guard for the checked-in
// pinned_release_gen.go: it must be byte-for-byte what pin-gen renders
// for the value it holds, and a non-zero pin must be exactly what
// NewHTTPMirrorPin would build from its own fields (so a hand edit that
// breaks the publish contract — a locator, a channel kind — fails CI).
func TestPinnedReleaseGen_NoDrift(t *testing.T) {
	onDisk, err := os.ReadFile("pinned_release_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	want, err := RenderPinnedReleaseSource(pinnedRelease)
	if err != nil {
		t.Fatalf("checked-in pin does not render: %v", err)
	}
	if !bytes.Equal(onDisk, want) {
		t.Fatalf("pinned_release_gen.go drifted from `go run ./cmd/kenaz-ml-sign pin-gen` output; regenerate it, never hand-edit.\n--- want ---\n%s", want)
	}
	if pinnedRelease.Version == "" {
		return
	}
	p := pinnedRelease
	if p.Signature == nil {
		t.Fatal("a non-zero pin must carry its signature ref")
	}
	rebuilt, err := NewHTTPMirrorPin(p.Version, p.ChannelURL, p.ArtifactPath, p.ExpectedSHA256, p.SizeBytes, p.Signature.KeyID)
	if err != nil {
		t.Fatalf("checked-in pin violates the publish contract: %v", err)
	}
	if !reflect.DeepEqual(rebuilt, p) {
		t.Fatalf("checked-in pin %+v != canonical %+v", p, rebuilt)
	}
}

func TestNewHTTPMirrorPin_CanonicalShape(t *testing.T) {
	r, err := NewHTTPMirrorPin("1.2.0", testPinURL+"/", testPinDMG, testPinSHA, testPinSize, testPinKey)
	if err != nil {
		t.Fatal(err)
	}
	if r.ChannelKind != "http_mirror" || r.ChannelURL != testPinURL || r.ArtifactPath != testPinDMG ||
		r.Signature == nil || r.Signature.Locator != testPinDMG+".sig" || r.Signature.Kind != "ed25519_detached" ||
		r.Signature.Algorithm != "ed25519" || r.Signature.KeyID != testPinKey || r.SizeMB() != 126 ||
		r.Source != "http_mirror:"+testPinURL+"/"+testPinDMG {
		t.Fatalf("pin = %+v / sig %+v", r, r.Signature)
	}
	req := r.InstallRequest()
	if req.ChannelURL != testPinURL || req.Signature.Locator != testPinDMG+".sig" || req.ExpectedSHA256 != testPinSHA {
		t.Fatalf("install request = %+v", req)
	}
}

func TestNewHTTPMirrorPin_RejectsBadInputs(t *testing.T) {
	type in struct {
		v, u, a, s string
		n          int64
		k          string
	}
	ok := in{"1.2.0", testPinURL, testPinDMG, testPinSHA, testPinSize, testPinKey}
	cases := map[string]func(*in){
		"no version":      func(x *in) { x.v = "" },
		"v-prefix":        func(x *in) { x.v = "v1.2.0" },
		"http url":        func(x *in) { x.u = "http://downloads.kameas.ai/kenaz-ml/1.2.0" },
		"url with query":  func(x *in) { x.u = testPinURL + "?t=1" },
		"nested artifact": func(x *in) { x.a = "1.2.0/" + testPinDMG },
		"zip artifact":    func(x *in) { x.a = "kenaz-ml-1.2.0.zip" },
		"bare hex sha":    func(x *in) { x.s = strings.TrimPrefix(testPinSHA, "sha256:") },
		"zero size":       func(x *in) { x.n = 0 },
		"short key id":    func(x *in) { x.k = "k1" },
	}
	for name, mut := range cases {
		x := ok
		mut(&x)
		if _, err := NewHTTPMirrorPin(x.v, x.u, x.a, x.s, x.n, x.k); !errors.Is(err, ErrInvalidPin) {
			t.Errorf("%s: err = %v, want ErrInvalidPin", name, err)
		}
	}
}

func TestRenderPinnedReleaseSource_NonZeroIsGofmtAndComplete(t *testing.T) {
	r, err := NewHTTPMirrorPin("1.2.0", testPinURL, testPinDMG, testPinSHA, testPinSize, testPinKey)
	if err != nil {
		t.Fatal(err)
	}
	src, err := RenderPinnedReleaseSource(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"DO NOT EDIT", `Version: "1.2.0"`, `"http_mirror"`, testPinURL, testPinDMG + `.sig"`,
		testPinSHA, testPinKey, "SizeBytes: 132120576", `import "github.com/kameas-ai/kenaz-harness/core/bundle/manifest"`,
	} {
		if !strings.Contains(strings.Join(strings.Fields(string(src)), " "), want) {
			t.Errorf("rendered pin lacks %q:\n%s", want, src)
		}
	}
	if _, err := RenderPinnedReleaseSource(EngineRelease{SizeBytes: 1}); !errors.Is(err, ErrInvalidPin) {
		t.Fatalf("a version-less non-zero pin rendered: %v", err)
	}
}

func TestPinnedEngineRelease_ReturnsNonZeroPinAndProtectsIt(t *testing.T) {
	r, err := NewHTTPMirrorPin("1.2.0", testPinURL, testPinDMG, testPinSHA, testPinSize, testPinKey)
	if err != nil {
		t.Fatal(err)
	}
	defer SetPinnedReleaseForTesting(r)()
	got, err := PinnedEngineRelease(context.Background())
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("PinnedEngineRelease = %+v, %v", got, err)
	}
	got.Signature.KeyID = "tampered"
	again, _ := PinnedEngineRelease(context.Background())
	if again.Signature.KeyID != testPinKey {
		t.Fatal("a caller mutated the build-time pin through the returned Signature pointer")
	}
}
