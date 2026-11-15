package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// orgStub is a race-safe, mutable org source for ConfigPoller.orgOf.
type orgStub struct {
	mu  sync.Mutex
	org string
}

func (o *orgStub) set(org string) { o.mu.Lock(); o.org = org; o.mu.Unlock() }
func (o *orgStub) get() string    { o.mu.Lock(); defer o.mu.Unlock(); return o.org }

// ctxApplier records every apply with the context markers it carried.
type ctxApplier struct {
	mu      sync.Mutex
	applies []ctxApply
}

type ctxApply struct {
	bundleID     int64
	orgReset     bool
	cacheRestore bool
}

func (a *ctxApplier) ApplyBundle(ctx context.Context, b *Bundle) []error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.applies = append(a.applies, ctxApply{bundleID: b.BundleID, orgReset: IsOrgReset(ctx), cacheRestore: IsCacheRestore(ctx)})
	return nil
}

func (a *ctxApplier) snapshot() []ctxApply {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ctxApply, len(a.applies))
	copy(out, a.applies)
	return out
}

func (a *ctxApplier) count(pred func(ctxApply) bool) int {
	n := 0
	for _, x := range a.snapshot() {
		if pred(x) {
			n++
		}
	}
	return n
}

// An org switch must not keep org A's state: org B's bundle 3 applies even
// though org A's last id was 50 (no non-monotonic rejection), and A's
// sections are cleared by an empty reset apply in between.
func TestConfigPoller_OrgChange_ResetsBundleState(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)

	fake := &fakeFleetConfigServer{response: bundleToJSON(t, buildAndSignBundle(t, priv, 50))}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &ctxApplier{}
	dataDir := t.TempDir()
	org := &orgStub{org: "org-a"}
	p := newPollerForTest(t, srv, applier, dataDir)
	p.orgOf = org.get

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	if !waitUntil(t, time.Second, func() bool { return p.Status().LastAppliedID == 50 }) {
		t.Fatalf("org A bundle 50 not applied: %+v", p.Status())
	}
	if got := loadBundleOrg(dataDir); got != "org-a" {
		t.Fatalf("persisted org = %q, want org-a", got)
	}

	// Sign in to org B, whose bundle ids are lower.
	fake.setResponse(bundleToJSON(t, buildAndSignBundle(t, priv, 3)))
	org.set("org-b")

	if !waitUntil(t, 2*time.Second, func() bool { return p.Status().LastAppliedID == 3 }) {
		t.Fatalf("org B bundle 3 not applied after org change: %+v", p.Status())
	}
	if st := p.Status(); st.LastError != "" {
		t.Fatalf("lastError after org change = %q", st.LastError)
	}
	resets := applier.count(func(a ctxApply) bool { return a.orgReset })
	if resets != 1 {
		t.Fatalf("org-reset clear applies = %d, want 1 (%+v)", resets, applier.snapshot())
	}
	// The reset apply happened before org B's bundle.
	seenReset := false
	for _, a := range applier.snapshot() {
		if a.orgReset {
			if a.bundleID != 0 {
				t.Fatalf("reset apply carried bundle %d, want an empty bundle", a.bundleID)
			}
			seenReset = true
		}
		if a.bundleID == 3 && !seenReset {
			t.Fatal("org B bundle applied before org A's sections were cleared")
		}
	}
	if got := loadBundleOrg(dataDir); got != "org-b" {
		t.Fatalf("persisted org = %q, want org-b", got)
	}
	if id, _, _ := loadBundleState(dataDir); id != 3 {
		t.Fatalf("persisted bundle id = %d, want 3", id)
	}
}

// writeCachedState seeds dataDir as a previous run would have left it.
func writeCachedState(t *testing.T, dataDir string, body []byte, id int64, org string) {
	t.Helper()
	sum := sha256.Sum256(body)
	if err := saveBundleState(dataDir, id, fmt.Sprintf("%x", sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := saveBundleApplyMeta(dataDir, bundleApplyMeta{BuildVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := saveBundleBody(dataDir, body); err != nil {
		t.Fatal(err)
	}
	if org != "" {
		if err := saveBundleOrg(dataDir, org); err != nil {
			t.Fatal(err)
		}
	}
}

// On restart under a different org, the cached bundle of the previous org
// is NOT re-enforced and its cache files are removed.
func TestConfigPoller_OrgChangeAtStart_DoesNotRestoreOtherOrgCache(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	body := bundleToJSON(t, buildAndSignBundle(t, priv, 40))
	dataDir := t.TempDir()
	writeCachedState(t, dataDir, body, 40, "org-a")

	fake := &fakeFleetConfigServer{} // always 304: org B has nothing new
	srv := httptest.NewServer(fake)
	defer srv.Close()

	applier := &ctxApplier{}
	p := newPollerForTest(t, srv, applier, dataDir)
	p.SetBuildVersion("v1")
	p.orgOf = func() string { return "org-b" }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	if !waitUntil(t, 500*time.Millisecond, func() bool { return applier.count(func(a ctxApply) bool { return a.orgReset }) == 1 }) {
		t.Fatalf("no org-reset clear apply: %+v", applier.snapshot())
	}
	time.Sleep(50 * time.Millisecond)
	if n := applier.count(func(a ctxApply) bool { return a.cacheRestore }); n != 0 {
		t.Fatalf("org A's cached bundle re-applied %d time(s) under org B", n)
	}
	st := p.Status()
	if st.LastAppliedID != 0 || st.Source != "default-deny" || st.BundleChecksum != "" {
		t.Fatalf("status after org change = %+v, want id 0, default-deny, no checksum", st)
	}
	if _, err := os.Stat(bundleBodyPath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("org A bundle.json still on disk: %v", err)
	}
	if got := loadBundleOrg(dataDir); got != "org-b" {
		t.Fatalf("persisted org = %q, want org-b", got)
	}
}

// State from a build before per-org keying (no bundle_org.txt) is adopted
// by the current org, not wiped; an unknown current org changes nothing.
func TestConfigPoller_OrgUnknownOrLegacy_NoReset(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setTestSigningKey(t, pub)
	body := bundleToJSON(t, buildAndSignBundle(t, priv, 9))

	for _, tc := range []struct {
		name, stored, current, wantStored string
	}{
		{"legacy state adopted", "", "org-a", "org-a"},
		{"unknown current org", "org-a", "", "org-a"},
		{"same org", "org-a", "org-a", "org-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeCachedState(t, dataDir, body, 9, tc.stored)
			srv := httptest.NewServer(&fakeFleetConfigServer{response: body})
			defer srv.Close()
			applier := &ctxApplier{}
			p := newPollerForTest(t, srv, applier, dataDir)
			p.SetBuildVersion("v1")
			cur := tc.current
			p.orgOf = func() string { return cur }
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			p.Start(ctx)
			if !waitUntil(t, 500*time.Millisecond, func() bool { return p.Status().Source == "fleet" }) {
				t.Fatalf("cached bundle not confirmed: %+v", p.Status())
			}
			p.Stop()
			if n := applier.count(func(a ctxApply) bool { return a.orgReset }); n != 0 {
				t.Fatalf("unexpected org reset (%d)", n)
			}
			if n := applier.count(func(a ctxApply) bool { return a.cacheRestore && a.bundleID == 9 }); n != 1 {
				t.Fatalf("cached bundle restore count = %d, want 1", n)
			}
			if st := p.Status(); st.LastAppliedID != 9 {
				t.Fatalf("LastAppliedID = %d, want 9", st.LastAppliedID)
			}
			if got := loadBundleOrg(dataDir); got != tc.wantStored {
				t.Fatalf("persisted org = %q, want %q", got, tc.wantStored)
			}
		})
	}
}
