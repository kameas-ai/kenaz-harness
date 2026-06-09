package fleet

import (
	"encoding/json"
	"errors"
	"testing"

	contextpack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// TestLayerMapping_ClassificationForLayer verifies the layer→classification mapping.
func TestLayerMapping_ClassificationForLayer(t *testing.T) {
	tests := []struct {
		layer   contextpack.Layer
		wantCls ContextClassification
		wantOK  bool
	}{
		{contextpack.LayerTeam, ClassTeamShared, true},
		{contextpack.LayerOrg, ClassOrgShared, true},
		{contextpack.LayerPersonal, "", false},
		{"unknown", "", false},
	}
	for _, tc := range tests {
		cls, ok := ClassificationForLayer(tc.layer)
		if ok != tc.wantOK {
			t.Errorf("ClassificationForLayer(%q): ok=%v, want %v", tc.layer, ok, tc.wantOK)
		}
		if cls != tc.wantCls {
			t.Errorf("ClassificationForLayer(%q): cls=%q, want %q", tc.layer, cls, tc.wantCls)
		}
	}
}

// TestLayerMapping_LayerForClassification verifies the classification→layer mapping.
func TestLayerMapping_LayerForClassification(t *testing.T) {
	tests := []struct {
		cls       ContextClassification
		wantLayer contextpack.Layer
		wantOK    bool
	}{
		{ClassTeamShared, contextpack.LayerTeam, true},
		{ClassOrgShared, contextpack.LayerOrg, true},
		{"personal", "", false},
		{"unknown", "", false},
	}
	for _, tc := range tests {
		layer, ok := LayerForClassification(tc.cls)
		if ok != tc.wantOK {
			t.Errorf("LayerForClassification(%q): ok=%v, want %v", tc.cls, ok, tc.wantOK)
		}
		if layer != tc.wantLayer {
			t.Errorf("LayerForClassification(%q): layer=%q, want %q", tc.cls, layer, tc.wantLayer)
		}
	}
}

// TestPushPersonalLayerRejected verifies that pushing a personal-layer entry
// returns ErrPersonalLayerNotSyncable without contacting the network.
func TestPushPersonalLayerRejected(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	entry := ContextNodeEntry{
		ID:    "test-id",
		Layer: contextpack.LayerPersonal,
		Kind:  "guidance",
		Title: "My secret context",
		Body:  "sensitive data",
	}
	_, err := s.PushEntry(t.Context(), entry, nil)
	if !errors.Is(err, ErrPersonalLayerNotSyncable) {
		t.Errorf("PushEntry(personal): want ErrPersonalLayerNotSyncable, got %v", err)
	}
}

// TestPushCapabilityGate_NilCaps verifies that pushing without a capability
// poller returns ErrCapabilityNotInTier.
func TestPushCapabilityGate_NilCaps(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	entry := ContextNodeEntry{
		ID:    "team-entry-1",
		Layer: contextpack.LayerTeam,
		Kind:  "guidance",
		Title: "Team context",
		Body:  "shared body",
	}
	_, err := s.PushEntry(t.Context(), entry, nil)
	if err == nil {
		t.Fatal("expected capability error when no poller wired, got nil")
	}
	if !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("expected ErrCapabilityNotInTier, got %v", err)
	}
}

// TestContextPushResult_ConflictUnmarshal checks that the wire response
// for version conflicts parses correctly.
func TestContextPushResult_ConflictUnmarshal(t *testing.T) {
	raw := `{"accepted_nodes":0,"accepted_edges":0,"conflicts":[{"node_id":"abc","server_version":5,"client_version":3}]}`
	var r ContextPushResult
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(r.Conflicts) != 1 {
		t.Fatalf("len(Conflicts)=%d, want 1", len(r.Conflicts))
	}
	if r.Conflicts[0].NodeID != "abc" {
		t.Errorf("NodeID=%q, want abc", r.Conflicts[0].NodeID)
	}
	if r.Conflicts[0].ServerVersion != 5 {
		t.Errorf("ServerVersion=%d, want 5", r.Conflicts[0].ServerVersion)
	}
}

// TestCursorPersistence verifies that the cursor survives save/load round-trip.
func TestCursorPersistence(t *testing.T) {
	dir := t.TempDir()
	want := "2026-06-08T10:00:00.000000001Z"
	if err := saveContextCursor(dir, want); err != nil {
		t.Fatalf("save cursor: %v", err)
	}
	got, err := loadContextCursor(dir)
	if err != nil {
		t.Fatalf("load cursor: %v", err)
	}
	if got != want {
		t.Errorf("cursor=%q, want %q", got, want)
	}
}

// TestContextSyncerStatus_Default verifies a freshly-created syncer
// returns sane defaults.
func TestContextSyncerStatus_Default(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	st := s.Status()
	if st.PullCount != 0 {
		t.Errorf("PullCount=%d, want 0", st.PullCount)
	}
	if st.TeamCapEnabled {
		t.Error("TeamCapEnabled=true, want false for nil caps")
	}
	if st.Cursor != "" {
		t.Errorf("Cursor=%q, want empty for fresh syncer", st.Cursor)
	}
}

// TestUpsertPulledLocked verifies the in-memory merge of pulled entries.
func TestUpsertPulledLocked(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	e1 := ContextNodeEntry{ID: "id1", Title: "first", Version: 1}
	e2 := ContextNodeEntry{ID: "id1", Title: "updated", Version: 2}

	s.mu.Lock()
	s.upsertPulledLocked(e1)
	s.upsertPulledLocked(e2) // should replace, not append
	s.mu.Unlock()

	entries := s.PulledEntries()
	if len(entries) != 1 {
		t.Fatalf("PulledEntries len=%d, want 1", len(entries))
	}
	if entries[0].Title != "updated" {
		t.Errorf("Title=%q, want updated", entries[0].Title)
	}
}

// TestPulledEntries_TombstonesFiltered verifies that tombstoned entries
// are excluded from PulledEntries().
func TestPulledEntries_TombstonesFiltered(t *testing.T) {
	s := NewContextGraphSyncer(nil, t.TempDir(), nil)
	deletedAt := "2026-06-08T00:00:00Z"
	s.mu.Lock()
	s.upsertPulledLocked(ContextNodeEntry{ID: "alive", Title: "live"})
	s.upsertPulledLocked(ContextNodeEntry{ID: "dead", Title: "gone", DeletedAt: &deletedAt})
	s.mu.Unlock()

	entries := s.PulledEntries()
	if len(entries) != 1 {
		t.Fatalf("PulledEntries len=%d, want 1 (tombstone excluded)", len(entries))
	}
	if entries[0].ID != "alive" {
		t.Errorf("ID=%q, want alive", entries[0].ID)
	}
}
