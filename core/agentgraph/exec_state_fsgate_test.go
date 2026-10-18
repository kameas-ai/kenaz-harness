package agentgraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// graph-fs-gate-01GFSG01 WP02: read_file / write_file consult the
// optional FileAccessGate (production: the fs builtin tools' fs.Gate)
// AFTER the Cedar checks, which still short-circuit first.

type fileGatePolicy struct {
	stubPolicyGate
	denyRead  error
	denyWrite error

	mu    sync.Mutex
	calls []string
}

func (p *fileGatePolicy) record(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, s)
}

func (p *fileGatePolicy) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func (p *fileGatePolicy) AuthorizeFileRead(_ context.Context, path string) error {
	p.record("read:" + path)
	return p.denyRead
}

func (p *fileGatePolicy) AuthorizeFileWrite(_ context.Context, path string) error {
	p.record("write:" + path)
	return p.denyWrite
}

var _ FileAccessGate = (*fileGatePolicy)(nil)

func runWriteFile(t *testing.T, pol PolicyGate, path string) error {
	t.Helper()
	env := &Env{RunID: "r", Policy: pol}
	applyEnvDefaults(env)
	node := &Node{ID: "wf", Kind: NodeKindWriteFile, Attrs: WriteFileAttrs{Path: path, Content: "payload"}}
	_, err := writeFileExecutor{}.Execute(context.Background(), env, node, PortValues{"payload": "x"})
	return err
}

func runReadFile(t *testing.T, pol PolicyGate, path string) error {
	t.Helper()
	env := &Env{RunID: "r", Policy: pol}
	applyEnvDefaults(env)
	node := &Node{ID: "rf", Kind: NodeKindReadFile, Attrs: ReadFileAttrs{Path: path}}
	_, err := readFileExecutor{}.Execute(context.Background(), env, node, nil)
	return err
}

func TestWriteFileExecutor_FileGateDenies(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	denied := errors.New("fs gate: user denied")
	pol := &fileGatePolicy{denyWrite: denied}
	if err := runWriteFile(t, pol, path); !errors.Is(err, denied) {
		t.Fatalf("want fs-gate deny, got %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("fs-gate-denied write still produced a file")
	}
	if got := pol.snapshot(); len(got) != 1 || got[0] != "write:"+path {
		t.Fatalf("fs gate calls = %v", got)
	}
}

func TestWriteFileExecutor_CedarDenyShortCircuitsFileGate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	cedarDeny := errors.New("cedar forbid")
	for name, pol := range map[string]*fileGatePolicy{
		"file_write":  {stubPolicyGate: stubPolicyGate{denyFileWrite: cedarDeny}},
		"state_write": {stubPolicyGate: stubPolicyGate{denyStateWrite: cedarDeny}},
	} {
		if err := runWriteFile(t, pol, path); !errors.Is(err, cedarDeny) {
			t.Fatalf("%s: want cedar deny, got %v", name, err)
		}
		if got := pol.snapshot(); len(got) != 0 {
			t.Fatalf("%s: fs gate consulted after a Cedar deny: %v", name, got)
		}
	}
}

func TestWriteFileExecutor_FileGateAllows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "out.txt")
	pol := &fileGatePolicy{}
	if err := runWriteFile(t, pol, path); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("allowed write missing on disk: %v", err)
	}
}

func TestReadFileExecutor_FileGateDenies(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	denied := errors.New("fs gate: unattended deny")
	pol := &fileGatePolicy{denyRead: denied}
	if err := runReadFile(t, pol, path); !errors.Is(err, denied) {
		t.Fatalf("want fs-gate deny, got %v", err)
	}

	cedarDeny := errors.New("cedar forbid read")
	pol2 := &fileGatePolicy{stubPolicyGate: stubPolicyGate{denyFileRead: cedarDeny}}
	if err := runReadFile(t, pol2, path); !errors.Is(err, cedarDeny) {
		t.Fatalf("want cedar deny, got %v", err)
	}
	if got := pol2.snapshot(); len(got) != 0 {
		t.Fatalf("fs gate consulted after a Cedar read deny: %v", got)
	}
}
