package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// turnedOnUnderOldText leaves the signed-in account with sync on and no ack,
// which is what an install that enabled sync before v0.106.2 looks like.
func turnedOnUnderOldText(t *testing.T, d *memDevice) {
	t.Helper()
	d.enable()
	if err := d.ms.update(func(s *memSyncState) { s.DisclosureAck = nil }); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeMemoryFleet) putsAndRequests() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.putLog), f.requests
}

// The re-acknowledgement is recorded per account (org + user) on this device,
// with the revision and a timestamp; it changes nothing about sync; an account
// switch shows each account its own answer.
func TestMemoryDisclosureAck_PerAccount(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.device("node-ack", 0)

	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	if st := d.ms.Status(ctx); !st.LocalEnabled || st.DisclosureAcked {
		t.Fatalf("A turned sync on under the old text: want enabled and unacknowledged, got %+v", st)
	}

	puts, _ := w.fleet.putsAndRequests()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.fleet.putsAndRequests(); p != puts {
		t.Fatalf("acknowledging changed sync on Fleet: enable/disable PUTs %d->%d", puts, p)
	}
	if got := w.fleet.reackLog(); len(got) != 1 {
		t.Fatalf("re-acknowledgements sent = %d, want 1", len(got))
	}
	st := d.ms.state()
	if st.DisclosureAck == nil || st.DisclosureAck.Revision != MemoryDisclosureRevision ||
		!st.DisclosureAck.At.Equal(w.wall.now().UTC()) {
		t.Fatalf("ack = %+v, want revision %s at the wall clock", st.DisclosureAck, MemoryDisclosureRevision)
	}
	if !st.Enabled || st.ConsentVersion != MemoryConsentVersion {
		t.Fatalf("acknowledging changed sync state: %+v", st.memSyncAccountState)
	}
	if got := d.ms.Status(ctx); !got.DisclosureAcked || !got.LocalEnabled {
		t.Fatalf("after the ack: %+v", got)
	}

	// Persisted in the 0600 state file, under A's account state.
	info, err := os.Stat(MemorySyncStatePath(d.dir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(MemorySyncStatePath(d.dir))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Account       string               `json:"account"`
		DisclosureAck *MemoryDisclosureAck `json:"disclosure_ack"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Account != AccountKey(accountA) || onDisk.DisclosureAck == nil ||
		onDisk.DisclosureAck.Revision != MemoryDisclosureRevision {
		t.Fatalf("on disk: account %q ack %+v", onDisk.Account, onDisk.DisclosureAck)
	}

	// B on the same device does not inherit A's ack.
	d.signIn(accountB)
	if got := d.ms.Status(ctx); got.DisclosureAcked {
		t.Fatalf("B inherited A's acknowledgement: %+v", got)
	}
	turnedOnUnderOldText(t, d)
	if got := d.ms.Status(ctx); !got.LocalEnabled || got.DisclosureAcked {
		t.Fatalf("B: want enabled and unacknowledged, got %+v", got)
	}

	// A comes back: still acknowledged. B's answer is still B's.
	d.signIn(accountA)
	if got := d.ms.Status(ctx); !got.DisclosureAcked {
		t.Fatalf("A's acknowledgement was lost across the switch: %+v", got)
	}
	d.signIn(accountB)
	if got := d.ms.Status(ctx); got.DisclosureAcked {
		t.Fatalf("B picked up A's acknowledgement on return: %+v", got)
	}
}

// An ack for an earlier revision doesn't cover the current one.
func TestMemoryDisclosureAck_OlderRevisionIsNotEnough(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.device("node-ack-old", 0)
	d.signIn(accountA)
	d.enable()
	if err := d.ms.update(func(s *memSyncState) {
		s.DisclosureAck = &MemoryDisclosureAck{Revision: "2026-10-07", At: w.wall.now()}
	}); err != nil {
		t.Fatal(err)
	}
	if got := d.ms.Status(ctx); got.DisclosureAcked {
		t.Fatalf("an older revision's ack covered the current one: %+v", got)
	}
}

// Turning sync on after this release shows the corrected text in the off-state
// disclosure, so the ack is recorded then and the notice never shows.
func TestMemoryDisclosureAck_RecordedAtEnable(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	d := w.device("node-ack-enable", 0)
	d.signIn(accountA)
	if got := d.ms.Status(ctx); got.DisclosureAcked {
		t.Fatalf("acknowledged before anything was read: %+v", got)
	}
	d.enable()
	st := d.ms.state()
	if st.DisclosureAck == nil || st.DisclosureAck.Revision != MemoryDisclosureRevision || st.DisclosureAck.At.IsZero() {
		t.Fatalf("enable did not record the ack: %+v", st.DisclosureAck)
	}
	if st.ConsentVersion != MemoryConsentVersion {
		t.Fatalf("consent version sent = %q, want the unchanged %q", st.ConsentVersion, MemoryConsentVersion)
	}
	if got := d.ms.Status(ctx); !got.LocalEnabled || !got.DisclosureAcked {
		t.Fatalf("after enable: %+v", got)
	}
}

// With no verified account there is nobody to record it for.
func TestMemoryDisclosureAck_NeedsAnAccount(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("node-ack-none", 0)
	d.signIn(Identity{}) // tokens, but enroll hasn't answered
	if err := d.ms.DismissNotice(context.Background(), MemoryNoticeDisclosureUpdate); !errors.Is(err, errNoAccount) {
		t.Fatalf("err = %v, want errNoAccount", err)
	}
}

// The consent version the enable path sends stays put: Fleet asked for the
// enable path to be unchanged, and the corrected text travels as a
// re-acknowledgement instead (Fleet, 2026-11-15).
func TestMemoryConsentVersion_Unchanged(t *testing.T) {
	if MemoryConsentVersion != "2026-10-07" {
		t.Fatalf("MemoryConsentVersion = %q; the enable path must keep sending 2026-10-07", MemoryConsentVersion)
	}
	if MemoryDisclosureRevision == MemoryConsentVersion {
		t.Fatal("the disclosure revision must be distinct from the enable path's consent version")
	}
}

func (f *fakeMemoryFleet) reackLog() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.reacks...)
}

func (f *fakeMemoryFleet) storedConsent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.consent
}

// "I have read this" sends Fleet exactly {consent_version, reacknowledge}
// once, and Fleet stores the revision over the old date.
func TestMemoryDisclosureReack_SentToFleetExactlyOnce(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	d := w.device("node-reack", 0)
	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	if got := w.fleet.storedConsent(); got != MemoryConsentVersion {
		t.Fatalf("stored consent before = %q", got)
	}
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	got := w.fleet.reackLog()
	if len(got) != 1 {
		t.Fatalf("re-acknowledgements = %d, want 1", len(got))
	}
	if len(got[0]) != 2 || got[0]["consent_version"] != MemoryDisclosureRevision || got[0]["reacknowledge"] != true {
		t.Fatalf("body = %v, want exactly consent_version=%s and reacknowledge=true", got[0], MemoryDisclosureRevision)
	}
	if c := w.fleet.storedConsent(); c != MemoryDisclosureRevision {
		t.Fatalf("Fleet stored %q, want %q", c, MemoryDisclosureRevision)
	}
	if d.ms.state().DisclosureReackPending {
		t.Fatal("still pending after Fleet stored it")
	}
	// Later cycles and a repeat click send nothing more.
	d.sync()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	d.sync()
	if n := len(w.fleet.reackLog()); n != 1 {
		t.Fatalf("re-acknowledgements after repeat = %d, want 1", n)
	}
}

// A stored memory-sync/vN edition is never sent a date: nothing goes, and the
// local ack still counts.
func TestMemoryDisclosureReack_SkippedForEdition(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	d := w.device("node-reack-v4", 0)
	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	w.fleet.mu.Lock()
	w.fleet.consent = "memory-sync/v4"
	w.fleet.mu.Unlock()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	if n := len(w.fleet.reackLog()); n != 0 {
		t.Fatalf("re-acknowledged over a memory-sync edition (%d sent)", n)
	}
	if c := w.fleet.storedConsent(); c != "memory-sync/v4" {
		t.Fatalf("stored edition changed to %q", c)
	}
	if st := d.ms.state(); st.DisclosureReackPending || !d.ms.Status(ctx).DisclosureAcked {
		t.Fatalf("want acked locally and nothing pending: %+v", st.memSyncAccountState)
	}
}

// Sync off on Fleet (turned off on another device): nothing to re-acknowledge.
func TestMemoryDisclosureReack_SkippedWhenFleetSyncOff(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	d := w.device("node-reack-off", 0)
	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	w.fleet.mu.Lock()
	w.fleet.enabled = false
	w.fleet.mu.Unlock()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	if n := len(w.fleet.reackLog()); n != 0 {
		t.Fatalf("re-acknowledged while sync is off on Fleet (%d sent)", n)
	}
	if d.ms.state().DisclosureReackPending {
		t.Fatal("left pending with nothing to send")
	}
}

// A Fleet failure doesn't fail the click: the ack is recorded, stays pending,
// and the lane sends it on the next run.
func TestMemoryDisclosureReack_RetriedByTheLane(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	d := w.device("node-reack-retry", 0)
	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	w.fleet.mu.Lock()
	w.fleet.failPut = 3 // do() retries 5xx three times
	w.fleet.mu.Unlock()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatalf("a Fleet failure surfaced to the click: %v", err)
	}
	if st := d.ms.state(); !st.DisclosureReackPending || !d.ms.Status(ctx).DisclosureAcked {
		t.Fatalf("want acked locally and pending: %+v", st.memSyncAccountState)
	}
	if c := w.fleet.storedConsent(); c != MemoryConsentVersion {
		t.Fatalf("stored = %q before the retry", c)
	}
	d.sync()
	if c := w.fleet.storedConsent(); c != MemoryDisclosureRevision {
		t.Fatalf("the lane did not send the re-acknowledgement: stored %q", c)
	}
	if d.ms.state().DisclosureReackPending {
		t.Fatal("still pending after the lane sent it")
	}
}
