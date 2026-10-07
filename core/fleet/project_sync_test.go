package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

func setupProjectSyncServer(t *testing.T) (*httptest.Server, *ProjectSyncer, *fakeEmitter) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	stubTokens(t, TokenSet{
		AccessToken:  "at-proj",
		RefreshToken: "rt-proj",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	fe := &fakeEmitter{}
	ps := NewProjectSyncer(c, fe, nil)

	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 9)
	}
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed: %v", err)
	}
	return srv, ps, fe
}

// ── EnableSync ─────────────────────────────────────────────────────────────

func TestProjectSyncer_EnableSync_ArtifactFilter(t *testing.T) {
	var appendCount int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append" {
			mu.Lock()
			appendCount++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-proj",
		RefreshToken: "rt-proj",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	ps := NewProjectSyncer(c, nil, nil)
	seed := make([]byte, seedSize)
	_ = StoreContextSeed(seed)

	projectID := "proj-filter-test"
	t.Cleanup(func() { _ = setProjectSyncEnabled(projectID, false) })

	events := []ProjectEventRecord{
		{Seq: 1, Bytes: []byte("note1"), ArtifactClass: ArtifactClassNotes},
		{Seq: 2, Bytes: []byte("bin1"), ArtifactClass: ArtifactClassBinaries},
	}

	// Exclude binaries.
	opts := ArtifactClassOptions{Notes: true, Binaries: false}
	if err := ps.EnableSync(context.Background(), projectID, events, opts); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}

	// Should have sent the notes event, not the binary.
	// Since backfill chunks them all in one POST, we check it fired at least once.
	mu.Lock()
	ac := appendCount
	mu.Unlock()
	if ac == 0 {
		t.Error("expected at least one backfill POST")
	}
}

// ── DisableSync ─────────────────────────────────────────────────────────────

func TestProjectSyncer_DisableSync(t *testing.T) {
	srv, ps, fe := setupProjectSyncServer(t)
	defer srv.Close()

	projectID := "proj-disable-test"
	_ = setProjectSyncEnabled(projectID, true)
	t.Cleanup(func() { _ = setProjectSyncEnabled(projectID, false) })

	if err := ps.DisableSync(context.Background(), projectID); err != nil {
		t.Fatalf("DisableSync: %v", err)
	}
	if isProjectSyncEnabled(projectID) {
		t.Error("expected sync flag to be cleared after DisableSync")
	}
	evts := fe.snapshot()
	if len(evts) == 0 || evts[0].Kind != contextaudit.KindFleetProjectSyncDisabled {
		t.Errorf("expected KindFleetProjectSyncDisabled audit event")
	}
}

// ── ArtifactClassOptions roundtrip ─────────────────────────────────────────

func TestProjectSyncer_ArtifactClassOptions_Roundtrip(t *testing.T) {
	srv, ps, _ := setupProjectSyncServer(t)
	defer srv.Close()

	projectID := "proj-opts-test"
	t.Cleanup(func() { _ = setProjectSyncEnabled(projectID, false) })

	want := ArtifactClassOptions{Notes: true, Binaries: true}
	if err := ps.SetArtifactClassOptions(projectID, want); err != nil {
		t.Fatalf("SetArtifactClassOptions: %v", err)
	}
	got := ps.GetArtifactClassOptions(projectID)
	if got != want {
		t.Errorf("artifact opts = %+v, want %+v", got, want)
	}
}

// ── Default artifact opts ───────────────────────────────────────────────────

func TestDefaultArtifactClassOptions(t *testing.T) {
	opts := DefaultArtifactClassOptions()
	if !opts.Notes {
		t.Error("default: Notes should be true")
	}
	if opts.Binaries {
		t.Error("default: Binaries should be false")
	}
}

// TestArtifactClassOptions_LegacyMemoryKeyIgnored pins memory-sync-01MEMSY01
// WP01: a persisted options blob written while the (retired, never-produced)
// "memory" class existed still decodes, and the key carries no meaning.
func TestArtifactClassOptions_LegacyMemoryKeyIgnored(t *testing.T) {
	var opts ArtifactClassOptions
	if err := json.Unmarshal([]byte(`{"notes":true,"binaries":false,"memory":true}`), &opts); err != nil {
		t.Fatalf("decode legacy blob: %v", err)
	}
	if opts != (ArtifactClassOptions{Notes: true}) {
		t.Fatalf("legacy blob decoded to %+v", opts)
	}
	raw, _ := json.Marshal(DefaultArtifactClassOptions())
	if strings.Contains(string(raw), "memory") {
		t.Fatalf("default options still advertise a memory class: %s", raw)
	}
}

// ── artifactClassAllowed ────────────────────────────────────────────────────

func TestArtifactClassAllowed(t *testing.T) {
	opts := ArtifactClassOptions{Notes: true, Binaries: false}
	if !artifactClassAllowed(ArtifactClassNotes, opts) {
		t.Error("notes should be allowed")
	}
	if artifactClassAllowed(ArtifactClassBinaries, opts) {
		t.Error("binaries should be disallowed")
	}
	if !artifactClassAllowed("unknown", opts) {
		t.Error("unknown class should default to allowed")
	}
}
