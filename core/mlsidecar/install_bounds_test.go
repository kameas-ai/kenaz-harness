package mlsidecar

// engine-publication-01ENPUB01 WP-H6 (review F2): every byte Install
// pulls from a channel is bounded — the signature by a fixed cap, the
// artifact by the pinned size + 10%, and the whole install by an overall
// deadline — because http_mirror (WP-H4) only aborts a STALLED body.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	httpchannel "github.com/kameas-ai/kenaz-harness/core/bundle/channels/http"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func boundsRequest(channelRoot, sha string) InstallRequest {
	return InstallRequest{
		ChannelKind: localpath.Kind, ChannelPath: channelRoot, Version: "1.0.0",
		ArtifactPath: "engine.zip", ExpectedSHA256: sha,
		Signature: &manifest.SignatureRef{Kind: "ed25519_detached", Locator: "engine.zip.sig", Algorithm: "ed25519", KeyID: "k1"},
	}
}

func okVerifier() Verifier {
	return Verifier{TrustVerifier: fakeTrustVerifier{ok: true}, Policy: integrity.SigningRequired}
}

func TestInstall_OversizedSignatureRefused(t *testing.T) {
	channelRoot := t.TempDir()
	_, sha := buildTestEngineZip(t, channelRoot, "engine.zip", []byte("engine"))
	huge := bytes.Repeat([]byte("s"), channels.MaxSignatureBytes+1)
	if err := os.WriteFile(filepath.Join(channelRoot, "engine.zip.sig"), huge, 0o600); err != nil {
		t.Fatal(err)
	}
	l := NewLayout(t.TempDir())
	_, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, okVerifier(), boundsRequest(channelRoot, sha))
	if !errors.Is(err, ErrVerificationFailed) || !strings.Contains(err.Error(), "exceeds its size cap") {
		t.Fatalf("oversized signature err = %v, want a size-cap verification failure", err)
	}
	if _, ok, _ := ReadInstallJSON(l); ok {
		t.Fatal("refused install wrote install.json")
	}
}

func TestInstall_ArtifactOverrunOfPinnedSizeRefused(t *testing.T) {
	channelRoot := t.TempDir()
	zipPath, sha := buildTestEngineZip(t, channelRoot, "engine.zip", bytes.Repeat([]byte("e"), 4096))
	if err := os.WriteFile(filepath.Join(channelRoot, "engine.zip.sig"), []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(zipPath)

	// Pinned exactly: installs.
	req := boundsRequest(channelRoot, sha)
	req.SizeBytes = st.Size()
	if _, err := Install(context.Background(), NewLayout(t.TempDir()), testChannelRegistry(), secrets.NoopResolver{}, okVerifier(), req); err != nil {
		t.Fatalf("exactly-pinned size refused: %v", err)
	}

	// Mirror serves far more than the pin (+10%) allows: refused, staging cleaned.
	req.SizeBytes = st.Size() / 3
	l := NewLayout(t.TempDir())
	_, err := Install(context.Background(), l, testChannelRegistry(), secrets.NoopResolver{}, okVerifier(), req)
	if !errors.Is(err, channels.ErrFetchTooLarge) {
		t.Fatalf("overrun err = %v, want ErrFetchTooLarge", err)
	}
	if _, serr := os.Stat(filepath.Join(l.Root, ".staging", "1.0.0.zip")); !os.IsNotExist(serr) {
		t.Fatal("overrun left the staged download behind")
	}
	if got := artifactCap(1000); got != 1100 {
		t.Fatalf("artifactCap(1000) = %d, want 1100 (10%% slack)", got)
	}
	if artifactCap(0) != 0 {
		t.Fatal("unknown size must stay uncapped")
	}
}

func TestInstall_NeverEndingTrickleHitsOverallDeadline(t *testing.T) {
	saved := installDeadline
	installDeadline = 400 * time.Millisecond
	t.Cleanup(func() { installDeadline = saved })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return
		}
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for { // one byte every 10ms, forever: never stalls, never ends
			if _, err := w.Write([]byte("x")); err != nil {
				return
			}
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	reg := channels.NewRegistry()
	if err := reg.Register(httpchannel.Kind, httpchannel.Factory); err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{
		ChannelKind: httpchannel.Kind, ChannelURL: srv.URL, Version: "1.0.0",
		ArtifactPath: "engine.zip", ExpectedSHA256: "sha256:" + strings.Repeat("0", 64), // SizeBytes 0: uncapped, only the deadline can stop it
	}
	start := time.Now()
	_, err := Install(context.Background(), NewLayout(t.TempDir()), reg, secrets.NoopResolver{}, okVerifier(), req)
	if err == nil {
		t.Fatal("a never-ending trickle completed")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("trickle err = %v, want the install deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("deadline took %s", elapsed)
	}
}
