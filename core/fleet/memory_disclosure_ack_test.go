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
// with the revision and a timestamp; it changes nothing about sync and sends
// nothing to Fleet; an account switch shows each account its own answer.
func TestMemoryDisclosureAck_PerAccountAndLocalOnly(t *testing.T) {
	ctx := context.Background()
	w := newMemWorld(t)
	w.fleet.enabled = true
	d := w.device("node-ack", 0)

	d.signIn(accountA)
	turnedOnUnderOldText(t, d)
	if st := d.ms.Status(ctx); !st.LocalEnabled || st.DisclosureAcked {
		t.Fatalf("A turned sync on under the old text: want enabled and unacknowledged, got %+v", st)
	}

	puts, reqs := w.fleet.putsAndRequests()
	if err := d.ms.DismissNotice(ctx, MemoryNoticeDisclosureUpdate); err != nil {
		t.Fatal(err)
	}
	if p, r := w.fleet.putsAndRequests(); p != puts || r != reqs {
		t.Fatalf("acknowledging sent something to Fleet: puts %d->%d, requests %d->%d", puts, p, reqs, r)
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

// The consent version Fleet receives must not move with the disclosure
// revision: Fleet keeps one overwritten consent_version column, and a new date
// could overwrite a member's stored memory-sync/v4 (Fleet, 2026-11-15).
func TestMemoryConsentVersion_Unchanged(t *testing.T) {
	if MemoryConsentVersion != "2026-10-07" {
		t.Fatalf("MemoryConsentVersion = %q; it must stay 2026-10-07 until Fleet keeps a consent history", MemoryConsentVersion)
	}
	if MemoryDisclosureRevision == MemoryConsentVersion {
		t.Fatal("the local disclosure revision must be distinct from the consent version sent to Fleet")
	}
}
