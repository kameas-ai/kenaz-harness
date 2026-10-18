package agentgraph_test

// graph-fs-gate-01GFSG01 WP02 (FR-1/FR-2): the graph PolicyGateAdapter
// routes read_file / write_file through the SAME core/tools/fs.Gate the
// fs builtin tools use. These drive a real cedar.Engine (embedded
// bundle) and a real corefs.Gate with a fake Prompter on a temp path.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

type fsgateFakePrompter struct {
	mu    sync.Mutex
	resp  corefs.PromptResponse
	calls []corefs.PromptSurface
}

func (p *fsgateFakePrompter) Prompt(_ context.Context, s corefs.PromptSurface) (corefs.PromptResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, s)
	return p.resp, nil
}

func (p *fsgateFakePrompter) snapshot() []corefs.PromptSurface {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]corefs.PromptSurface, len(p.calls))
	copy(out, p.calls)
	return out
}

func embeddedEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func fileGate(t *testing.T, eng *cedar.Engine, p corefs.Prompter) *corefs.Gate {
	t.Helper()
	return corefs.NewGate(corefs.GateOptions{Engine: eng, Prompter: p})
}

func asFileGate(t *testing.T, pg coreag.PolicyGate) coreag.FileAccessGate {
	t.Helper()
	fg, ok := pg.(coreag.FileAccessGate)
	if !ok {
		t.Fatalf("%T does not implement coreag.FileAccessGate", pg)
	}
	return fg
}

func TestPolicyGateAdapter_NoFileGate_Unchanged(t *testing.T) {
	t.Parallel()
	a := graphview.NewPolicyGateAdapter(embeddedEngine(t))
	path := filepath.Join(t.TempDir(), "x.txt")
	if err := a.AuthorizeFileWrite(context.Background(), path); err != nil {
		t.Fatalf("no fs gate bound: AuthorizeFileWrite = %v, want nil", err)
	}
	a.SetFileGate(nil)
	if err := a.AuthorizeFileRead(context.Background(), path); err != nil {
		t.Fatalf("SetFileGate(nil): AuthorizeFileRead = %v, want nil", err)
	}
}

func TestPolicyGateAdapter_FileGate_PromptDenyDenies(t *testing.T) {
	t.Parallel()
	eng := embeddedEngine(t)
	p := &fsgateFakePrompter{resp: corefs.PromptDeny}
	a := graphview.NewPolicyGateAdapter(eng)
	a.SetFileGate(fileGate(t, eng, p))
	path := filepath.Join(t.TempDir(), "x.txt")

	// Cedar alone permits (embedded default_policy permits file_write).
	if err := a.CheckFileWrite(context.Background(), path); err != nil {
		t.Fatalf("Cedar file_write denied on the embedded bundle: %v", err)
	}
	err := a.AuthorizeFileWrite(context.Background(), path)
	var pde *cedar.PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("AuthorizeFileWrite with a denying prompter = %v, want *cedar.PolicyDeniedError", err)
	}
	if err := a.AuthorizeFileRead(context.Background(), path); !cedar.IsPolicyDenied(err) {
		t.Fatalf("AuthorizeFileRead outside recipe dirs with a denying prompter = %v, want denied", err)
	}
	if n := len(p.snapshot()); n != 2 {
		t.Fatalf("prompter calls = %d, want 2", n)
	}
}

func TestPolicyGateAdapter_FileGate_AllowOnceHonoredAcrossCallers(t *testing.T) {
	t.Parallel()
	eng := embeddedEngine(t)
	p := &fsgateFakePrompter{resp: corefs.PromptAllowOnce}
	gate := fileGate(t, eng, p)
	a := graphview.NewPolicyGateAdapter(eng)
	a.SetFileGate(gate)
	path := filepath.Join(t.TempDir(), "x.txt")

	// The fs TOOL path confirms the path first (same gate instance) ...
	if d, err := gate.Evaluate(context.Background(), corefs.OpWrite, path); err != nil || d.Outcome != cedar.Allow {
		t.Fatalf("tool-path confirm: outcome=%v err=%v", d.Outcome, err)
	}
	// ... then the prompter starts refusing; the graph node path must
	// still honour the grant the user gave the tool path.
	p.mu.Lock()
	p.resp = corefs.PromptDeny
	p.mu.Unlock()
	if err := a.AuthorizeFileWrite(context.Background(), path); err != nil {
		t.Fatalf("graph write to a user-confirmed path denied: %v", err)
	}
	if n := len(p.snapshot()); n != 1 {
		t.Fatalf("prompter calls = %d, want 1 (the grant should be reused, not re-prompted)", n)
	}
}

func TestPolicyGateAdapter_FileGate_SharedAcrossPostureCopies(t *testing.T) {
	t.Parallel()
	eng := embeddedEngine(t)
	p := &fsgateFakePrompter{resp: corefs.PromptDeny}
	base := graphview.NewPolicyGateAdapter(eng)
	copyBefore := base.WithPostureMode("")
	path := filepath.Join(t.TempDir(), "x.txt")

	// Bind AFTER the copy exists: the copy must see it.
	base.SetFileGate(fileGate(t, eng, p))
	if err := asFileGate(t, copyBefore).AuthorizeFileWrite(context.Background(), path); !cedar.IsPolicyDenied(err) {
		t.Fatalf("copy made before SetFileGate: AuthorizeFileWrite = %v, want denied", err)
	}
	copyAfter := base.WithPostureMode("")
	if err := asFileGate(t, copyAfter).AuthorizeFileWrite(context.Background(), path); !cedar.IsPolicyDenied(err) {
		t.Fatalf("copy made after SetFileGate: AuthorizeFileWrite = %v, want denied", err)
	}
	// plan_mode still denies at the Cedar layer first.
	plan := base.WithPostureMode(autonomy.PostureModePlanMode)
	if err := plan.CheckFileWrite(context.Background(), path); err == nil {
		t.Fatal("plan_mode CheckFileWrite = nil, want denied")
	}
}

func TestPolicyGateAdapter_FileGate_InvalidPath(t *testing.T) {
	t.Parallel()
	eng := embeddedEngine(t)
	a := graphview.NewPolicyGateAdapter(eng)
	a.SetFileGate(fileGate(t, eng, &fsgateFakePrompter{resp: corefs.PromptAllowOnce}))
	if err := a.AuthorizeFileWrite(context.Background(), "bad\x00path"); !errors.Is(err, corefs.ErrInvalidPath) {
		t.Fatalf("AuthorizeFileWrite(invalid) = %v, want ErrInvalidPath", err)
	}
}
