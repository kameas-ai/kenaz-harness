package agentgraph

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestEventBatch_AppendKind(t *testing.T) {
	t.Parallel()
	var b EventBatch
	if err := b.AppendKind("run-1", "node-a", EventLLMCall, map[string]any{"model": "x"}); err != nil {
		t.Fatalf("AppendKind: %v", err)
	}
	if b.Len() != 1 {
		t.Fatalf("Len = %d, want 1", b.Len())
	}
	got := b.Events[0]
	if got.RunID != "run-1" || got.NodeID != "node-a" || got.Kind != EventLLMCall {
		t.Errorf("event mismatch: %+v", got)
	}
	var pl map[string]any
	if err := json.Unmarshal(got.Payload, &pl); err != nil {
		t.Fatalf("payload parse: %v", err)
	}
	if pl["model"] != "x" {
		t.Errorf("payload model = %v", pl["model"])
	}
}

func TestEventBatch_NilPayload(t *testing.T) {
	t.Parallel()
	var b EventBatch
	if err := b.AppendKind("run", "n", EventNodeStart, nil); err != nil {
		t.Fatalf("AppendKind: %v", err)
	}
	if len(b.Events[0].Payload) != 0 {
		t.Errorf("expected nil payload, got %s", b.Events[0].Payload)
	}
}

func TestMemEventLog_AppendAssignsSeq(t *testing.T) {
	t.Parallel()
	log := NewMemoryEventLog()
	var b EventBatch
	for i := 0; i < 3; i++ {
		_ = b.AppendKind("run", "n", EventLLMCall, map[string]any{"i": i})
	}
	last, err := log.Append(b)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if last != 3 {
		t.Errorf("last seq = %d, want 3", last)
	}
	if log.Len("run") != 3 {
		t.Errorf("Len = %d, want 3", log.Len("run"))
	}
}

func TestMemEventLog_ReplayInOrder(t *testing.T) {
	t.Parallel()
	log := NewMemoryEventLog()
	var b EventBatch
	for i := 0; i < 5; i++ {
		_ = b.AppendKind("r", "n", EventLLMCall, map[string]any{"i": i})
	}
	if _, err := log.Append(b); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var seqs []int64
	if err := log.Replay("r", func(e Event) error {
		seqs = append(seqs, e.Seq)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for i, s := range seqs {
		if s != int64(i+1) {
			t.Errorf("Seq[%d] = %d, want %d", i, s, i+1)
		}
	}
}

func TestMemEventLog_RejectsMissingRunID(t *testing.T) {
	t.Parallel()
	log := NewMemoryEventLog()
	var b EventBatch
	b.Append(Event{Kind: EventNodeStart})
	if _, err := log.Append(b); err == nil {
		t.Fatalf("expected error for missing run_id")
	}
}

func TestMemEventLog_PerRunSeqIsolation(t *testing.T) {
	t.Parallel()
	log := NewMemoryEventLog()
	var b EventBatch
	_ = b.AppendKind("a", "x", EventLLMCall, nil)
	_ = b.AppendKind("b", "x", EventLLMCall, nil)
	_ = b.AppendKind("a", "y", EventLLMCall, nil)
	if _, err := log.Append(b); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if log.Len("a") != 2 || log.Len("b") != 1 {
		t.Errorf("a=%d, b=%d", log.Len("a"), log.Len("b"))
	}
}

func TestAllEventKinds_CompleteAndUnique(t *testing.T) {
	t.Parallel()
	seen := map[EventKind]bool{}
	for _, k := range AllEventKinds() {
		if seen[k] {
			t.Errorf("duplicate EventKind %q", k)
		}
		seen[k] = true
		if k == "" {
			t.Error("empty EventKind in AllEventKinds")
		}
	}
}

func TestMemEventLog_ConcurrentAppend(t *testing.T) {
	t.Parallel()
	log := NewMemoryEventLog()
	var wg sync.WaitGroup
	const writers = 4
	const perWriter = 25
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				var b EventBatch
				_ = b.AppendKind("r", "n", EventLLMCall, map[string]any{"i": i})
				if _, err := log.Append(b); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if log.Len("r") != writers*perWriter {
		t.Errorf("Len = %d, want %d", log.Len("r"), writers*perWriter)
	}
}
