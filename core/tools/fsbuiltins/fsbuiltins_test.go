package fsbuiltins_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
	"github.com/sigil-tech/kaneaz-harness/core/tools/fsbuiltins"
)

// opts returns Options with no Gate (all paths permitted) and the given ReadSet.
func opts(rs *fsbuiltins.ReadSet) fsbuiltins.Options {
	return fsbuiltins.Options{
		Gate:         nil, // nil gate = always permit
		ReadSet:      rs,
		ReadEnabled:  nil, // nil = always enabled
		WriteEnabled: nil,
	}
}

// call is a test helper that calls a BuiltinTool with JSON args.
func call(t *testing.T, tool toolloop.BuiltinTool, args any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("tool.Call returned unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("unmarshal result: %v — raw: %s", err, result)
	}
	return m
}

// isError reports whether the result carries is_error=true.
func isError(m map[string]any) bool {
	v, _ := m["is_error"].(bool)
	return v
}

// ─── ReadSet ────────────────────────────────────────────────────────────────

func TestReadSet(t *testing.T) {
	rs := fsbuiltins.NewReadSet()
	const sid = "session-1"
	const path = "/tmp/foo.txt"

	if rs.Has(sid, path) {
		t.Fatal("expected false before Add")
	}
	rs.Add(sid, path)
	if !rs.Has(sid, path) {
		t.Fatal("expected true after Add")
	}
	// Different session should not see it.
	if rs.Has("session-2", path) {
		t.Fatal("expected false for different session")
	}
	rs.Drop(sid)
	if rs.Has(sid, path) {
		t.Fatal("expected false after Drop")
	}
}

// ─── read_file ──────────────────────────────────────────────────────────────

func TestReadFileTool_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet()
	tool := fsbuiltins.NewReadFileTool(opts(rs))

	m := call(t, tool, map[string]any{"path": path})
	if isError(m) {
		t.Fatalf("unexpected error: %s", m["error"])
	}
	if m["content"] != "hello world" {
		t.Errorf("unexpected content: %v", m["content"])
	}
	// read_file must record the canonical path so edit_file can proceed.
	canonical, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if !rs.Has("", canonical) {
		// Empty session ID from context is ""
		t.Error("ReadSet should contain the canonical path after read_file")
	}
}

func TestReadFileTool_Offset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("ABCDEF"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewReadFileTool(opts(nil))
	m := call(t, tool, map[string]any{"path": path, "offset": 2, "limit": 3})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	if m["content"] != "CDE" {
		t.Errorf("expected CDE, got %v", m["content"])
	}
	if m["truncated"] != true {
		t.Errorf("expected truncated=true")
	}
}

func TestReadFileTool_Missing(t *testing.T) {
	tool := fsbuiltins.NewReadFileTool(opts(nil))
	m := call(t, tool, map[string]any{"path": "/nonexistent/file.txt"})
	if !isError(m) {
		t.Error("expected error for missing file")
	}
}

func TestReadFileTool_DisabledWhenPredicateOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("x"), 0o644)

	o := opts(nil)
	o.ReadEnabled = func() bool { return false }
	tool := fsbuiltins.NewReadFileTool(o)

	m := call(t, tool, map[string]any{"path": path})
	if !isError(m) {
		t.Error("expected error when ReadEnabled=false")
	}
}

// ─── list_dir ───────────────────────────────────────────────────────────────

func TestListDirTool_Basic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewListDirTool(opts(nil))
	m := call(t, tool, map[string]any{"path": dir})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	entries := m["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestListDirTool_Recursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "c.txt"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewListDirTool(opts(nil))
	m := call(t, tool, map[string]any{"path": dir, "recursive": true})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	entries := m["entries"].([]any)
	// Expect: sub/ (dir), sub/c.txt (file), a.txt (file) — 3 total.
	if len(entries) != 3 {
		t.Errorf("expected 3 entries recursive, got %d: %v", len(entries), entries)
	}
}

// ─── glob ───────────────────────────────────────────────────────────────────

func TestGlobTool_Basic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bar.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "baz.ts"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewGlobTool(opts(nil))
	m := call(t, tool, map[string]any{"pattern": "*.go", "base_dir": dir})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	matches := m["matches"].([]any)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}

func TestGlobTool_DoubleStarPattern(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "deep.go"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewGlobTool(opts(nil))
	m := call(t, tool, map[string]any{"pattern": "**/*.go", "base_dir": dir})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	matches := m["matches"].([]any)
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d: %v", len(matches), matches)
	}
}

// ─── grep ───────────────────────────────────────────────────────────────────

func TestGrepTool_BasicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	content := "package main\nfunc foo() {}\nfunc bar() {}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewGrepTool(opts(nil))
	m := call(t, tool, map[string]any{"pattern": "func", "path": path})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	matches := m["matches"].([]any)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestGrepTool_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(path, []byte("Hello World\nhello world\nHELLO WORLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewGrepTool(opts(nil))
	m := call(t, tool, map[string]any{
		"pattern":          "hello",
		"path":             path,
		"case_insensitive": true,
	})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	matches := m["matches"].([]any)
	if len(matches) != 3 {
		t.Errorf("expected 3 case-insensitive matches, got %d", len(matches))
	}
}

func TestGrepTool_Directory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("no match\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewGrepTool(opts(nil))
	// With include filter.
	m := call(t, tool, map[string]any{
		"pattern": "needle",
		"path":    dir,
		"include": "*.go",
	})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	matches := m["matches"].([]any)
	if len(matches) != 1 {
		t.Errorf("expected 1 match (only .go), got %d", len(matches))
	}
}

// ─── write_file ─────────────────────────────────────────────────────────────

func TestWriteFileTool_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tool := fsbuiltins.NewWriteFileTool(opts(nil))
	m := call(t, tool, map[string]any{"path": path, "content": "hello"})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestWriteFileTool_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c.txt")

	tool := fsbuiltins.NewWriteFileTool(opts(nil))
	m := call(t, tool, map[string]any{"path": path, "content": "x"})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestWriteFileTool_DisabledWhenPredicateOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")

	o := opts(nil)
	o.WriteEnabled = func() bool { return false }
	tool := fsbuiltins.NewWriteFileTool(o)

	m := call(t, tool, map[string]any{"path": path, "content": "x"})
	if !isError(m) {
		t.Error("expected error when WriteEnabled=false")
	}
}

// ─── edit_file ──────────────────────────────────────────────────────────────

func TestEditFileTool_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("foo bar baz"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet()
	canonical, _ := filepath.Abs(path)
	rs.Add("", canonical) // pre-record as read (empty session from ctx)

	tool := fsbuiltins.NewEditFileTool(opts(rs))
	m := call(t, tool, map[string]any{
		"path":    path,
		"old_str": "bar",
		"new_str": "qux",
	})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foo qux baz" {
		t.Errorf("unexpected content: %q", data)
	}
}

func TestEditFileTool_RequiresRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("foo bar baz"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet() // not adding the path
	o := opts(rs)
	tool := fsbuiltins.NewEditFileTool(o)
	m := call(t, tool, map[string]any{
		"path":    path,
		"old_str": "bar",
		"new_str": "qux",
	})
	if !isError(m) {
		t.Error("expected ErrEditWithoutRead error")
	}
}

func TestEditFileTool_AmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet()
	canonical, _ := filepath.Abs(path)
	rs.Add("", canonical)

	tool := fsbuiltins.NewEditFileTool(opts(rs))
	m := call(t, tool, map[string]any{
		"path":    path,
		"old_str": "foo",
		"new_str": "bar",
	})
	if !isError(m) {
		t.Error("expected error for ambiguous match (appears 3 times)")
	}
}

func TestEditFileTool_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet()
	canonical, _ := filepath.Abs(path)
	rs.Add("", canonical)

	tool := fsbuiltins.NewEditFileTool(opts(rs))
	m := call(t, tool, map[string]any{
		"path":    path,
		"old_str": "NOTPRESENT",
		"new_str": "x",
	})
	if !isError(m) {
		t.Error("expected error when old_str not found")
	}
}

// ─── ReadFile → EditFile integration ────────────────────────────────────────

func TestReadThenEdit_Integration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := fsbuiltins.NewReadSet()
	readTool := fsbuiltins.NewReadFileTool(fsbuiltins.Options{ReadSet: rs})
	editTool := fsbuiltins.NewEditFileTool(fsbuiltins.Options{ReadSet: rs})

	// Step 1: read.
	_ = call(t, readTool, map[string]any{"path": path})

	// Step 2: edit without error.
	m := call(t, editTool, map[string]any{
		"path":    path,
		"old_str": "func main() {}",
		"new_str": "func main() { println(\"hi\") }",
	})
	if isError(m) {
		t.Fatalf("edit after read should succeed: %s", m["error"])
	}

	data, _ := os.ReadFile(path)
	if !containsStr(string(data), "println") {
		t.Errorf("edit not applied: %q", data)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s[1:], sub) || s[:len(sub)] == sub)
}

// ─── list_open_worklist ─────────────────────────────────────────────────────

func TestListOpenWorklistTool_Empty(t *testing.T) {
	tool := fsbuiltins.NewListOpenWorklistTool(fsbuiltins.Options{WorklistDir: ""})
	m := call(t, tool, map[string]any{})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	// Empty worklist must include loop_refusal hint.
	refusal, _ := m["loop_refusal"].(string)
	if refusal == "" {
		t.Error("expected loop_refusal hint when worklist is empty")
	}
}

func TestListOpenWorklistTool_WithItems(t *testing.T) {
	dir := t.TempDir()
	item := map[string]any{
		"id":     "task-1",
		"title":  "Do the thing",
		"status": "open",
	}
	data, _ := json.Marshal(item)
	if err := os.WriteFile(filepath.Join(dir, "task-1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := fsbuiltins.NewListOpenWorklistTool(fsbuiltins.Options{WorklistDir: dir})
	m := call(t, tool, map[string]any{})
	if isError(m) {
		t.Fatalf("error: %s", m["error"])
	}
	items := m["items"].([]any)
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}
	// No loop refusal when items present.
	if refusal, _ := m["loop_refusal"].(string); refusal != "" {
		t.Errorf("unexpected loop_refusal when items exist: %q", refusal)
	}
}
