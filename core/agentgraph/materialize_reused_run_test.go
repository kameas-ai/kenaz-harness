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
