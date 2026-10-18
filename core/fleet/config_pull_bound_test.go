package fleet

// bundle-key-rotation WP02: the config bundle body read is bounded at
// maxConfigBundleBytes (4 MiB).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// padBundleJSON returns a validly-signed bundle's JSON padded with trailing
// whitespace to exactly size bytes. json.Unmarshal accepts trailing
// whitespace, so WITHOUT the cap this body would verify and apply — the
// rejection below is the cap's doing, not a parse or signature failure.
func padBundleJSON(t *testing.T, priv ed25519.PrivateKey, id int64, size int) []byte {
	t.Helper()
	data := bundleToJSON(t, buildAndSignBundle(t, priv, id))
	if len(data) > size {
		t.Fatalf("bundle JSON (%d bytes) already exceeds pad target %d", len(data), size)
	}
	return append(data, bytes.Repeat([]byte(" "), size-len(data))...)
}

// TestConfigPoller_OversizedBodyRejected: a body one byte over 4 MiB is
// rejected with ErrConfigBundleTooLarge, nothing is applied, bundle_id does
// not advance, and the config-pull status names the error.
func TestConfigPoller_OversizedBodyRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	fake := &fakeFleetConfigServer{response: padBundleJSON(t, priv, 1, int(maxConfigBundleBytes)+1)}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	p := newPollerForTest(t, srv, applier, t.TempDir())
	err := p.poll(context.Background())
	if !errors.Is(err, ErrConfigBundleTooLarge) {
		t.Fatalf("poll: expected ErrConfigBundleTooLarge, got: %v", err)
	}
	if n := len(applier.snapshot()); n != 0 {
		t.Errorf("applier ran %d time(s) for an oversized body", n)
	}
	st := p.Status()
	if st.LastAppliedID != 0 {
		t.Errorf("LastAppliedID = %d, want 0", st.LastAppliedID)
	}
	if !strings.Contains(st.LastError, "config bundle too large") || !strings.Contains(st.LastError, "4194304") {
		t.Errorf("LastError should name the size rejection and the limit, got: %q", st.LastError)
	}
}

// TestConfigPoller_BodyAtLimitAccepted: exactly 4 MiB is within the cap — the
// boundary is "over", not "at".
func TestConfigPoller_BodyAtLimitAccepted(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	fake := &fakeFleetConfigServer{response: padBundleJSON(t, priv, 1, int(maxConfigBundleBytes))}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &fakeApplier{}
	p := newPollerForTest(t, srv, applier, t.TempDir())
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: a body exactly at the limit must be accepted, got: %v", err)
	}
	if st := p.Status(); st.LastAppliedID != 1 {
		t.Errorf("LastAppliedID = %d, want 1", st.LastAppliedID)
	}
}
