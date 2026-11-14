package fs

// Fleet e2e 2026-11-07 regressions (E2E-1 / E2E-3), driven through the
// REAL chain: Gate.Evaluate -> CedarPrompter -> cedar.Registry ->
// a disk-backed cedar.Engine. No stub engine: the original bug was that
// a written grant was never re-read, which a stub cannot see.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
		// snippetName's scope is the suffix, or — when the path is long,
		// as macOS temp dirs are — sits before a hash: fs_allow_write_path_<hash>.
		if len(entries) != 1 || !(strings.HasSuffix(entries[0].Name(), "_path.cedar") ||
			strings.HasPrefix(entries[0].Name(), "fs_allow_write_path_")) {
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

// E2E-3 aliasing: the state classification compares as the filesystem
// sees paths, not as bytes. A case variant on a case-insensitive volume
// (APFS, NTFS) names the same state directory.
func TestStateRelation_CaseVariantIsState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	if err := os.MkdirAll(filepath.Join(dataDir, "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(home, ".KENAZ", "harness", "prod", "policy")
	if _, err := os.Stat(variant); err != nil {
		t.Skipf("case-sensitive filesystem, no case alias to test: %v", err)
	}
	if in, _, _ := StateRelation(variant, HarnessStateDirs(dataDir), ""); !in {
		t.Fatalf("StateRelation(%s) inside=false, want true (case alias of the state dir)", variant)
	}
	g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(dataDir)}}
	if p := g.privilege(filepath.Join(variant, "evil.cedar")); !p.inside {
		t.Fatalf("gate privilege for a case variant = %+v, want inside", p)
	}
}

// A state dir configured through a symlink is still recognised when the
// path names its real target, and the other way round.
func TestGatePrivilege_SymlinkAliasesOfStateDir(t *testing.T) {
	t.Parallel()
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".kenaz", "harness", "prod", "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(realHome, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	viaLink := filepath.Join(link, ".kenaz", "harness", "prod")
	viaReal := filepath.Join(realHome, ".kenaz", "harness", "prod")
	for _, c := range []struct{ name, stateDir, target string }{
		{"real target of a linked state dir", viaLink, filepath.Join(viaReal, "policy", "evil.cedar")},
		{"linked path to a real state dir", viaReal, filepath.Join(viaLink, "policy", "evil.cedar")},
	} {
		g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(c.stateDir)}}
		if p := g.privilege(c.target); !p.inside {
			t.Errorf("%s: privilege = %+v, want inside", c.name, p)
		}
		if in, _, _ := StateRelation(c.target, HarnessStateDirs(c.stateDir), ""); !in {
			t.Errorf("%s: StateRelation inside=false, want true", c.name)
		}
	}
	// A file whose directory contains the state dir, named through the
	// other alias, still narrows a directory grant (dirCoversState), and
	// that directory itself "contains" state for recipe_dir_add.
	g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(viaLink)}}
	if p := g.privilege(filepath.Join(realHome, ".kenaz", "notes.txt")); !p.dirCoversState {
		t.Errorf("sibling of the state tree via the real path: privilege = %+v, want dirCoversState", p)
	}
	if in, con, _ := StateRelation(filepath.Join(realHome, ".kenaz"), HarnessStateDirs(viaLink), ""); in || !con {
		t.Errorf("ancestor of a linked state dir: StateRelation = (%v,%v), want (false,true)", in, con)
	}
}

// Case is folded lexically too on macOS and Windows, so a case variant is
// state even when nothing exists on disk to compare with os.SameFile.
func TestStateRelation_CaseFoldedWithoutDisk(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("case is significant on this platform")
	}
	roots := []string{"/nonexistent-kz/.kenaz/harness/prod"}
	if in, _, _ := StateRelation("/NONEXISTENT-KZ/.KENAZ/harness/prod/policy", roots, ""); !in {
		t.Fatal("case variant not classed as state")
	}
}

// The workspace exemption still holds when the workspace is configured
// through a symlink and the path names its real location under DataDir.
func TestGatePrivilege_WorkspaceExemptBehindSymlink(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	ws := filepath.Join(dataDir, "agent-workspace")
	if err := os.MkdirAll(filepath.Join(ws, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	wsLink := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(ws, wsLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	target := filepath.Join(ws, "proj", "main.go")
	g := &Gate{opts: GateOptions{
		StateDirs:    HarnessStateDirs(dataDir),
		WorkspaceDir: func() string { return wsLink },
	}}
	if p := g.privilege(target); p.inside || p.dirCoversState {
		t.Fatalf("workspace file behind a symlinked workspace classed privileged: %+v", p)
	}
	if in, con, _ := StateRelation(target, HarnessStateDirs(dataDir), wsLink); in || con {
		t.Fatalf("StateRelation = (%v,%v), want workspace exemption", in, con)
	}
	// Control: the same file is state without the exemption.
	if in, _, _ := StateRelation(target, HarnessStateDirs(dataDir), ""); !in {
		t.Fatal("control: workspace file under DataDir should be state without the exemption")
	}
}

// A workspace that CONTAINS the state dir (workspace = HOME, the usual
// desktop shape) exempts nothing: the state below it stays state. Only a
// workspace inside a state dir (the fallback <DataDir>/agent-workspace)
// exempts its own paths. No disk needed.
func TestStateRelation_WorkspaceContainingStateExemptsNothing(t *testing.T) {
	t.Parallel()
	home := "/nx-home/u"
	roots := HarnessStateDirs(home + "/.kenaz/harness/prod")
	cases := []struct {
		p                string
		inside, contains bool
	}{
		{home + "/.kenaz/harness/prod/policy", true, false},
		{home + "/.kenaz/harness/prod/data.db", true, false},
		{home + "/.kenaz/harness/dev", true, false},
		{home + "/.kenaz", false, true},
		{home, false, true},
		{home + "/proj", false, false},
	}
	for _, c := range cases {
		in, con, _ := StateRelation(c.p, roots, home)
		if in != c.inside || con != c.contains {
			t.Errorf("StateRelation(%s, ws=HOME) = (%v,%v), want (%v,%v)", c.p, in, con, c.inside, c.contains)
		}
	}
	// The fallback workspace inside DataDir is still exempt.
	ws := home + "/.kenaz/harness/prod/agent-workspace"
	if in, con, _ := StateRelation(ws+"/proj/x", roots, ws); in || con {
		t.Errorf("fallback workspace path = (%v,%v), want exempt", in, con)
	}
}

// The workspace exemption does not fold case lexically (the fail-open
// side): on a path that is not on disk, a case look-alike of the fallback
// workspace is still state.
func TestStateRelation_WorkspaceExemptionIsNotCaseFolded(t *testing.T) {
	t.Parallel()
	if in, _, _ := StateRelation("/nx-data/WS/x", []string{"/nx-data"}, "/nx-data/ws"); !in {
		t.Fatal("case look-alike of the workspace was exempted from state")
	}
}

// At the gate, with workspace = HOME: a write into the policy dir is
// labelled privileged and an Allow always is not remembered; a file
// beside the state tree narrows a directory grant.
func TestE2E3_WorkspaceHomeDoesNotExemptState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	if err := os.MkdirAll(filepath.Join(dataDir, "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(dataDir), WorkspaceDir: func() string { return home }}}
	if p := g.privilege(filepath.Join(dataDir, "policy", "evil.cedar")); !p.inside {
		t.Fatalf("policy file with workspace=HOME: privilege = %+v, want inside", p)
	}
	if p := g.privilege(filepath.Join(home, ".kenaz", "notes.txt")); !p.dirCoversState {
		t.Fatalf("sibling of the state tree with workspace=HOME: privilege = %+v, want dirCoversState", p)
	}
	if p := g.privilege(filepath.Join(home, "proj", "main.go")); p.inside || p.dirCoversState {
		t.Fatalf("ordinary workspace file: privilege = %+v, want none", p)
	}

	rc := newRealChain(t, GateOptions{
		StateDirs:    HarnessStateDirs(dataDir),
		WorkspaceDir: func() string { return home },
	})
	ch := rc.evalAsync(OpWrite, filepath.Join(dataDir, "policy", "evil.cedar"))
	p := rc.waitPending(t, 1)[0]
	if !strings.Contains(p.Surface.FS.Privileged, "harness's own state directory") {
		t.Fatalf("Privileged = %q, want the state-directory banner", p.Surface.FS.Privileged)
	}
	if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatalf("ResolveScoped: %v", err)
	}
	mustAllow(t, ch, "privileged write")
	if entries, _ := os.ReadDir(rc.policyDir); len(entries) != 0 {
		t.Fatalf("policy dir has %d files after a privileged Allow-always, want 0", len(entries))
	}
}

// symlinkOrSkip plants link -> target, skipping where symlinks can't be made.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
}

// A NEW file behind a planted symlink into the state dir is state. The
// file does not exist yet, so EvalSymlinks on the whole path fails; the
// classification must resolve the deepest existing ancestor instead.
// Exploit shape: bash `ln -s <DataDir>/policy /tmp/pl`, then
// write_file /tmp/pl/evil.cedar with a Cedar permit.
func TestGatePrivilege_NewFileBehindSymlinkIntoState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	policy := filepath.Join(dataDir, "policy")
	if err := os.MkdirAll(policy, 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	planted := filepath.Join(elsewhere, "pl")
	symlinkOrSkip(t, policy, planted)
	hop2 := filepath.Join(elsewhere, "hop2")
	symlinkOrSkip(t, policy, hop2)
	hop1 := filepath.Join(elsewhere, "hop1")
	symlinkOrSkip(t, hop2, hop1)
	dangling := filepath.Join(elsewhere, "dangling.cedar")
	symlinkOrSkip(t, filepath.Join(policy, "evil.cedar"), dangling) // target not created yet
	relDangling := filepath.Join(elsewhere, "rel")
	rel, err := filepath.Rel(elsewhere, filepath.Join(policy, "missing-dir"))
	if err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, rel, relDangling)

	// A relative dangling link with ".." reached through a symlinked
	// directory: its target is relative to the directory it really lives
	// in, not to the lexical path used to reach it.
	realDir := filepath.Join(home, "a", "b")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	relUp, err := filepath.Rel(realDir, filepath.Join(policy, "missing-dir"))
	if err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, relUp, filepath.Join(realDir, "rl"))
	viaDir := filepath.Join(t.TempDir(), "ld")
	symlinkOrSkip(t, realDir, viaDir)

	g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(dataDir)}}
	for name, p := range map[string]string{
		"relative dangling link reached through a linked dir": filepath.Join(viaDir, "rl", "evil.cedar"),
		"new file behind a planted link":                      filepath.Join(planted, "evil.cedar"),
		"new file directly in the state dir":                  filepath.Join(policy, "new.cedar"),
		"new file behind a link chain":                        filepath.Join(hop1, "evil.cedar"),
		"dangling link to a new state file":                   dangling,
		"new file behind a relative dangling link":            filepath.Join(relDangling, "evil.cedar"),
	} {
		if pr := g.privilege(p); !pr.inside {
			t.Errorf("%s (%s): privilege = %+v, want inside", name, p, pr)
		}
		if in, _, _ := StateRelation(p, HarnessStateDirs(dataDir), ""); !in {
			t.Errorf("%s (%s): StateRelation inside=false, want true", name, p)
		}
	}
	// Control: an ordinary new file elsewhere is not state.
	if pr := g.privilege(filepath.Join(elsewhere, "notes.txt")); pr.inside || pr.dirCoversState {
		t.Errorf("ordinary new file: privilege = %+v, want none", pr)
	}

	// End to end at the gate: the prompt carries the privileged banner
	// and an Allow always is not remembered.
	rc := newRealChain(t, GateOptions{StateDirs: HarnessStateDirs(dataDir)})
	ch := rc.evalAsync(OpWrite, filepath.Join(planted, "evil.cedar"))
	pending := rc.waitPending(t, 1)[0]
	if !strings.Contains(pending.Surface.FS.Privileged, "harness's own state directory") {
		t.Fatalf("Privileged = %q, want the state-directory banner", pending.Surface.FS.Privileged)
	}
	if err := rc.registry.ResolveScoped(pending.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatalf("ResolveScoped: %v", err)
	}
	mustAllow(t, ch, "privileged write")
	if entries, _ := os.ReadDir(rc.policyDir); len(entries) != 0 {
		t.Fatalf("policy dir has %d files after a privileged Allow-always, want 0", len(entries))
	}
}

// A symlink INSIDE the fallback workspace that points into the state dir
// does not carry the workspace exemption: the exemption judges a path by
// where it really lands.
func TestGatePrivilege_LinkInFallbackWorkspaceIntoStateIsState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	policy := filepath.Join(dataDir, "policy")
	ws := filepath.Join(dataDir, "agent-workspace")
	for _, d := range []string{policy, ws} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(ws, "pl")
	symlinkOrSkip(t, policy, link)
	g := &Gate{opts: GateOptions{StateDirs: HarnessStateDirs(dataDir), WorkspaceDir: func() string { return ws }}}
	if pr := g.privilege(filepath.Join(link, "evil.cedar")); !pr.inside {
		t.Fatalf("link in the workspace into policy: privilege = %+v, want inside", pr)
	}
	if in, _, _ := StateRelation(filepath.Join(link, "evil.cedar"), HarnessStateDirs(dataDir), ws); !in {
		t.Fatal("StateRelation: link in the workspace into policy not classed as state")
	}
	// Control: ordinary (new) workspace files stay exempt.
	if pr := g.privilege(filepath.Join(ws, "proj", "new.txt")); pr.inside || pr.dirCoversState {
		t.Fatalf("ordinary workspace file: privilege = %+v, want none", pr)
	}
}
