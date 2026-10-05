package agentgraph

import (
	"errors"
	"testing"
)

// agentgraph-settings-linkage-01DOGF0D WP02: a pre-fix chat run id
// ("chat-<n>" from a per-process counter) can carry several unrelated
// turns in the persistent SQL log — one run_start per boot that reused
// it. Materializing such an id must refuse rather than project the
// turns as one graph. Driven on the SQL log: on NewMemoryEventLog the
// reuse cannot happen across a restart, so the defect is invisible there.
// openTestDB's DDL is the hand-mirrored 0309 schema (core/agentgraph
// cannot import core/session); the chat-package pins use the registered
// migration's own UpSource.
func TestMaterializeRun_RefusesRunIDReusedAcrossRestarts(t *testing.T) {
	t.Parallel()
	log := NewSQLEventLog(openTestDB(t))
	const legacyID = "chat-3"
	for boot := 0; boot < 2; boot++ {
		var b EventBatch
		_ = b.AppendKind(legacyID, "", EventRunStart, map[string]any{"graph_id": "chat_default"})
		_ = b.AppendKind(legacyID, "n1", EventNodeStart, map[string]any{"kind": "artifact"})
		_ = b.AppendKind(legacyID, "n1", EventNodeComplete, map[string]any{})
		if _, err := log.Append(b); err != nil {
			t.Fatalf("Append boot %d: %v", boot, err)
		}
	}
	g := Graph{SpecVersion: "1", ID: "chat_default", Entrypoints: []string{"n1"}, Nodes: []Node{{ID: "n1", Kind: NodeKindArtifact}}}
	_, err := MaterializeRun(g, legacyID, log)
	if !errors.Is(err, ErrRunIDReused) {
		t.Fatalf("MaterializeRun(reused id) err = %v, want ErrRunIDReused", err)
	}
}

// Delta review NEW-1: only TRUE reuse is refused. A ULID-id run with two
// run_starts and no completion in between is a resume or an overflow
// redrive — one run continuing — and must materialize; a run_start AFTER
// the id's run completed is reuse whatever the id shape.
func TestMaterializeRun_RefusesOnlyTrueReuse(t *testing.T) {
	t.Parallel()
	g := Graph{SpecVersion: "1", ID: "g", Entrypoints: []string{"n1"}, Nodes: []Node{{ID: "n1", Kind: NodeKindArtifact}}}
	write := func(log EventLog, id string, completeBetween bool) {
		var b EventBatch
		_ = b.AppendKind(id, "", EventRunStart, map[string]any{"graph_id": "g"})
		_ = b.AppendKind(id, "n1", EventNodeStart, map[string]any{"kind": "artifact"})
		_ = b.AppendKind(id, "n1", EventNodeError, map[string]any{"err": "overflow"})
		if completeBetween {
			_ = b.AppendKind(id, "", EventRunComplete, map[string]any{})
		}
		_ = b.AppendKind(id, "", EventRunStart, map[string]any{"graph_id": "g"})
		_ = b.AppendKind(id, "n1", EventNodeStart, map[string]any{"kind": "artifact"})
		_ = b.AppendKind(id, "n1", EventNodeComplete, map[string]any{})
		_ = b.AppendKind(id, "", EventRunComplete, map[string]any{})
		if _, err := log.Append(b); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	log := NewSQLEventLog(openTestDB(t))
	const cont = "chat-01J000000000000000CONTINUE"
	write(log, cont, false)
	if _, err := MaterializeRun(g, cont, log); err != nil {
		t.Errorf("continuation (two starts, no completion between) refused: %v", err)
	}
	const reused = "chat-01J0000000000000000REUSED"
	write(log, reused, true)
	if _, err := MaterializeRun(g, reused, log); !errors.Is(err, ErrRunIDReused) {
		t.Errorf("start after completion: err = %v, want ErrRunIDReused", err)
	}
	for id, want := range map[string]bool{"chat-3": true, "chat-12": true, "chat-": false, cont: false, "run-3": false} {
		if got := IsLegacyChatRunID(id); got != want {
			t.Errorf("IsLegacyChatRunID(%q) = %v, want %v", id, got, want)
		}
	}
}
