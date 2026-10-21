package fleet

// context_pull_tombstone_test.go — a pulled tombstone removes the local copy
// in the Curated lane.
//
// Fleet serves a tombstone (deleted_at set) for an FR-103 delete and, since
// kenaz-fleet #325, for a node quarantined because its content may contain a
// secret: every member but the author pulls it with an EMPTY title, body and
// metadata, deleted_at = quarantined_at, and the version UNCHANGED. The author
// pulls the full node with quarantined_at / quarantine_reason and keeps it. A
// lint-passing author edit clears the quarantine and the node comes back to
// members as a live node at a higher version.
//
// Before this fix the poll loop handed the library merger PulledEntries(),
// which hides tombstones, so Library.MergeFleetEntries never saw one and the
// old body stayed in the library's fleet layer. These tests drive the real
// poll iteration (pullAndMerge) into a real contexts.Library.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
)

// libraryMergerFor mirrors the production merger wired in core/rpc/api.go
// (ContextNodeEntry → contexts.FleetEntry → Library.MergeFleetEntries).
func libraryMergerFor(lib *contexts.Library) func([]ContextNodeEntry) {
	return func(entries []ContextNodeEntry) {
		converted := make([]contexts.FleetEntry, 0, len(entries))
		for _, e := range entries {
			converted = append(converted, contexts.FleetEntry{
				ID: e.ID, Layer: e.Layer, Kind: e.Kind, Title: e.Title, Body: e.Body,
				Metadata: e.Metadata, Version: e.Version, DeletedAt: e.DeletedAt,
			})
		}
		lib.MergeFleetEntries(converted)
	}
}

func libraryEntry(lib *contexts.Library, id string) (contexts.FleetEntry, bool) {
	for _, e := range lib.FleetEntries() {
		if e.ID == id {
			return e, true
		}
	}
	return contexts.FleetEntry{}, false
}

func ts(offset time.Duration) string {
	return time.Now().Add(offset).UTC().Format(time.RFC3339Nano)
}

// curatedTombstoneHarness wires a syncer against a fake Fleet serving the
// given pull pages in order, with the production-shaped merger into a real
// Library.
func curatedTombstoneHarness(t *testing.T, pages ...contextPullResponse) (*ContextGraphSyncer, *contexts.Library) {
	t.Helper()
	fake := &contextFakeServer{}
	for _, p := range pages {
		fake.addPullResponse(p)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-curated-tomb", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	syncer := NewContextGraphSyncer(makeTestClient(t, srv.URL), t.TempDir(), makeCapPollerWithTeamCap(t))
	lib, err := contexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("contexts.Open: %v", err)
	}
	syncer.SetLibraryMerger(libraryMergerFor(lib))
	return syncer, lib
}

func mustPullAndMerge(t *testing.T, s *ContextGraphSyncer) {
	t.Helper()
	if _, err := s.pullAndMerge(context.Background()); err != nil {
		t.Fatalf("pullAndMerge: %v", err)
	}
}

// assertNoPulledContent fails if any pulled-cache entry for id still holds
// a title, body or metadata.
func assertNoPulledContent(t *testing.T, s *ContextGraphSyncer, id string) {
	t.Helper()
	for _, e := range s.pulledEntriesForMerger() {
		if e.ID == id && (e.Title != "" || e.Body != "" || len(e.Metadata) != 0) {
			t.Errorf("pulled cache still holds content for tombstoned %s", id)
		}
	}
	for _, e := range s.PulledEntries() {
		if e.ID == id {
			t.Errorf("PulledEntries still surfaces tombstoned %s", id)
		}
	}
}

// FR-103 delete: live node, then its tombstone → gone from the library.
func TestCuratedPull_DeleteTombstoneRemovesLibraryCopy(t *testing.T) {
	del := ts(0)
	syncer, lib := curatedTombstoneHarness(t,
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-del", Kind: "guidance", Title: "Deploy rules", Body: "old body",
			Classification: ClassTeamShared, Version: 2, UpdatedAt: ts(-time.Minute),
		}}, Cursor: ts(-time.Minute)},
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-del", Kind: "guidance", Title: "Deploy rules", Body: "old body",
			Classification: ClassTeamShared, Version: 3, UpdatedAt: del, DeletedAt: &del,
		}}, Cursor: del},
	)
	mustPullAndMerge(t, syncer)
	if _, ok := libraryEntry(lib, "cur-del"); !ok {
		t.Fatal("live node did not reach the library")
	}
	mustPullAndMerge(t, syncer)
	if _, ok := libraryEntry(lib, "cur-del"); ok {
		t.Error("deleted node still in the library after its tombstone was pulled")
	}
	// Even though this server echoed the old content on the tombstone,
	// none of it is retained in memory.
	assertNoPulledContent(t, syncer, "cur-del")
}

// Quarantine (kenaz-fleet #325 member shape): empty fields, deleted_at set,
// SAME version as the live copy → gone. Then the author's clean edit brings
// it back at a higher version with new content → restored.
func TestCuratedPull_QuarantineTombstoneThenCleanReappearance(t *testing.T) {
	quarantined := ts(0)
	syncer, lib := curatedTombstoneHarness(t,
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-q", Kind: "guidance", Title: "Ops notes", Body: "body that tripped the lint",
			Classification: ClassTeamShared, Version: 4, UpdatedAt: ts(-time.Minute),
		}}, Cursor: ts(-time.Minute)},
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-q", Kind: "guidance", Title: "", Body: "", Metadata: json.RawMessage(`{}`),
			Classification: ClassTeamShared, Version: 4, UpdatedAt: quarantined, DeletedAt: &quarantined,
		}}, Cursor: quarantined},
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-q", Kind: "guidance", Title: "Ops notes", Body: "clean body",
			Classification: ClassTeamShared, Version: 5, UpdatedAt: ts(time.Minute),
		}}, Cursor: ts(time.Minute)},
	)
	mustPullAndMerge(t, syncer)
	if _, ok := libraryEntry(lib, "cur-q"); !ok {
		t.Fatal("live node did not reach the library")
	}

	mustPullAndMerge(t, syncer)
	if _, ok := libraryEntry(lib, "cur-q"); ok {
		t.Error("quarantined node still in the library (same-version tombstone ignored)")
	}
	assertNoPulledContent(t, syncer, "cur-q")

	mustPullAndMerge(t, syncer)
	got, ok := libraryEntry(lib, "cur-q")
	if !ok {
		t.Fatal("clean re-appearance did not restore the node")
	}
	if got.Body != "clean body" || got.Version != 5 {
		t.Errorf("restored entry = body %q v%d; want the clean body at v5", got.Body, got.Version)
	}
	if len(syncer.PulledEntries()) != 1 {
		t.Errorf("PulledEntries = %d entries, want the restored node", len(syncer.PulledEntries()))
	}
}

// Author view: full content plus quarantined_at / quarantine_reason, no
// deleted_at → kept, in the library and in PulledEntries.
func TestCuratedPull_AuthorQuarantineViewIsKept(t *testing.T) {
	q := ts(0)
	syncer, lib := curatedTombstoneHarness(t,
		contextPullResponse{Nodes: []ContextPulledNode{{
			ID: "cur-author", Kind: "guidance", Title: "Mine", Body: "author body",
			Classification: ClassTeamShared, Version: 2, UpdatedAt: q,
			QuarantinedAt: &q, QuarantineReason: "secret_detected",
		}}, Cursor: q},
	)
	mustPullAndMerge(t, syncer)
	got, ok := libraryEntry(lib, "cur-author")
	if !ok || got.Body != "author body" {
		t.Errorf("author's quarantined node = %+v, %v; want kept with its body", got, ok)
	}
	if len(syncer.PulledEntries()) != 1 {
		t.Error("author's quarantined node missing from PulledEntries")
	}
}

// The exact #325 wire shapes decode: member tombstone (empty strings, {}
// metadata, deleted_at) and author view (quarantine fields).
func TestContextPulledNode_DecodesQuarantineShapes(t *testing.T) {
	member := `{"id":"n1","org_id":"o","owner_user_id":"u","kind":"guidance","title":"","body":"",` +
		`"metadata":{},"classification":"team_shared","scope":"org.x","version":4,` +
		`"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-20T00:00:00Z","deleted_at":"2026-10-20T00:00:00Z"}`
	var m ContextPulledNode
	if err := json.Unmarshal([]byte(member), &m); err != nil {
		t.Fatalf("member shape: %v", err)
	}
	if m.DeletedAt == nil || m.QuarantinedAt != nil || m.Version != 4 {
		t.Errorf("member shape decoded as %+v", m)
	}

	author := `{"id":"n1","org_id":"o","owner_user_id":"u","kind":"guidance","title":"t","body":"b",` +
		`"classification":"team_shared","version":4,"created_at":"2026-10-01T00:00:00Z",` +
		`"updated_at":"2026-10-20T00:00:00Z","quarantined_at":"2026-10-20T00:00:00Z","quarantine_reason":"secret_detected"}`
	var a ContextPulledNode
	if err := json.Unmarshal([]byte(author), &a); err != nil {
		t.Fatalf("author shape: %v", err)
	}
	if a.DeletedAt != nil || a.QuarantinedAt == nil || a.QuarantineReason != "secret_detected" {
		t.Errorf("author shape decoded as %+v", a)
	}
}
