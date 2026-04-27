package agentgraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		CorpusIds: []string{"x"},
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
		CorpusId: "x", SourcePath: "y",
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
	node := &Node{ID: "a", Kind: NodeKindAttachment, Attrs: AttachmentAttrs{AttachmentId: "id-1"}}
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
	node := &Node{ID: "a", Kind: NodeKindAttachment, Attrs: AttachmentAttrs{AttachmentId: "x"}}
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

// stubPolicyGate is the test seam that drives every executor's Cedar
// gate path. Each Check* method consults the corresponding `deny*`
// field; nil = allow, non-nil = deny.
type stubPolicyGate struct {
	denyFileRead   error
	denyFileWrite  error
	denyStateRead  error
	denyStateWrite error
}

func (g stubPolicyGate) CheckFileRead(_ context.Context, _ string) error {
	return g.denyFileRead
}
func (g stubPolicyGate) CheckFileWrite(_ context.Context, _ string) error {
	return g.denyFileWrite
}
func (g stubPolicyGate) CheckStateRead(_ context.Context, _ string) error {
	return g.denyStateRead
}
func (g stubPolicyGate) CheckStateWrite(_ context.Context, _ string) error {
	return g.denyStateWrite
}

// stubBashOutput is a tiny BashOutputStore fake.
type stubBashOutput struct {
	mu      sync.Mutex
	entries map[string]BashOutput
}

func newStubBashOutput() *stubBashOutput {
	return &stubBashOutput{entries: map[string]BashOutput{}}
}

func (s *stubBashOutput) put(o BashOutput) {
	s.mu.Lock()
	s.entries[o.RunID] = o
	s.mu.Unlock()
}

func (s *stubBashOutput) Get(_ context.Context, runID string) (BashOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.entries[runID]
	if !ok {
		return BashOutput{}, errors.New("bash output: unknown run id " + runID)
	}
	return o, nil
}

// stubAttachmentRegistry records every Register call for the read_file
// as_attachment path test.
type stubAttachmentRegistry struct {
	mu       sync.Mutex
	id       string
	last     AttachmentBlock
	registered bool
}

func (s *stubAttachmentRegistry) Register(_ context.Context, b AttachmentBlock) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = b
	s.registered = true
	id := s.id
	if id == "" {
		id = "att-" + b.Title
	}
	return id, nil
}

func TestReadFileExecutor_Inline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", Memory: mem}
	applyEnvDefaults(env)
	ex := readFileExecutor{}
	node := &Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path}}
	res, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["result"]; got != "hello world" {
		t.Errorf("result = %v, want %q", got, "hello world")
	}
	if res.Outputs["sha256"] == "" {
		t.Error("missing sha256")
	}
	// Provenance event recorded.
	var saw bool
	var sha string
	for _, e := range res.Events.Events {
		if e.Kind == EventFileRead {
			saw = true
			if !strings.Contains(string(e.Payload), "sha256") {
				t.Errorf("file_read event missing sha256: %s", e.Payload)
			}
			if !strings.Contains(string(e.Payload), "mtime") {
				t.Errorf("file_read event missing mtime: %s", e.Payload)
			}
			sha = string(e.Payload)
		}
	}
	if !saw {
		t.Error("missing file_read event")
	}
	if sha == "" {
		t.Error("event payload empty")
	}
	// Greedy hook fires post-read.
	if mem.writeCount() != 1 {
		t.Errorf("expected 1 memory hook write, got %d", mem.writeCount())
	}
}

func TestReadFileExecutor_Base64(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := readFileExecutor{}
	node := &Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path, Encoding: "base64"}}
	res, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["result"]; got != "AAEC" {
		t.Errorf("base64 result = %v, want AAEC", got)
	}
}

func TestReadFileExecutor_AsAttachment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("attached content"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	reg := &stubAttachmentRegistry{id: "att-1"}
	env := &Env{RunID: "r", AttachmentRegistry: reg}
	applyEnvDefaults(env)
	ex := readFileExecutor{}
	node := &Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path, AsAttachment: true}}
	res, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["attachment_ref"]; got != "att-1" {
		t.Errorf("attachment_ref = %v, want att-1", got)
	}
	if !reg.registered {
		t.Error("AttachmentRegistrar.Register not called")
	}
	if reg.last.Title != "doc.txt" {
		t.Errorf("registered title = %q, want doc.txt", reg.last.Title)
	}
}

func TestReadFileExecutor_PolicyDenied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	denied := errors.New("policy denied")
	env := &Env{RunID: "r", Policy: stubPolicyGate{denyFileRead: denied}}
	applyEnvDefaults(env)
	ex := readFileExecutor{}
	node := &Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path}}
	if _, err := ex.Execute(context.Background(), env, node, nil); !errors.Is(err, denied) {
		t.Fatalf("expected policy-denied error, got %v", err)
	}
}

func TestReadBashOutputExecutor_HappyPath(t *testing.T) {
	t.Parallel()
	store := newStubBashOutput()
	store.put(BashOutput{
		RunID:    "run-1",
		Command:  "echo hi",
		Stdout:   "hi\n",
		Stderr:   "",
		ExitCode: 0,
	})
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", BashOutput: store, Memory: mem}
	applyEnvDefaults(env)
	ex := readBashOutputExecutor{}
	node := &Node{ID: "rb", Kind: NodeKindReadBashOutput, Attrs: ReadBashOutputAttrs{BashRunId: "run-1"}}
	res, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["result"]; got != "hi\n" {
		t.Errorf("result = %v, want %q", got, "hi\n")
	}
	if res.Outputs["exit_code"] != 0 {
		t.Errorf("exit_code = %v, want 0", res.Outputs["exit_code"])
	}
	var saw bool
	for _, e := range res.Events.Events {
		if e.Kind == EventBashOutputRead {
			saw = true
			if !strings.Contains(string(e.Payload), "run-1") {
				t.Errorf("event missing bash_run_id: %s", e.Payload)
			}
			if !strings.Contains(string(e.Payload), `"command":"echo hi"`) {
				t.Errorf("event missing command: %s", e.Payload)
			}
		}
	}
	if !saw {
		t.Error("missing bash_output_read event")
	}
	if mem.writeCount() != 1 {
		t.Errorf("expected 1 memory hook write, got %d", mem.writeCount())
	}
}

func TestReadBashOutputExecutor_TailBytes(t *testing.T) {
	t.Parallel()
	store := newStubBashOutput()
	store.put(BashOutput{RunID: "run-2", Stdout: "0123456789", ExitCode: 0})
	env := &Env{RunID: "r", BashOutput: store}
	applyEnvDefaults(env)
	ex := readBashOutputExecutor{}
	node := &Node{ID: "rb", Kind: NodeKindReadBashOutput, Attrs: ReadBashOutputAttrs{BashRunId: "run-2", TailBytes: 4}}
	res, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["result"]; got != "6789" {
		t.Errorf("tail result = %v, want 6789", got)
	}
}

func TestReadBashOutputExecutor_NoStore(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	// Override the default stub with a nil store to exercise the
	// "no store wired" path; applyEnvDefaults installed nilBashOutput
	// which returns ErrNoBashOutput on Get, so a missing run-id
	// surfaces the same sentinel.
	ex := readBashOutputExecutor{}
	node := &Node{ID: "rb", Kind: NodeKindReadBashOutput, Attrs: ReadBashOutputAttrs{BashRunId: "missing"}}
	if _, err := ex.Execute(context.Background(), env, node, nil); !errors.Is(err, ErrNoBashOutput) {
		t.Fatalf("expected ErrNoBashOutput, got %v", err)
	}
}

func TestWriteFileExecutor_CreateAndProvenance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	mem := newStubMemory()
	env := &Env{RunID: "r", SessionID: "s", Memory: mem}
	applyEnvDefaults(env)
	ex := writeFileExecutor{}
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload"}}
	inputs := PortValues{"payload": "hello file"}
	res, err := ex.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Outputs["ack"] != true {
		t.Errorf("ack = %v, want true", res.Outputs["ack"])
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello file" {
		t.Errorf("file content = %q, want %q", got, "hello file")
	}
	var saw bool
	for _, e := range res.Events.Events {
		if e.Kind == EventFileWrite {
			saw = true
			if !strings.Contains(string(e.Payload), "sha256") {
				t.Errorf("file_write event missing sha256: %s", e.Payload)
			}
			if !strings.Contains(string(e.Payload), "mtime") {
				t.Errorf("file_write event missing mtime: %s", e.Payload)
			}
			if !strings.Contains(string(e.Payload), `"state_action":"Write::file"`) {
				t.Errorf("file_write event missing state_action UID: %s", e.Payload)
			}
		}
	}
	if !saw {
		t.Error("missing file_write event")
	}
	if mem.writeCount() != 1 {
		t.Errorf("expected 1 memory hook write, got %d", mem.writeCount())
	}
}

func TestWriteFileExecutor_Append(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(path, []byte("line1\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := writeFileExecutor{}
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload", Mode: "append"}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"payload": "line2\n"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "line1\nline2\n" {
		t.Errorf("after append = %q, want line1\\nline2\\n", got)
	}
}

func TestWriteFileExecutor_PolicyDenied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	denied := errors.New("policy denied write")
	env := &Env{RunID: "r", Policy: stubPolicyGate{denyFileWrite: denied}}
	applyEnvDefaults(env)
	ex := writeFileExecutor{}
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload"}}
	_, err := ex.Execute(context.Background(), env, node, PortValues{"payload": "denied content"})
	if !errors.Is(err, denied) {
		t.Fatalf("expected policy-denied error, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("denied write still produced a file on disk")
	}
}

func TestWriteFileExecutor_StateWriteDenied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	denied := errors.New("state_write forbidden")
	env := &Env{RunID: "r", Policy: stubPolicyGate{denyStateWrite: denied}}
	applyEnvDefaults(env)
	ex := writeFileExecutor{}
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload"}}
	_, err := ex.Execute(context.Background(), env, node, PortValues{"payload": "blocked"})
	if !errors.Is(err, denied) {
		t.Fatalf("expected state_write deny, got %v", err)
	}
}

func TestWriteFileExecutor_CreateRefusesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(path, []byte("here"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	env := &Env{RunID: "r"}
	applyEnvDefaults(env)
	ex := writeFileExecutor{}
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload"}}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{"payload": "new"}); err == nil {
		t.Fatalf("expected error on create over existing file")
	}
}
