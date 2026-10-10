package mlproducer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeConsent is a ConsentSource the test body mutates while the gate's
// refresh goroutines read it: mutex + snapshot.
type fakeConsent struct {
	mu      sync.Mutex
	id      Identity
	idErr   error
	caps    CapabilityState
	capsErr error
	eff     bool
	excl    Exclusions
	effErr  error
	mlReads int
	idReads int
}

func devConsent() *fakeConsent {
	return &fakeConsent{
		id:   Identity{SignedIn: true, Enrolled: true, ResourceOrgID: "390451413051827052", NodeID: "NODE1"},
		caps: CapabilityState{HostedInference: true},
		eff:  true,
	}
}

func (f *fakeConsent) set(fn func(*fakeConsent)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeConsent) reads() (id, ml int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.idReads, f.mlReads
}

func (f *fakeConsent) Identity(context.Context) (Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idReads++
	return f.id, f.idErr
}

func (f *fakeConsent) Capabilities(context.Context) (CapabilityState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.caps, f.capsErr
}

func (f *fakeConsent) MLConsent(context.Context) (MLConsent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mlReads++
	if f.effErr != nil {
		return MLConsent{}, f.effErr
	}
	return MLConsent{Effective: f.eff, Exclusions: f.excl}, nil
}

type purgeCounter struct {
	mu sync.Mutex
	n  int
}

func (p *purgeCounter) purge(context.Context) error { p.mu.Lock(); p.n++; p.mu.Unlock(); return nil }
func (p *purgeCounter) count() int                  { p.mu.Lock(); defer p.mu.Unlock(); return p.n }

// WP05 removed the dev-org guard: an org other than the internal dev org
// opens the gate whenever every spec §4 condition holds.
func TestConsentGate_AnyOrgOpensWhenEffective(t *testing.T) {
	t.Parallel()
	src := devConsent()
	src.set(func(f *fakeConsent) { f.id.ResourceOrgID = "999000111222333444" })
	var p purgeCounter
	g := NewConsentGate(GateConfig{Source: src, Purge: p.purge})
	if d := g.Refresh(context.Background()); !d.Open || d.ResourceOrgID != "999000111222333444" {
		t.Fatalf("decision = %+v, want open for a customer org", d)
	}
	if p.count() != 0 {
		t.Errorf("purged %d times on an open gate", p.count())
	}
}

// The /me/ml read hands the newest compiled exclusions to OnExclusions
// before the decision is stored — on every read, effective or not — and a
// list that cannot be compiled closes the gate without purging.
func TestConsentGate_PassesNewestExclusions(t *testing.T) {
	t.Parallel()
	src := devConsent()
	src.set(func(f *fakeConsent) {
		f.excl = Exclusions{Paths: []string{"hr/**"}, Commands: []string{"ssh"}, Version: 2}
	})
	var mu sync.Mutex
	var got []*ExclusionSet
	var p purgeCounter
	g := NewConsentGate(GateConfig{Source: src, Purge: p.purge, OnExclusions: func(s *ExclusionSet) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	}})
	last := func() *ExclusionSet {
		mu.Lock()
		defer mu.Unlock()
		if len(got) == 0 {
			return nil
		}
		return got[len(got)-1]
	}
	if d := g.Refresh(context.Background()); !d.Open {
		t.Fatalf("decision = %+v", d)
	}
	if s := last(); s == nil || s.Version() != 2 || !s.MatchPath("/w/hr/a.txt") || !s.MatchCommand("ssh host") {
		t.Fatalf("exclusions not handed over: %+v", s)
	}

	// Narrowing: a new entry applies on the next read.
	src.set(func(f *fakeConsent) {
		f.excl = Exclusions{Paths: []string{"hr/**", "/secret/**"}, Commands: []string{"ssh"}, Version: 3}
	})
	g.Refresh(context.Background())
	if s := last(); s.Version() != 3 || !s.MatchPath("/secret/x") {
		t.Fatalf("narrowed set not applied: v%d", s.Version())
	}

	// Not effective (a broadening change bumps notice_version server side):
	// the set still updates; the gate closes on effective alone.
	src.set(func(f *fakeConsent) { f.eff = false; f.excl = Exclusions{Version: 4} })
	if d := g.Refresh(context.Background()); d.Open || d.Reason != ReasonNotEffective {
		t.Fatalf("decision = %+v", d)
	}
	if s := last(); s.Version() != 4 || !s.Empty() {
		t.Fatalf("set after broadening = v%d", s.Version())
	}

	// A pattern this build cannot compile: closed, transient, set untouched.
	src.set(func(f *fakeConsent) { f.eff = true; f.excl = Exclusions{Paths: []string{"hr/[x"}, Version: 5} })
	purgesBefore := p.count()
	if d := g.Refresh(context.Background()); d.Open || d.Reason != ReasonExclusionsInvalid || d.Definitive {
		t.Fatalf("decision = %+v, want closed / %s / transient", d, ReasonExclusionsInvalid)
	}
	if s := last(); s.Version() != 4 {
		t.Errorf("an uncompilable set was handed over (v%d)", s.Version())
	}
	if p.count() != purgesBefore {
		t.Errorf("an invalid exclusion list purged the outbox")
	}
}

// Every condition of spec §4 closes the gate on its own, with its reason
// and its purge class.
func TestConsentGate_ConditionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		mut        func(*fakeConsent)
		open       bool
		reason     string
		definitive bool
		wantMLRead bool
	}{
		{"all conditions hold", func(*fakeConsent) {}, true, "", false, true},
		{"identity read error", func(f *fakeConsent) { f.idErr = errors.New("keychain") }, false, ReasonConsentReadError, false, false},
		{"signed out", func(f *fakeConsent) { f.id.SignedIn = false }, false, ReasonSignedOut, false, false},
		{"not enrolled", func(f *fakeConsent) { f.id.Enrolled = false }, false, ReasonNotEnrolled, false, false},
		{"no node id", func(f *fakeConsent) { f.id.NodeID = "" }, false, ReasonNoNodeID, false, false},
		{"no resource-owner claim", func(f *fakeConsent) { f.id.ResourceOrgID = "" }, false, ReasonNoOrgClaim, false, false},
		{"capabilities unknown", func(f *fakeConsent) { f.capsErr = ErrCapabilitiesUnknown }, false, ReasonCapsUnknown, false, false},
		{"org paused (not a withdrawal)", func(f *fakeConsent) { f.caps.Paused = true }, false, ReasonOrgPaused, false, false},
		{"hosted_inference off", func(f *fakeConsent) { f.caps.HostedInference = false }, false, ReasonNotEntitled, true, false},
		{"/me/ml read fails", func(f *fakeConsent) { f.effErr = errors.New("503") }, false, ReasonConsentReadError, false, true},
		{"/me/ml not effective", func(f *fakeConsent) { f.eff = false }, false, ReasonNotEffective, true, true},
		{"exclusions cannot be honoured", func(f *fakeConsent) { f.excl.Commands = []string{"   "} }, false, ReasonExclusionsInvalid, false, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := devConsent()
			src.set(tc.mut)
			var p purgeCounter
			g := NewConsentGate(GateConfig{Source: src, Purge: p.purge})
			d := g.Refresh(context.Background())
			if d.Open != tc.open || d.Reason != tc.reason || d.Definitive != tc.definitive {
				t.Fatalf("decision = %+v, want open=%v reason=%q definitive=%v", d, tc.open, tc.reason, tc.definitive)
			}
			if d.Open && (d.ResourceOrgID != "390451413051827052" || d.NodeID != "NODE1") {
				t.Errorf("open decision carries org=%q node=%q", d.ResourceOrgID, d.NodeID)
			}
			if _, ml := src.reads(); (ml > 0) != tc.wantMLRead {
				t.Errorf("/me/ml reads = %d, want read=%v", ml, tc.wantMLRead)
			}
			wantPurge := 0
			if tc.definitive {
				wantPurge = 1
			}
			if got := p.count(); got != wantPurge {
				t.Errorf("purges = %d, want %d", got, wantPurge)
			}
			if g.Recording() != tc.open {
				t.Errorf("Recording() = %v, want %v", g.Recording(), tc.open)
			}
		})
	}
}

// The first definitive true→false purges once; staying closed does not
// purge again; reopening re-arms it. A transient closure never purges.
func TestConsentGate_PurgeOncePerWithdrawal(t *testing.T) {
	t.Parallel()
	src := devConsent()
	var p purgeCounter
	g := NewConsentGate(GateConfig{Source: src, Purge: p.purge})
	ctx := context.Background()
	if !g.Refresh(ctx).Open {
		t.Fatal("gate not open")
	}
	src.set(func(f *fakeConsent) { f.effErr = errors.New("network") })
	if d := g.Refresh(ctx); d.Open || p.count() != 0 {
		t.Fatalf("transient read error: open=%v purges=%d, want closed with no purge", d.Open, p.count())
	}
	src.set(func(f *fakeConsent) { f.effErr = nil; f.eff = false })
	g.Refresh(ctx)
	g.Refresh(ctx)
	if p.count() != 1 {
		t.Fatalf("purges after withdrawal = %d, want exactly 1", p.count())
	}
	src.set(func(f *fakeConsent) { f.eff = true })
	g.Refresh(ctx)
	g.NotEffective(ctx) // a 403 ml_not_effective mid-stream
	if p.count() != 2 {
		t.Fatalf("purges after 403 ml_not_effective = %d, want 2", p.count())
	}
	if g.Recording() {
		t.Fatal("Recording() true after ml_not_effective")
	}
}

// Recording answers from the cache: no read per call, a re-read after the
// TTL or an Invalidate, and false (never a stale true) meanwhile.
func TestConsentGate_RecordingCacheTTLAndInvalidate(t *testing.T) {
	t.Parallel()
	src := devConsent()
	var mu sync.Mutex
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	g := NewConsentGate(GateConfig{Source: src, Now: clock})
	ctx := context.Background()
	g.Refresh(ctx)
	_, before := src.reads()
	for i := 0; i < 50; i++ {
		if !g.Recording() {
			t.Fatal("Recording() false on a fresh open decision")
		}
	}
	if _, after := src.reads(); after != before {
		t.Fatalf("Recording() read /me/ml %d times; it must answer from the cache", after-before)
	}
	advance(61 * time.Second)
	if g.Recording() {
		t.Fatal("Recording() true from a decision older than the 60 s TTL")
	}
	waitFor(t, func() bool { _, n := src.reads(); return n > before }, "background re-read after TTL")
	waitFor(t, g.Recording, "gate reopens after the background re-read")

	src.set(func(f *fakeConsent) { f.eff = false })
	g.Invalidate()
	if g.Recording() {
		// The invalidation is visible immediately: no stale true.
		t.Fatal("Recording() true right after Invalidate")
	}
	waitFor(t, func() bool { return g.Last().Reason == ReasonNotEffective }, "re-read after invalidate")
}

func TestConsentGate_OnChangeAndRefresherLifecycle(t *testing.T) {
	t.Parallel()
	src := devConsent()
	var mu sync.Mutex
	var changes []Decision
	g := NewConsentGate(GateConfig{Source: src, TTL: 60 * time.Millisecond, OnChange: func(d Decision) {
		mu.Lock()
		changes = append(changes, d)
		mu.Unlock()
	}})
	ctx := context.Background()
	g.Start(ctx)
	g.Start(ctx) // idempotent
	waitFor(t, func() bool { _, n := src.reads(); return n >= 3 }, "refresher ticks")
	g.Stop()
	_, n := src.reads()
	time.Sleep(150 * time.Millisecond)
	if _, m := src.reads(); m != n {
		t.Fatalf("refresher still reading after Stop (%d → %d)", n, m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) != 1 || !changes[0].Open {
		t.Fatalf("OnChange = %+v, want exactly one (open) change across repeated identical reads", changes)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
