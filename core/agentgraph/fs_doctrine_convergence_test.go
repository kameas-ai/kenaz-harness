package agentgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fs_doctrine_convergence_test.go — agentgraph-total-convergence-01PMGX01
// WP13: pins the collapse of the "State-vs-Tool framing" doctrine
// (docs/agent-kernel-graph-node-catalog.md §4.9, deleted by this WP).
//
// Two things are pinned here:
//
//  1. read_file is genuinely reachable and correct through a real
//     Kernel (not just the direct-Execute() unit tests already in
//     exec_state_test.go) — spec §6 I3's "exercised end-to-end"
//     language, at the kernel level rather than the shipped-graph
//     level (see the WP13 report for why the shipped-graph level was
//     not attempted: a hardcoded absolute path in a bundled activity
//     would either be fake or would break real installs).
//  2. appendFSToolProvenance (tool_invocation.go) — the mechanism that
//     replaces the doctrine — actually produces the EventFileRead /
//     EventFileWrite shape the State-kind executors produce, from the
//     model-dispatch tool-call path, and does so identically for the
//     same file. That equivalence is the whole point of WP13's chosen
//     collapse: the doctrine arbitrated "which one remembers", and
//     both now do, identically.

// TestReadFileNode_RealKernelRun runs a single read_file node through a
// real *Kernel (NewKernel(), the production executor registry — no
// stub executors) end to end: graph construction, promotion, the real
// readFileExecutor, and EventLog persistence.
//
// convergence:exercised read_file
func TestReadFileNode_RealKernelRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	content := "read_file through a real kernel"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "read-file-kernel-run",
		Entrypoints: []string{"rf"},
		Nodes: []Node{
			{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path}},
		},
	}
	k := NewKernel()
	env := &Env{RunID: "read-file-kernel-run", Graph: g, SessionID: "s"}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !env.State.Completed("rf") {
		t.Fatal("read_file node did not complete")
	}
	got := env.State.Outputs("rf")
	if got["result"] != content {
		t.Errorf("result = %v, want %q", got["result"], content)
	}

	var sawFileRead bool
	if err := k.EventLog().Replay("read-file-kernel-run", func(e Event) error {
		if e.Kind == EventFileRead {
			sawFileRead = true
			var payload map[string]any
			if jerr := json.Unmarshal(e.Payload, &payload); jerr != nil {
				t.Errorf("EventFileRead payload not JSON: %v", jerr)
				return nil
			}
			if payload["state_action"] != "Read::file" {
				t.Errorf("state_action = %v, want Read::file", payload["state_action"])
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !sawFileRead {
		t.Error("missing file_read event from the real kernel run")
	}
}

// TestWriteFileNode_RealKernelRun is the write half of the pair, and
// the one the fsdoctrine review asked for: WP13 shipped a real-kernel
// fixture for read_file and left write_file with direct-Execute() unit
// tests only, so the two halves of the same doctrine collapse had
// asymmetric evidence.
//
// It is deliberately a TWO-node graph, not a one-node graph. write_file
// takes its bytes from an input PORT (`payload`, per
// nodes/manifests/write_file.yaml) rather than from an attr, so a
// single-node run cannot exercise it at all — it fails with "missing
// content on port". Wiring read_file -> write_file therefore exercises
// what a one-node fixture structurally cannot: edge delivery into a
// State-kind executor, and the port contract the manifest declares.
//
// convergence:exercised write_file
func TestWriteFileNode_RealKernelRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	content := "write_file through a real kernel"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "write-file-kernel-run",
		Entrypoints: []string{"rf"},
		Nodes: []Node{
			{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: src}},
			{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: dst, Content: "payload"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "rf", Port: "result"}, To: EndpointRef{Node: "wf", Port: "payload"}},
		},
	}
	k := NewKernel()
	env := &Env{RunID: "write-file-kernel-run", Graph: g, SessionID: "s"}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !env.State.Completed("wf") {
		t.Fatal("write_file node did not complete")
	}
	if ack := env.State.Outputs("wf")["ack"]; ack != true {
		t.Errorf("ack = %v, want true", ack)
	}

	// The bytes actually landed on disk — not just an event claiming so.
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read back written file: %v", err)
	}
	if string(got) != content {
		t.Errorf("written content = %q, want %q", got, content)
	}

	// And the provenance record matches those bytes.
	wantHash := sha256.Sum256([]byte(content))
	wantSum := hex.EncodeToString(wantHash[:])
	var sawFileWrite bool
	if err := k.EventLog().Replay("write-file-kernel-run", func(e Event) error {
		if e.Kind != EventFileWrite {
			return nil
		}
		sawFileWrite = true
		var payload map[string]any
		if jerr := json.Unmarshal(e.Payload, &payload); jerr != nil {
			t.Errorf("EventFileWrite payload not JSON: %v", jerr)
			return nil
		}
		if payload["state_action"] != "Write::file" {
			t.Errorf("state_action = %v, want Write::file", payload["state_action"])
		}
		if payload["sha256"] != wantSum {
			t.Errorf("sha256 = %v, want %q", payload["sha256"], wantSum)
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !sawFileWrite {
		t.Error("missing file_write event from the real kernel run")
	}
}

// TestAppendFSToolProvenance_ReadFile exercises the NEW WP13 mechanism:
// a model-dispatched kenaz__read_file tool call (the production
// tool_dispatch path — dispatchOneCall runs the real toolDispatchExecutor)
// records the same file_read provenance shape the read_file State kind
// records, even though the stubbed ToolRegistry — standing in for
// core/tools/fsbuiltins, which this package cannot import (WP13's own
// tool_invocation.go doc comment explains why dispatch is not
// retargeted onto the tool) — never touches the filesystem itself.
func TestAppendFSToolProvenance_ReadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	content := "kenaz__read_file provenance"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	wantHash := sha256.Sum256([]byte(content))
	wantSum := hex.EncodeToString(wantHash[:])

	tools := newStubTools()
	tools.allow("kenaz__read_file", `{"content":"`+content+`"}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	argsJSON, _ := json.Marshal(map[string]string{"path": path})
	res := dispatchOneCall(t, env, "kenaz__read_file", string(argsJSON))

	found := findEvent(res.Events.Events, EventFileRead)
	if found == nil {
		t.Fatal("missing file_read event for kenaz__read_file dispatch")
	}
	var payload map[string]any
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["sha256"] != wantSum {
		t.Errorf("sha256 = %v, want %q", payload["sha256"], wantSum)
	}
	if payload["state_action"] != "Read::file" {
		t.Errorf("state_action = %v, want Read::file", payload["state_action"])
	}
	if payload["tool"] != "kenaz__read_file" {
		t.Errorf("tool = %v, want kenaz__read_file", payload["tool"])
	}
}

// TestAppendFSToolProvenance_WriteFile mirrors the read case for
// kenaz__write_file: the file already reflects the write (as it would
// in production, since the real fsbuiltins tool writes before
// returning success) and the provenance helper hashes what is actually
// on disk after the call, not what the model claimed to write.
func TestAppendFSToolProvenance_WriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	content := "kenaz__write_file provenance"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed post-write file state: %v", err)
	}
	wantHash := sha256.Sum256([]byte(content))
	wantSum := hex.EncodeToString(wantHash[:])

	stubResult, _ := json.Marshal(map[string]any{"written": len(content), "path": path})
	tools := newStubTools()
	tools.allow("kenaz__write_file", string(stubResult), false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	argsJSON, _ := json.Marshal(map[string]string{"path": path, "content": content})
	res := dispatchOneCall(t, env, "kenaz__write_file", string(argsJSON))

	found := findEvent(res.Events.Events, EventFileWrite)
	if found == nil {
		t.Fatal("missing file_write event for kenaz__write_file dispatch")
	}
	var payload map[string]any
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["sha256"] != wantSum {
		t.Errorf("sha256 = %v, want %q", payload["sha256"], wantSum)
	}
	if payload["state_action"] != "Write::file" {
		t.Errorf("state_action = %v, want Write::file", payload["state_action"])
	}
}

// TestAppendFSToolProvenance_SkipsOnToolError pins the guard: a failed
// kenaz__read_file / kenaz__write_file call (IsError) must not fabricate
// a provenance record for an operation that did not actually happen.
func TestAppendFSToolProvenance_SkipsOnToolError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing-perm.txt")
	tools := newStubTools()
	tools.allow("kenaz__read_file", `{"error":"denied"}`, true)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	argsJSON, _ := json.Marshal(map[string]string{"path": path})
	res := dispatchOneCall(t, env, "kenaz__read_file", string(argsJSON))

	if found := findEvent(res.Events.Events, EventFileRead); found != nil {
		t.Errorf("file_read event emitted for a failed call: %s", found.Payload)
	}
}

// TestAppendFSToolProvenance_UnrelatedToolIsNoop pins the allowlist:
// appendFSToolProvenance only fires for the two named fs tools, not for
// every successful tool call (kenaz__glob also touches the filesystem
// but has no State-kind counterpart to converge with).
func TestAppendFSToolProvenance_UnrelatedToolIsNoop(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	res := dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
	if found := findEvent(res.Events.Events, EventFileRead); found != nil {
		t.Errorf("unrelated tool emitted file_read: %s", found.Payload)
	}
	if found := findEvent(res.Events.Events, EventFileWrite); found != nil {
		t.Errorf("unrelated tool emitted file_write: %s", found.Payload)
	}
}

// TestFSProvenance_StateKindAndToolPathAgree is the convergence
// assertion WP13 exists to establish: reading the SAME file through the
// State kind's executor and through the model-dispatched
// kenaz__read_file tool call must produce the same sha256 in the
// EventLog. Before this WP the tool-call path recorded no provenance at
// all — "which one do you use if you want it remembered" was a real
// question because one path had an answer and the other had silence.
// Now both do, and they agree.
func TestFSProvenance_StateKindAndToolPathAgree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")
	content := "the same bytes, two surfaces"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Surface A: the read_file State kind, direct executor call.
	envA := &Env{RunID: "state-path"}
	applyEnvDefaults(envA)
	resA, err := (readFileExecutor{}).Execute(context.Background(), envA,
		&Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path}}, nil)
	if err != nil {
		t.Fatalf("state-kind Execute: %v", err)
	}
	evA := findEvent(resA.Events.Events, EventFileRead)
	if evA == nil {
		t.Fatal("state-kind path emitted no file_read event")
	}
	var payloadA map[string]any
	if err := json.Unmarshal(evA.Payload, &payloadA); err != nil {
		t.Fatalf("state-kind payload not JSON: %v", err)
	}

	// Surface B: kenaz__read_file, model-dispatched.
	toolsB := newStubTools()
	toolsB.allow("kenaz__read_file", `{"content":"`+content+`"}`, false)
	envB := &Env{RunID: "tool-path", Tools: toolsB}
	applyEnvDefaults(envB)
	argsJSON, _ := json.Marshal(map[string]string{"path": path})
	resB := dispatchOneCall(t, envB, "kenaz__read_file", string(argsJSON))
	evB := findEvent(resB.Events.Events, EventFileRead)
	if evB == nil {
		t.Fatal("tool-dispatch path emitted no file_read event")
	}
	var payloadB map[string]any
	if err := json.Unmarshal(evB.Payload, &payloadB); err != nil {
		t.Fatalf("tool-dispatch payload not JSON: %v", err)
	}

	if payloadA["sha256"] != payloadB["sha256"] {
		t.Errorf("sha256 disagreement: state-kind=%v tool-dispatch=%v",
			payloadA["sha256"], payloadB["sha256"])
	}
	if payloadA["sha256"] == "" || payloadA["sha256"] == nil {
		t.Error("state-kind sha256 empty")
	}
}

// findEvent returns the first event of the given kind, or nil.
func findEvent(events []Event, kind EventKind) *Event {
	for i := range events {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}
