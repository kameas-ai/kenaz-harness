package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer wraps fakeFleetConfigServer and records each GET's checksum.
type countingServer struct {
	inner *fakeFleetConfigServer
	mu    sync.Mutex
	gets  []string
}

func (c *countingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		c.mu.Lock()
		c.gets = append(c.gets, r.URL.Query().Get("checksum"))
		c.mu.Unlock()
	}
	c.inner.ServeHTTP(w, r)
}

func (c *countingServer) snapshotGets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.gets...)
}

// runOnce starts p, waits for cond, stops p.
func runUntil(t *testing.T, p *ConfigPoller, what string, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()
	if !waitUntil(t, 2*time.Second, cond) {
		t.Fatalf("timed out waiting for %s (status %+v)", what, p.Status())
	}
}

// Restart: the cached bundle is re-verified and re-applied BEFORE the first
// poll, even though the server then answers 304.
func TestConfigPoller_RestartReappliesCachedBundle(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := buildAndSignBundle(t, priv, 5)
	srv := httptest.NewServer(&fakeFleetConfigServer{response: bundleToJSON(t, b)})
	defer srv.Close()
	dataDir := t.TempDir() // real on-disk dir

	first := &fakeApplier{}
	p1 := newPollerForTest(t, srv, first, dataDir)
	runUntil(t, p1, "first apply", func() bool { return len(first.snapshot()) == 1 })

	fi, err := os.Stat(bundleBodyPath(dataDir))
	if err != nil {
		t.Fatalf("bundle.json not persisted: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("bundle.json mode = %o, want 600", mode)
	}

	// "Restart": fresh poller + applier (in-memory policy state is gone).
	second := &fakeApplier{}
	p2 := newPollerForTest(t, srv, second, dataDir)
	p2.interval = time.Hour
	runUntil(t, p2, "cached re-apply then 304", func() bool {
		return len(second.snapshot()) == 1 && p2.Status().Source == "fleet"
	})
	got := second.snapshot()
	if got[0].BundleID != 5 || len(got[0].MCPAllowlist) != 1 || got[0].MCPAllowlist[0] != "github" {
		t.Errorf("re-applied bundle = %+v", got[0])
	}
	if st := p2.Status(); st.LastAppliedID != 5 || st.LastAppliedAt == "" || st.LastError != "" {
		t.Errorf("status = %+v", st)
	}
}

// A tampered cache is discarded, never applied, and the checksum cleared so
// the next poll re-fetches (and the same id may verify again).
func TestConfigPoller_TamperedCacheDiscardedAndRefetched(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := buildAndSignBundle(t, priv, 9)
	cs := &countingServer{inner: &fakeFleetConfigServer{response: bundleToJSON(t, b)}}
	srv := httptest.NewServer(cs)
	defer srv.Close()
	dataDir := t.TempDir()

	a1 := &fakeApplier{}
	runUntil(t, newPollerForTest(t, srv, a1, dataDir), "first apply", func() bool { return len(a1.snapshot()) == 1 })

	raw, _ := os.ReadFile(bundleBodyPath(dataDir))
	tampered := strings.Replace(string(raw), `"github"`, `"evil"`, 1)
	if tampered == string(raw) {
		t.Fatal("tamper did not change the cache")
	}
	if err := os.WriteFile(bundleBodyPath(dataDir), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}

	a2 := &fakeApplier{}
	p2 := newPollerForTest(t, srv, a2, dataDir)
	p2.interval = time.Hour
	before := len(cs.snapshotGets())
	runUntil(t, p2, "refetch apply", func() bool { return len(a2.snapshot()) == 1 && p2.Status().Source == "fleet" })
	for _, ab := range a2.snapshot() {
		for _, id := range ab.MCPAllowlist {
			if id == "evil" {
				t.Fatal("tampered cache was applied")
			}
		}
	}
	gets := cs.snapshotGets()[before:]
	if len(gets) == 0 || gets[0] != "" {
		t.Errorf("first GET after discard sent checksum %q, want empty (re-fetch)", gets)
	}
	// The discarded cache is replaced by the freshly verified bundle.
	if raw2, _ := os.ReadFile(bundleBodyPath(dataDir)); strings.Contains(string(raw2), "evil") {
		t.Error("tampered bundle.json still on disk")
	}
}

// The pinned key set changed (e.g. a different env): the cache no longer
// verifies and is discarded rather than re-applied.
func TestConfigPoller_CacheUnderUnpinnedKeyDiscarded(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := buildAndSignBundle(t, priv, 3)
	dataDir := t.TempDir()
	body := bundleToJSON(t, b)
	if err := saveBundleBody(dataDir, body); err != nil {
		t.Fatal(err)
	}
	if err := saveBundleState(dataDir, 3, hexChecksumOf(body)); err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, other)

	a := &fakeApplier{}
	p := NewConfigPoller(nil, dataDir, a) // nil client: no network
	if id, csum, _ := loadBundleState(dataDir); id != 3 || csum == "" {
		t.Fatalf("precondition state id=%d cs=%q", id, csum)
	}
	runUntil(t, p, "discard", func() bool { return strings.Contains(p.Status().LastError, "cached bundle not re-applied") })
	if n := len(a.snapshot()); n != 0 {
		t.Fatalf("unverifiable cache applied %d times", n)
	}
	if _, err := os.Stat(bundleBodyPath(dataDir)); !os.IsNotExist(err) {
		t.Errorf("bundle.json not removed: %v", err)
	}
	st := p.Status()
	if st.BundleChecksum != "" || st.Source != "default-deny" {
		t.Errorf("status after discard = %+v", st)
	}
}

// State written by an older build (id + checksum, no bundle.json): the first
// poll must re-fetch instead of taking a 304 that re-applies nothing.
func TestConfigPoller_LegacyStateWithoutBodyRefetches(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := buildAndSignBundle(t, priv, 4)
	body := bundleToJSON(t, b)
	srv := httptest.NewServer(&fakeFleetConfigServer{response: body})
	defer srv.Close()
	dataDir := t.TempDir()
	if err := saveBundleState(dataDir, 4, hexChecksumOf(body)); err != nil {
		t.Fatal(err)
	}
	if err := saveBundleApplyMeta(dataDir, bundleApplyMeta{BuildVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	a := &fakeApplier{}
	p := newPollerForTest(t, srv, a, dataDir)
	p.SetBuildVersion("v1")
	p.interval = time.Hour
	runUntil(t, p, "re-fetch apply", func() bool { return len(a.snapshot()) == 1 && p.Status().Source == "fleet" })
	if _, err := os.Stat(bundleBodyPath(dataDir)); err != nil {
		t.Errorf("bundle.json not written after re-fetch: %v", err)
	}
}

// Served mode: the token source is installed after the poller's first
// poll found no session. Installing it must trigger an immediate poll, not
// one ~5 minutes later.
func TestConfigPoller_WakesWhenTokenSourceInstalled(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	b := buildAndSignBundle(t, priv, 2)
	cs := &countingServer{inner: &fakeFleetConfigServer{response: bundleToJSON(t, b)}}
	srv := httptest.NewServer(cs)
	defer srv.Close()

	var token atomic.Value
	token.Store("")
	SetExternalTokenSource(func() string { return token.Load().(string) })
	t.Cleanup(func() { SetExternalTokenSource(nil) })

	a := &fakeApplier{}
	p := NewConfigPoller(makeTestClient(t, srv.URL), t.TempDir(), a)
	p.interval = time.Hour // only a wake can produce a second round
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	if !waitUntil(t, 2*time.Second, func() bool {
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.awaitingSignIn
	}) {
		t.Fatal("first poll did not park on ErrNotSignedIn")
	}
	if n := len(a.snapshot()); n != 0 {
		t.Fatalf("applied before sign-in: %d", n)
	}

	token.Store("at-served")
	NotifyTokensAvailable() // what servedfleet does on a broker token
	if !waitUntil(t, 2*time.Second, func() bool { return len(a.snapshot()) == 1 }) {
		t.Fatal("no immediate poll after the token arrived")
	}

	// Signed in: further notifications (routine renewals) do not poll.
	gets := len(cs.snapshotGets())
	NotifyTokensAvailable()
	time.Sleep(100 * time.Millisecond)
	if n := len(cs.snapshotGets()); n != gets {
		t.Errorf("a renewal notification while signed in caused %d extra polls", n-gets)
	}
}
