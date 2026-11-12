package fs

// Fleet e2e 2026-11-07 regressions (E2E-1 / E2E-3), driven through the
// REAL chain: Gate.Evaluate -> CedarPrompter -> cedar.Registry ->
// a disk-backed cedar.Engine. No stub engine: the original bug was that
// a written grant was never re-read, which a stub cannot see.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

type realChain struct {
	gate       *Gate
	registry   *cedar.Registry
	policyDir  string
	dispatched atomic.Int32
}

func newRealChain(t *testing.T, opts GateOptions) *realChain {
	t.Helper()
	dataDir := t.TempDir()
	rc := &realChain{policyDir: filepath.Join(dataDir, cedar.PolicyDir)}
	rc.registry = cedar.NewRegistry(
		cedar.WithTimeout(10*time.Second),
		cedar.WithDispatcher(cedar.PromptDispatcherFunc(func(context.Context, string, cedar.PendingRequest) {
			rc.dispatched.Add(1)
		})),
	)
	engine, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	opts.Engine = engine
	opts.Prompter = &CedarPrompter{Registry: rc.registry}
	opts.PolicyDir = rc.policyDir
	rc.gate = NewGate(opts)
	rc.registry.SetGrantCoverage(cedar.FamilyFilesystem, rc.gate.GrantCoverage)
	return rc
}

type evalResult struct {
	d   cedar.Decision
	err error
}

// evalAsync starts Evaluate on a goroutine.
func (rc *realChain) evalAsync(op Op, path string) <-chan evalResult {
	ch := make(chan evalResult, 1)
	go func() {
		d, err := rc.gate.Evaluate(context.Background(), op, path)
		ch <- evalResult{d, err}
	}()
	return ch
}

// waitPending blocks until n requests are pending and returns them.
func (rc *realChain) waitPending(t *testing.T, n int) []cedar.PendingRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := rc.registry.ListPending(); len(p) >= n {
			return p
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pending requests (have %d)", n, len(rc.registry.ListPending()))
	return nil
}

func pendingFor(t *testing.T, ps []cedar.PendingRequest, path string) cedar.PendingRequest {
	t.Helper()
	for _, p := range ps {
		if p.Surface.FS != nil && p.Surface.FS.CanonicalPath == path {
			return p
		}
	}
	t.Fatalf("no pending request for %s", path)
	return cedar.PendingRequest{}
}

func mustAllow(t *testing.T, ch <-chan evalResult, what string) cedar.Decision {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("%s: Evaluate error: %v", what, r.err)
		}
		if r.d.Outcome != cedar.Allow {
			t.Fatalf("%s: outcome = %s (%s), want Allow", what, r.d.Outcome, r.d.Reason)
		}
		return r.d
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: Evaluate did not return", what)
	}
	return cedar.Decision{}
}

// evalNoPrompt runs Evaluate and fails if it raises any prompt.
func (rc *realChain) evalNoPrompt(t *testing.T, op Op, path string) {
	t.Helper()
	before := rc.dispatched.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d, err := rc.gate.Evaluate(ctx, op, path)
	if err != nil {
		t.Fatalf("Evaluate(%s %s): %v", op, path, err)
	}
	if got := rc.dispatched.Load(); got != before {
		t.Fatalf("Evaluate(%s %s) prompted (%d new prompts); want covered by the earlier grant", op, path, got-before)
	}
	if d.Outcome != cedar.Allow {
		t.Fatalf("Evaluate(%s %s) = %s (%s), want Allow", op, path, d.Outcome, d.Reason)
	}
}

// E2E-1 (a): grant the directory on the first write; the second write
// into the same directory must not prompt. Fails on main twice over:
// the scope was dropped (exact grant) and the engine never reloaded.
func TestE2E1_DirectoryGrant_SiblingWriteNeedsNoPrompt(t *testing.T) {
	t.Parallel()
	rc := newRealChain(t, GateOptions{})
	dir := filepath.Join(t.TempDir(), "e2e-status-page")

	first := rc.evalAsync(OpWrite, filepath.Join(dir, "app.js"))
	p := rc.waitPending(t, 1)[0]
	if !p.Surface.FS.ParentMissing {
		t.Errorf("ParentMissing = false for a write into a directory that does not exist yet")
	}
	if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatalf("ResolveScoped: %v", err)
	}
	mustAllow(t, first, "first write")

	rc.evalNoPrompt(t, OpWrite, filepath.Join(dir, "styles.css"))
	rc.evalNoPrompt(t, OpWrite, filepath.Join(dir, "assets", "logo.svg"))
}

// E2E-1 (b): three writes queued in parallel; answering the first with
// a directory grant releases the other two inside the same Resolve call,
// and an unrelated queued request stays pending.
func TestE2E1_QueuedPromptsReleasedByBroaderGrant(t *testing.T) {
	t.Parallel()
	rc := newRealChain(t, GateOptions{})
	root := t.TempDir()
	dir := filepath.Join(root, "site")
	names := []string{"app.js", "styles.css", "index.html"}
	chans := make([]<-chan evalResult, len(names))
	for i, n := range names {
		chans[i] = rc.evalAsync(OpWrite, filepath.Join(dir, n))
	}
	other := rc.evalAsync(OpWrite, filepath.Join(root, "elsewhere", "x.txt"))
	ps := rc.waitPending(t, 4)

	granted := pendingFor(t, ps, filepath.Join(dir, "app.js"))
	if err := rc.registry.ResolveScoped(granted.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatalf("ResolveScoped: %v", err)
	}
	// Synchronous: by the time Resolve returns, only the unrelated
	// request is left, so the modal's post-resolve reconcile drops the
	// released rows without a race.
	left := rc.registry.ListPending()
	if len(left) != 1 || left[0].Surface.FS.CanonicalPath != filepath.Join(root, "elsewhere", "x.txt") {
		t.Fatalf("pending after directory grant = %+v, want only the unrelated request", left)
	}
	for i, ch := range chans {
		mustAllow(t, ch, names[i])
	}
	if err := rc.registry.Resolve(left[0].RequestID, cedar.DecisionDeny); err != nil {
		t.Fatalf("Resolve unrelated: %v", err)
	}
	if r := <-other; r.d.Outcome != cedar.Deny {
		t.Fatalf("unrelated request outcome = %s, want Deny", r.d.Outcome)
	}
}

// An exact-path Allow-always covers nothing else: queued siblings keep
// prompting.
func TestE2E1_ExactGrantDoesNotReleaseSiblings(t *testing.T) {
	t.Parallel()
	rc := newRealChain(t, GateOptions{})
	dir := t.TempDir()
	a := rc.evalAsync(OpWrite, filepath.Join(dir, "a"))
	b := rc.evalAsync(OpWrite, filepath.Join(dir, "b"))
	ps := rc.waitPending(t, 2)
	if err := rc.registry.ResolveScoped(pendingFor(t, ps, filepath.Join(dir, "a")).RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeExact); err != nil {
		t.Fatalf("ResolveScoped: %v", err)
	}
	mustAllow(t, a, "a")
	left := rc.registry.ListPending()
	if len(left) != 1 {
		t.Fatalf("pending after exact grant = %d, want 1 (sibling still prompts)", len(left))
	}
	_ = rc.registry.Resolve(left[0].RequestID, cedar.DecisionDeny)
	<-b
}

// E2E-1 (c): reads after a directory WRITE grant. Read and write stay
// separate Cedar actions; the write prompt's "also allow reading" scope
// (directory_rw) grants both, the plain directory scope grants writes
// only.
func TestE2E1_ReadAfterDirectoryWriteGrant(t *testing.T) {
	t.Parallel()

	t.Run("directory_rw covers reads of files and the directory itself", func(t *testing.T) {
		t.Parallel()
		rc := newRealChain(t, GateOptions{})
		dir := filepath.Join(t.TempDir(), "site")
		w := rc.evalAsync(OpWrite, filepath.Join(dir, "index.html"))
		// A read already queued behind the write is released too.
		r := rc.evalAsync(OpRead, filepath.Join(dir, "app.js"))
		ps := rc.waitPending(t, 2)
		if err := rc.registry.ResolveScoped(pendingFor(t, ps, filepath.Join(dir, "index.html")).RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectoryReadWrite); err != nil {
			t.Fatalf("ResolveScoped: %v", err)
		}
		mustAllow(t, w, "write")
		mustAllow(t, r, "queued read")
		rc.evalNoPrompt(t, OpRead, filepath.Join(dir, "index.html"))
		rc.evalNoPrompt(t, OpRead, dir) // list_dir of the granted directory
	})

	t.Run("directory (write only) still prompts for reads", func(t *testing.T) {
		t.Parallel()
		rc := newRealChain(t, GateOptions{})
		dir := filepath.Join(t.TempDir(), "site")
		w := rc.evalAsync(OpWrite, filepath.Join(dir, "index.html"))
		p := rc.waitPending(t, 1)[0]
		if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
			t.Fatalf("ResolveScoped: %v", err)
		}
		mustAllow(t, w, "write")
		r := rc.evalAsync(OpRead, filepath.Join(dir, "index.html"))
		p = rc.waitPending(t, 1)[0]
		if p.Surface.FS.Op != string(OpRead) {
			t.Fatalf("pending op = %q, want read", p.Surface.FS.Op)
		}
		_ = rc.registry.Resolve(p.RequestID, cedar.DecisionDeny)
		<-r
	})
}

// A dangerous-tier request is never released by a broader grant: it
// was never going to be satisfied by a remembered grant.
func TestGrantCoverage_SkipsDangerousQueued(t *testing.T) {
	t.Parallel()
	g := NewGate(GateOptions{})
	granted := cedar.PendingRequest{RequestID: "a", Family: cedar.FamilyFilesystem, Surface: cedar.PromptSurface{FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/srv/p/a"}}}
	match := g.GrantCoverage(granted, cedar.ScopeDirectory)
	if match == nil {
		t.Fatal("directory grant covers nothing")
	}
	mk := func(op, path string, dangerous bool) cedar.PendingRequest {
		return cedar.PendingRequest{RequestID: path + op, Family: cedar.FamilyFilesystem, Surface: cedar.PromptSurface{FS: &cedar.FSPromptSurface{Op: op, CanonicalPath: path, Dangerous: dangerous}}}
	}
	cases := []struct {
		req  cedar.PendingRequest
		want bool
	}{
		{mk("write", "/srv/p/b", false), true},
		{mk("write", "/srv/p/sub/c", false), true},
		{mk("write", "/srv/p/b", true), false},
		{mk("read", "/srv/p/b", false), false},
		{mk("write", "/srv/pp/b", false), false},
		{mk("recipe_dir_add", "/srv/p/b", false), false},
	}
	for _, c := range cases {
		if got := match(c.req); got != c.want {
			t.Errorf("covers(%s %s dangerous=%v) = %v, want %v", c.req.Surface.FS.Op, c.req.Surface.FS.CanonicalPath, c.req.Surface.FS.Dangerous, got, c.want)
		}
	}
	if g.GrantCoverage(granted, cedar.ScopeExact)(mk("write", "/srv/p/b", false)) {
		t.Error("exact grant released a sibling")
	}
}

// E2E-3: the harness's own state directory is labelled privileged and
// never remembered; a directory grant that would contain it narrows to
// the exact path; the agent workspace under DataDir is exempt.
func TestE2E3_StateDirIsPrivileged(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	workspace := filepath.Join(dataDir, "agent-workspace")
	rc := newRealChain(t, GateOptions{
		StateDirs:    HarnessStateDirs(dataDir),
		WorkspaceDir: func() string { return workspace },
	})
	if got := HarnessStateDirs(dataDir); len(got) != 2 || got[1] != filepath.Join(home, ".kenaz", "harness") {
		t.Fatalf("HarnessStateDirs = %v, want [dataDir, <home>/.kenaz/harness]", got)
	}

	t.Run("inside state: labelled, allowed once, not remembered", func(t *testing.T) {
		target := filepath.Join(dataDir, "policy", "evil.cedar")
		ch := rc.evalAsync(OpWrite, target)
		p := rc.waitPending(t, 1)[0]
		if !strings.Contains(p.Surface.FS.Privileged, "harness's own state directory") {
			t.Fatalf("Privileged = %q, want the state-directory banner", p.Surface.FS.Privileged)
		}
		if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
			t.Fatalf("ResolveScoped: %v", err)
		}
		d := mustAllow(t, ch, "privileged write")
		if !strings.Contains(d.Reason, "cannot be remembered") {
			t.Errorf("reason = %q, want it to say the grant was not remembered", d.Reason)
		}
		if entries, _ := os.ReadDir(rc.policyDir); len(entries) != 0 {
			t.Fatalf("policy dir has %d files after a privileged Allow-always, want 0", len(entries))
		}
	})

	t.Run("directory containing state narrows to exact", func(t *testing.T) {
		target := filepath.Join(home, ".kenaz", "notes.txt") // parent ~/.kenaz contains the state root
		ch := rc.evalAsync(OpWrite, target)
		p := rc.waitPending(t, 1)[0]
		if !strings.Contains(p.Surface.FS.Privileged, "exact path only") {
			t.Fatalf("Privileged = %q, want the narrowing banner", p.Surface.FS.Privileged)
		}
		_ = rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory)
		mustAllow(t, ch, "narrowed write")
		entries, _ := os.ReadDir(rc.policyDir)
		if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), "_path.cedar") {
			t.Fatalf("policy files = %v, want exactly one exact-path grant", entries)
		}
		// The state directory is still not writable without a prompt.
		ch = rc.evalAsync(OpWrite, filepath.Join(dataDir, "policy", "x.cedar"))
		p = rc.waitPending(t, 1)[0]
		_ = rc.registry.Resolve(p.RequestID, cedar.DecisionDeny)
		<-ch
	})

	t.Run("workspace under DataDir is not privileged", func(t *testing.T) {
		ch := rc.evalAsync(OpWrite, filepath.Join(workspace, "out.txt"))
		p := rc.waitPending(t, 1)[0]
		if p.Surface.FS.Privileged != "" {
			t.Fatalf("workspace path labelled privileged: %q", p.Surface.FS.Privileged)
		}
		_ = rc.registry.Resolve(p.RequestID, cedar.DecisionDeny)
		<-ch
	})
}

func TestStateRelation(t *testing.T) {
	t.Parallel()
	roots := []string{"/var/lib/kenaz-harness/.kenaz/harness/prod", "/var/lib/kenaz-harness/.kenaz/harness"}
	ws := "/var/lib/kenaz-harness/.kenaz/harness/prod/agent-workspace"
	cases := []struct {
		p                string
		inside, contains bool
	}{
		{"/var/lib/kenaz-harness", false, true}, // the E2E-3 recipe_dir_add target
		{"/var/lib/kenaz-harness/.kenaz/harness/prod/data.db", true, false},
		{"/var/lib/kenaz-harness/.kenaz/harness", true, false},
		{ws + "/proj", false, false},
		{"/home/kenaz", false, false},
	}
	for _, c := range cases {
		in, con, _ := StateRelation(c.p, roots, ws)
		if in != c.inside || con != c.contains {
			t.Errorf("StateRelation(%s) = (%v,%v), want (%v,%v)", c.p, in, con, c.inside, c.contains)
		}
	}
}

// "~" means HOME in Canonicalize, as it does to the bash tool's shell
// (E2E-2): previously "~/x" became "<cwd>/~/x".
func TestCanonicalize_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		"~":           home,
		"~/site/a.js": filepath.Join(home, "site", "a.js"),
	} {
		got, err := Canonicalize(in)
		if err != nil || got != want {
			t.Errorf("Canonicalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, _ := Canonicalize("~other/x"); strings.HasPrefix(got, home) {
		t.Errorf("~user form expanded to HOME: %q", got)
	}
}
