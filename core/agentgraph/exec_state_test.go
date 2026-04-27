package agentgraph

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryExecutor_Write(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", Memory: mem}
	applyEnvDefaults(env)
	ex := memoryExecutor{}
	node := &Node{ID: "m", Kind: NodeKindMemory, Attrs: MemoryAttrs{
		Mode: "write", Scope: "session", Content: "hello",
	}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["chunk_id"] == "" {
		t.Error("expected non-empty chunk_id")
	}
	if mem.writeCount() != 1 {
		t.Errorf("write count = %d", mem.writeCount())
	}
}

func TestMemoryExecutor_DedupOnSecondWrite(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", Memory: mem}
	applyEnvDefaults(env)
	ex := memoryExecutor{}
	node := &Node{ID: "m", Kind: NodeKindMemory, Attrs: MemoryAttrs{
		Mode: "write", Scope: "session", Content: "same content",
	}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if r.Outputs["deduped"] != true {
		t.Errorf("expected deduped=true, got %v", r.Outputs["deduped"])
	}
}

func TestMemoryExecutor_Read(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	mem.hits = []MemoryHit{{ID: "x", Content: "y", Similarity: 0.9}}
	env := &Env{RunID: "r", Memory: mem}
	applyEnvDefaults(env)
	ex := memoryExecutor{}
	node := &Node{ID: "m", Kind: NodeKindMemory, Attrs: MemoryAttrs{
		Mode: "read", Query: "anything", TopK: 3,
	}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["count"] != 1 {
		t.Errorf("count = %v", r.Outputs["count"])
	}
}

func TestMemoryExecutor_BadMode(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", Memory: newStubMemory()}
	applyEnvDefaults(env)
	ex := memoryExecutor{}
	node := &Node{ID: "m", Kind: NodeKindMemory, Attrs: MemoryAttrs{Mode: "blah"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestCorpusReadExecutor_StubReturnsEmpty(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := corpusReadExecutor{}
	node := &Node{ID: "c", Kind: NodeKindCorpusRead, Attrs: CorpusReadAttrs{
		CorpusIDs: []string{"x"},
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"query": "test"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["count"] != 0 {
		t.Errorf("count = %v", r.Outputs["count"])
	}
}

func TestCorpusWriteExecutor_StubReturnsEmpty(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := corpusWriteExecutor{}
	node := &Node{ID: "c", Kind: NodeKindCorpusWrite, Attrs: CorpusWriteAttrs{
		CorpusID: "x", SourcePath: "y",
	}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// stubAttachments is a tiny AttachmentResolver fake.
type stubAttachments struct {
	block AttachmentBlock
	err   error
}

func (s stubAttachments) Resolve(_ context.Context, _ string) (AttachmentBlock, error) {
	if s.err != nil {
		return AttachmentBlock{}, s.err
	}
	return s.block, nil
}

func TestAttachmentExecutor_Resolves(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", Attachments: stubAttachments{
		block: AttachmentBlock{MIME: "image/png", Title: "diagram"},
	}}
	applyEnvDefaults(env)
	ex := attachmentExecutor{}
	node := &Node{ID: "a", Kind: NodeKindAttachment, Attrs: AttachmentAttrs{AttachmentID: "id-1"}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	block := r.Outputs["block"].(AttachmentBlock)
	if block.MIME != "image/png" {
		t.Errorf("mime = %s", block.MIME)
	}
}

func TestAttachmentExecutor_PropagatesError(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", Attachments: stubAttachments{err: errors.New("nope")}}
	applyEnvDefaults(env)
	ex := attachmentExecutor{}
	node := &Node{ID: "a", Kind: NodeKindAttachment, Attrs: AttachmentAttrs{AttachmentID: "x"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestHistoryReadExecutor_PassesThrough(t *testing.T) {
	t.Parallel()
	env := &Env{
		RunID:     "r",
		SessionID: "s",
		History: HistoryReaderFunc(func(_ context.Context, sid string, n int) ([]Message, error) {
			if sid != "s" || n != 2 {
				return nil, errors.New("bad args")
			}
			return []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}, nil
		}),
	}
	applyEnvDefaults(env)
	ex := historyReadExecutor{}
	node := &Node{ID: "h", Kind: NodeKindHistoryRead, Attrs: HistoryReadAttrs{N: 2}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msgs := r.Outputs["messages"].([]Message)
	if len(msgs) != 2 {
		t.Errorf("len = %d", len(msgs))
	}
}

func TestTraceWriteExecutor_EmitsEvent(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := traceWriteExecutor{}
	node := &Node{ID: "t", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{
		Severity: "warn", Message: "watch out",
	}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var saw bool
	for _, e := range r.Events.Events {
		if e.Kind == EventTraceWrite {
			saw = true
		}
	}
	if !saw {
		t.Error("missing trace_write event")
	}
}

func TestCheckpointExecutor_FiresHook(t *testing.T) {
	t.Parallel()
	mem := newStubMemory()
	env := &Env{RunID: "r", Memory: mem, SessionID: "s"}
	applyEnvDefaults(env)
	ex := checkpointExecutor{}
	node := &Node{ID: "c", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "save here"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if mem.writeCount() != 1 {
		t.Errorf("checkpoint did not fire memory hook (writes=%d)", mem.writeCount())
	}
}
