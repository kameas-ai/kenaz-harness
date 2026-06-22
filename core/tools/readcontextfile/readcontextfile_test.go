package readcontextfile_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
	"github.com/kameas-ai/kenaz-harness/core/tools/readcontextfile"
)

// fakeLibrary is a minimal LibraryReader stub.
type fakeLibrary struct {
	root    string
	content map[string]string
}

func (f *fakeLibrary) Root() string { return f.root }
func (f *fakeLibrary) Get(path string) (string, error) {
	if c, ok := f.content[path]; ok {
		return c, nil
	}
	return "", errors.New("contexts: not found: " + path)
}

// fakeModuleSource is a minimal ModuleSource stub.
type fakeModuleSource struct {
	dirs []string
}

func (f *fakeModuleSource) AttachedModuleDirs(_ context.Context, _ string) ([]string, error) {
	return f.dirs, nil
}

// fixedSession returns a session resolver that always returns the given ID.
func fixedSession(id string) func(context.Context) string {
	return func(context.Context) string { return id }
}

func newTool(lib readcontextfile.LibraryReader, dirs []string) *readcontextfile.Tool {
	return readcontextfile.New(readcontextfile.Options{
		Library:         lib,
		Modules:         &fakeModuleSource{dirs: dirs},
		SessionResolver: fixedSession("sess-test"),
	})
}

func callArgs(t *testing.T, tool *readcontextfile.Tool, path string) json.RawMessage {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	return out
}

func isSuccess(t *testing.T, out json.RawMessage) (string, string) {
	t.Helper()
	var v struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if v.Error != "" {
		t.Fatalf("unexpected error result: %s", v.Error)
	}
	return v.Path, v.Content
}

func isError(t *testing.T, out json.RawMessage) string {
	t.Helper()
	var v struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if v.Error == "" {
		t.Fatalf("expected error result; got success")
	}
	return v.Error
}

// ── Happy path ──────────────────────────────────────────────────────────────

func TestCall_ReadsFileInModule(t *testing.T) {
	t.Parallel()
	lib := &fakeLibrary{
		root:    "/tmp/fake-root",
		content: map[string]string{"mymod/reference.md": "# Reference"},
	}
	tool := newTool(lib, []string{"mymod"})
	out := callArgs(t, tool, "mymod/reference.md")
	path, content := isSuccess(t, out)
	if path != "mymod/reference.md" {
		t.Errorf("path = %q; want mymod/reference.md", path)
	}
	if content != "# Reference" {
		t.Errorf("content = %q; want '# Reference'", content)
	}
}

// ── Path-confinement checks ─────────────────────────────────────────────────

func TestCall_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	lib := &fakeLibrary{root: "/tmp/fake", content: map[string]string{}}
	tool := newTool(lib, []string{"mymod"})

	for _, bad := range []string{
		"../escape.md",
		"mymod/../../escape.md",
		"/absolute/path.md",
	} {
		t.Run(bad, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"path": bad})
			out, err := tool.Call(context.Background(), args)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			errMsg := isError(t, out)
			if !strings.Contains(errMsg, "path_traversal") && !strings.Contains(errMsg, "not_in_module") {
				t.Errorf("expected path_traversal or not_in_module; got: %s", errMsg)
			}
		})
	}
}

func TestCall_RejectsPathOutsideAttachedModule(t *testing.T) {
	t.Parallel()
	lib := &fakeLibrary{
		root:    "/tmp/fake",
		content: map[string]string{"othermod/secret.md": "secret"},
	}
	// Only "mymod" is attached, not "othermod".
	tool := newTool(lib, []string{"mymod"})
	args, _ := json.Marshal(map[string]string{"path": "othermod/secret.md"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	errMsg := isError(t, out)
	if !strings.Contains(errMsg, "not_in_module") {
		t.Errorf("expected not_in_module; got: %s", errMsg)
	}
}

func TestCall_RejectsEmptyPath(t *testing.T) {
	t.Parallel()
	lib := &fakeLibrary{root: "/tmp/fake", content: map[string]string{}}
	tool := newTool(lib, []string{"mymod"})
	args, _ := json.Marshal(map[string]string{"path": ""})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	errMsg := isError(t, out)
	if !strings.Contains(errMsg, "invalid_args") {
		t.Errorf("expected invalid_args; got: %s", errMsg)
	}
}

// ── No module source wired ──────────────────────────────────────────────────

func TestCall_NilModuleSource(t *testing.T) {
	t.Parallel()
	lib := &fakeLibrary{
		root:    "/tmp/fake",
		content: map[string]string{"mymod/a.md": "hello"},
	}
	tool := readcontextfile.New(readcontextfile.Options{
		Library:         lib,
		Modules:         nil, // no module source wired
		SessionResolver: fixedSession("s"),
	})
	args, _ := json.Marshal(map[string]string{"path": "mymod/a.md"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	errMsg := isError(t, out)
	if !strings.Contains(errMsg, "not_in_module") {
		t.Errorf("expected not_in_module; got: %s", errMsg)
	}
}

// ── No library wired ───────────────────────────────────────────────────────

func TestCall_NilLibrary(t *testing.T) {
	t.Parallel()
	tool := readcontextfile.New(readcontextfile.Options{
		Library:         nil,
		Modules:         &fakeModuleSource{dirs: []string{"mymod"}},
		SessionResolver: fixedSession("s"),
	})
	args, _ := json.Marshal(map[string]string{"path": "mymod/a.md"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	errMsg := isError(t, out)
	if !strings.Contains(errMsg, "library_unavailable") {
		t.Errorf("expected library_unavailable; got: %s", errMsg)
	}
}

// ── Integration: real library + module detection ────────────────────────────

func TestIntegration_RealLibraryAndModule(t *testing.T) {
	t.Parallel()
	lib, err := contexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// CreateFolder scaffolds context.md.
	if err := lib.CreateFolder("docs"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := lib.Save("docs/reference.md", "# Reference doc"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tool := readcontextfile.New(readcontextfile.Options{
		Library: lib,
		Modules: &fakeModuleSource{dirs: []string{"docs"}},
	})

	args, _ := json.Marshal(map[string]string{"path": "docs/reference.md"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	_, content := isSuccess(t, out)
	if content != "# Reference doc" {
		t.Errorf("content = %q; want '# Reference doc'", content)
	}
}
