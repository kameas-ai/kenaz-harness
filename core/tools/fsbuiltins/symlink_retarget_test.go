package fsbuiltins_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
)

// retargetRig is a real gate (real Cedar engine over a policy dir, the
// real prompt registry) behind the write_file tool, with a responder
// that answers every prompt with the next scripted reply and records it.
type retargetRig struct {
	dataDir, policyDir string
	registry           *cedar.Registry
	engine             *cedar.Engine
	write              *fsbuiltins.WriteFileTool

	mu      sync.Mutex
	replies []func(cedar.PendingRequest)
	seen    []cedar.PendingRequest
	stop    chan struct{}
}

func newRetargetRig(t *testing.T) *retargetRig {
	t.Helper()
	home := t.TempDir()
	r := &retargetRig{dataDir: filepath.Join(home, ".kenaz", "harness", "prod"), stop: make(chan struct{})}
	r.policyDir = filepath.Join(r.dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(r.policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	r.registry = cedar.NewRegistry(cedar.WithTimeout(10 * time.Second))
	eng, err := cedar.NewEngine(cedar.Options{DataDir: r.dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	r.engine = eng
	gate := corefs.NewGate(corefs.GateOptions{
		Engine:    eng,
		Prompter:  &corefs.CedarPrompter{Registry: r.registry},
		PolicyDir: r.policyDir,
		StateDirs: corefs.HarnessStateDirs(r.dataDir),
	})
	r.registry.SetGrantCoverage(cedar.FamilyFilesystem, gate.GrantCoverage)
	r.write = fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: gate})
	done := make(chan struct{})
	t.Cleanup(func() { close(r.stop); <-done })
	go func() {
		defer close(done)
		for {
			select {
			case <-r.stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			for _, p := range r.registry.ListPending() {
				r.mu.Lock()
				r.seen = append(r.seen, p)
				var reply func(cedar.PendingRequest)
				if len(r.replies) > 0 {
					reply, r.replies = r.replies[0], r.replies[1:]
				}
				r.mu.Unlock()
				if reply == nil {
					t.Errorf("unexpected prompt for %+v", p.Surface.FS)
					_ = r.registry.Resolve(p.RequestID, cedar.DecisionDeny)
					continue
				}
				reply(p)
			}
		}
	}()
	return r
}

func (r *retargetRig) expect(reply func(cedar.PendingRequest)) {
	r.mu.Lock()
	r.replies = append(r.replies, reply)
	r.mu.Unlock()
}

func (r *retargetRig) prompts() []cedar.PendingRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cedar.PendingRequest(nil), r.seen...)
}

func (r *retargetRig) allowDirAlways(p cedar.PendingRequest) {
	_ = r.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory)
}

func (r *retargetRig) deny(p cedar.PendingRequest) {
	_ = r.registry.Resolve(p.RequestID, cedar.DecisionDeny)
}

func retarget(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// The reviewer's exploit, step by step, through the real gate and the
// real write_file:
//  1. ln -s /tmp/harmless /tmp/pl
//  2. write_file /tmp/pl/a.txt, answered "Allow always (this directory)"
//  3. ln -sfn <DataDir>/policy /tmp/pl
//  4. write_file /tmp/pl/evil.cedar
//
// Step 4 must prompt with the privileged banner (here denied) and nothing
// may land in the policy directory. A sibling write before the retarget
// still auto-allows: the grant works for the real directory it named.
func TestWriteFile_RememberedGrantDoesNotFollowRetargetedLink(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	harmless := filepath.Join(work, "harmless")
	if err := os.MkdirAll(harmless, 0o700); err != nil {
		t.Fatal(err)
	}
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(harmless, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	r.expect(r.allowDirAlways)
	if m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "a.txt"), "content": "a"}); isError(m) {
		t.Fatalf("step 2: %v", m["error"])
	}
	if m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "b.txt"), "content": "b"}); isError(m) {
		t.Fatalf("sibling under the granted real dir: %v", m["error"])
	}
	if n := len(r.prompts()); n != 1 {
		t.Fatalf("prompts = %d, want 1 (the sibling write is covered by the grant)", n)
	}

	retarget(t, r.policyDir, pl)
	r.expect(r.deny)
	m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "evil.cedar"), "content": "permit(principal, action, resource);"})
	if !isError(m) {
		t.Fatal("step 4: write behind the retargeted link was allowed")
	}
	ps := r.prompts()
	if len(ps) != 2 || !strings.Contains(ps[1].Surface.FS.Privileged, "harness's own state directory") {
		t.Fatalf("step 4 prompt = %+v, want one privileged prompt", ps)
	}
	if _, err := os.Stat(filepath.Join(r.policyDir, "evil.cedar")); !os.IsNotExist(err) {
		t.Fatalf("evil.cedar landed in the policy dir (stat err=%v)", err)
	}
}

// The grant binds the real directory: retargeted to a different ordinary
// directory, the link is no longer covered and the write prompts again.
func TestWriteFile_RememberedGrantBindsTheRealDirectory(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	harmless, other := filepath.Join(work, "harmless"), filepath.Join(work, "other")
	for _, d := range []string{harmless, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(harmless, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	r.expect(r.allowDirAlways)
	if m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "a.txt"), "content": "a"}); isError(m) {
		t.Fatalf("grant write: %v", m["error"])
	}
	retarget(t, other, pl)
	r.expect(r.deny)
	if m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "x.txt"), "content": "x"}); !isError(m) {
		t.Fatal("write behind a retargeted link was covered by the old grant")
	}
	if n := len(r.prompts()); n != 2 {
		t.Fatalf("prompts = %d, want 2", n)
	}
}

// A policy Allow that predates resolved-path binding (or any broad
// permit) never authorises harness state: the gate asks with the
// privileged banner instead.
func TestWriteFile_PolicyAllowDoesNotCoverState(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(r.policyDir, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	legacy := "permit (\n    principal == User::\"local\",\n    action == Action::\"write_filesystem\",\n    resource is FilesystemOp\n) when {\n    context.canonical_path like " +
		cedar.QuoteLikePrefix(pl+"/") + "\n};\n"
	if err := os.WriteFile(filepath.Join(r.policyDir, "fs_allow_write_legacy_dir.cedar"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.engine.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	r.expect(r.deny)
	if m := call(t, r.write, map[string]any{"path": filepath.Join(pl, "evil.cedar"), "content": "permit(principal, action, resource);"}); !isError(m) {
		t.Fatal("legacy lexical grant authorised a write into the policy dir")
	}
	ps := r.prompts()
	if len(ps) != 1 || !strings.Contains(ps[0].Surface.FS.Privileged, "harness's own state directory") {
		t.Fatalf("prompts = %+v, want one privileged prompt", ps)
	}
	if _, err := os.Stat(filepath.Join(r.policyDir, "evil.cedar")); !os.IsNotExist(err) {
		t.Fatalf("evil.cedar landed in the policy dir (stat err=%v)", err)
	}
}

// write_file never writes through a leaf link: the link itself is
// replaced (the location the gate judged), and its target is untouched.
func TestWriteFile_ReplacesLeafLinkNeverWritesThrough(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	realFile := filepath.Join(work, "real.txt")
	if err := os.WriteFile(realFile, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(work, "link.txt")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	if m := call(t, r.write, map[string]any{"path": link, "content": "new"}); isError(m) {
		t.Fatalf("write: %v", m["error"])
	}
	if b, _ := os.ReadFile(realFile); string(b) != "old" {
		t.Fatalf("target content = %q, want it untouched", b)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the link was not replaced by a regular file (lstat %v, err %v)", fi, err)
	}
}

// A path that cannot be resolved (a symlink loop) is refused by write_file
// even when the person allows it: there is no location to write.
func TestWriteFile_SymlinkLoopIsRefused(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	a, b := filepath.Join(work, "a"), filepath.Join(work, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	if m := call(t, r.write, map[string]any{"path": filepath.Join(a, "x.txt"), "content": "x"}); !isError(m) {
		t.Fatal("write through a symlink loop succeeded")
	}
	if ps := r.prompts(); len(ps) != 1 || ps[0].Surface.FS.Privileged == "" {
		t.Fatalf("prompts = %+v, want one privileged (fail-closed) prompt", ps)
	}
}

// An Allow once is for the location it was given on: after the link is
// retargeted, the same lexical path prompts again.
func TestWriteFile_AllowOnceDoesNotFollowRetargetedLink(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	harmless, other := filepath.Join(work, "harmless"), filepath.Join(work, "other")
	for _, d := range []string{harmless, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(harmless, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	target := filepath.Join(pl, "a.txt")
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	if m := call(t, r.write, map[string]any{"path": target, "content": "a"}); isError(m) {
		t.Fatalf("allow-once write: %v", m["error"])
	}
	if m := call(t, r.write, map[string]any{"path": target, "content": "a2"}); isError(m) {
		t.Fatalf("repeat under the transient grant: %v", m["error"])
	}
	retarget(t, other, pl)
	r.expect(r.deny)
	if m := call(t, r.write, map[string]any{"path": target, "content": "b"}); !isError(m) {
		t.Fatal("transient grant followed the retargeted link")
	}
	if n := len(r.prompts()); n != 2 {
		t.Fatalf("prompts = %d, want 2", n)
	}
}

// leafLinkRig plants proj/k -> an outside "sensitive" file (standing in
// for ~/.ssh/authorized_keys; a real HOME is never touched by tests).
func leafLinkRig(t *testing.T) (proj, link, outside string) {
	t.Helper()
	work := t.TempDir()
	proj = filepath.Join(work, "proj")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(t.TempDir(), "authorized_keys")
	if err := os.WriteFile(outside, []byte("ssh-ed25519 KEEP"), 0o600); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(proj, "k")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	return proj, link, outside
}

func mustUntouched(t *testing.T, outside string) {
	t.Helper()
	if b, _ := os.ReadFile(outside); string(b) != "ssh-ed25519 KEEP" {
		t.Fatalf("outside target modified: %q", b)
	}
}

// Reviewer case (a): under an old-format (lexical-only) directory grant on
// proj, write_file proj/k replaces the link and never writes through it.
func TestWriteFile_LeafLinkUnderLegacyGrantNeverWritesThrough(t *testing.T) {
	r := newRetargetRig(t)
	proj, link, outside := leafLinkRig(t)
	legacy := "permit (\n    principal == User::\"local\",\n    action == Action::\"write_filesystem\",\n    resource is FilesystemOp\n) when {\n    context.canonical_path like " +
		cedar.QuoteLikePrefix(proj+"/") + "\n};\n"
	if err := os.WriteFile(filepath.Join(r.policyDir, "fs_allow_write_legacy_proj.cedar"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.engine.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if m := call(t, r.write, map[string]any{"path": link, "content": "attacker-key"}); isError(m) {
		t.Fatalf("write: %v", m["error"])
	}
	mustUntouched(t, outside)
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("link not replaced (lstat %v, err %v)", fi, err)
	}
}

// Reviewer case (b): with no grant, the prompt is for the link's own
// location (no "points to" a target that is never written) and the
// target is untouched whatever the answer.
func TestWriteFile_LeafLinkWithoutGrantNeverWritesThrough(t *testing.T) {
	r := newRetargetRig(t)
	_, link, outside := leafLinkRig(t)
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	if m := call(t, r.write, map[string]any{"path": link, "content": "attacker-key"}); isError(m) {
		t.Fatalf("write: %v", m["error"])
	}
	mustUntouched(t, outside)
	ps := r.prompts()
	if len(ps) != 1 {
		t.Fatalf("prompts = %d, want 1", len(ps))
	}
	if rp := ps[0].Surface.FS.ResolvedPath; rp != "" && filepath.Base(rp) != "k" {
		t.Fatalf("prompt points to %q; the write acts on the link itself", rp)
	}
}

// edit_file refuses a leaf link instead of editing through it.
func TestEditFile_RefusesLeafLink(t *testing.T) {
	r := newRetargetRig(t)
	_, link, outside := leafLinkRig(t)
	gate := corefs.NewGate(corefs.GateOptions{Engine: r.engine, Prompter: &corefs.CedarPrompter{Registry: r.registry}, StateDirs: corefs.HarnessStateDirs(r.dataDir)})
	rs := fsbuiltins.NewReadSet()
	rs.Add("", link)
	edit := fsbuiltins.NewEditFileTool(fsbuiltins.Options{Gate: gate, ReadSet: rs})
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	m := call(t, edit, map[string]any{"path": link, "old_str": "KEEP", "new_str": "attacker"})
	if !isError(m) || !strings.Contains(m["error"].(string), "link") {
		t.Fatalf("edit through a link = %v, want a link refusal", m)
	}
	mustUntouched(t, outside)
}

// An exact-path Allow always binds the location it was given on: after
// the parent link is retargeted to another ordinary dir, the same lexical
// path prompts again (the exact snippet's resolved_path clause alone).
func TestWriteFile_ExactGrantDoesNotFollowRetargetedLink(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	harmless, other := filepath.Join(work, "harmless"), filepath.Join(work, "other")
	for _, d := range []string{harmless, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(harmless, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	target := filepath.Join(pl, "a.txt")
	r.expect(func(p cedar.PendingRequest) {
		_ = r.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeExact)
	})
	if m := call(t, r.write, map[string]any{"path": target, "content": "a"}); isError(m) {
		t.Fatalf("grant write: %v", m["error"])
	}
	if m := call(t, r.write, map[string]any{"path": target, "content": "a2"}); isError(m) {
		t.Fatalf("repeat under the exact grant: %v", m["error"])
	}
	retarget(t, other, pl)
	r.expect(r.deny)
	if m := call(t, r.write, map[string]any{"path": target, "content": "b"}); !isError(m) {
		t.Fatal("exact grant followed the retargeted link")
	}
	if n := len(r.prompts()); n != 2 {
		t.Fatalf("prompts = %d, want 2", n)
	}
}

// read_file through a link reads the location the gate judged and shows
// it in the prompt.
func TestReadFile_ThroughLinkShowsTarget(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	realFile := filepath.Join(work, "real.txt")
	if err := os.WriteFile(realFile, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "k")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	gate := corefs.NewGate(corefs.GateOptions{Engine: r.engine, Prompter: &corefs.CedarPrompter{Registry: r.registry}, StateDirs: corefs.HarnessStateDirs(r.dataDir)})
	read := fsbuiltins.NewReadFileTool(fsbuiltins.Options{Gate: gate})
	r.expect(func(p cedar.PendingRequest) { _ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
	if m := call(t, read, map[string]any{"path": link}); isError(m) {
		t.Fatalf("read: %v", m["error"])
	}
	ps := r.prompts()
	if len(ps) != 1 || filepath.Base(ps[0].Surface.FS.ResolvedPath) != "real.txt" {
		t.Fatalf("prompts = %+v, want one prompt pointing to real.txt", ps)
	}
}

// The I/O uses the location the gate judged: a link retargeted while the
// prompt is open (between the check and the I/O) does not redirect it.
func TestReadWrite_IOUsesTheCheckedLocation(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	checked, swapped := filepath.Join(work, "checked"), filepath.Join(work, "swapped")
	for _, d := range []string{checked, swapped} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f.txt"), []byte(filepath.Base(d)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pl := filepath.Join(work, "pl")
	if err := os.Symlink(checked, pl); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	gate := corefs.NewGate(corefs.GateOptions{Engine: r.engine, Prompter: &corefs.CedarPrompter{Registry: r.registry}, StateDirs: corefs.HarnessStateDirs(r.dataDir)})
	read := fsbuiltins.NewReadFileTool(fsbuiltins.Options{Gate: gate})
	write := fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: gate})
	swapThenAllow := func(to string) func(cedar.PendingRequest) {
		return func(p cedar.PendingRequest) {
			retarget(t, to, pl)
			_ = r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce)
		}
	}

	r.expect(swapThenAllow(swapped))
	m := call(t, read, map[string]any{"path": filepath.Join(pl, "f.txt")})
	if isError(m) {
		t.Fatalf("read: %v", m["error"])
	}
	if c, _ := m["content"].(string); !strings.Contains(c, "checked") {
		t.Fatalf("read returned %v, want the checked file's content", m)
	}

	r.expect(swapThenAllow(checked)) // pl -> swapped at check time; swapped back mid-prompt
	if m := call(t, write, map[string]any{"path": filepath.Join(pl, "new.txt"), "content": "w"}); isError(m) {
		t.Fatalf("write: %v", m["error"])
	}
	if _, err := os.Stat(filepath.Join(swapped, "new.txt")); err != nil {
		t.Fatalf("write did not land in the checked directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(checked, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("write followed the link swapped after the check (stat err=%v)", err)
	}
}

// legacyReadGrant writes an old-format (canonical_path only) read grant on
// dir and reloads the engine.
func (r *retargetRig) legacyReadGrant(t *testing.T, dir string) {
	t.Helper()
	body := "permit (\n    principal == User::\"local\",\n    action == Action::\"read_filesystem\",\n    resource is FilesystemOp\n) when {\n    context.canonical_path == " +
		cedar.QuoteString(dir) + " ||\n    context.canonical_path like " + cedar.QuoteLikePrefix(dir+"/") + "\n};\n"
	if err := os.WriteFile(filepath.Join(r.policyDir, "fs_allow_read_legacy_dir.cedar"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.engine.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

// An old-format grant on proj covers the lexical path only: a read through
// a link in proj that lands outside it must prompt (with "Points to"),
// while a link that lands inside proj is still covered.
func TestRead_LegacyGrantDoesNotCoverLinksOutOfTheDir(t *testing.T) {
	r := newRetargetRig(t)
	work := t.TempDir()
	proj := filepath.Join(work, "proj")
	docs := filepath.Join(t.TempDir(), "Documents")
	for _, d := range []string{proj, docs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(docs, "secret.txt")
	inside := filepath.Join(proj, "real.txt")
	for _, f := range []string{secret, inside} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"s": secret, "d": docs, "in": inside} {
		if err := os.Symlink(target, filepath.Join(proj, link)); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	}
	r.legacyReadGrant(t, proj)
	gate := corefs.NewGate(corefs.GateOptions{Engine: r.engine, Prompter: &corefs.CedarPrompter{Registry: r.registry}, StateDirs: corefs.HarnessStateDirs(r.dataDir)})
	read := fsbuiltins.NewReadFileTool(fsbuiltins.Options{Gate: gate})
	list := fsbuiltins.NewListDirTool(fsbuiltins.Options{Gate: gate})

	// Covered: a real file and a link that lands inside proj.
	for _, p := range []string{inside, filepath.Join(proj, "in")} {
		if m := call(t, read, map[string]any{"path": p}); isError(m) {
			t.Fatalf("read %s under the grant: %v", p, m["error"])
		}
	}
	if n := len(r.prompts()); n != 0 {
		t.Fatalf("prompts = %d, want 0 for paths the grant really covers", n)
	}

	// Not covered: links that land outside proj.
	r.expect(r.deny)
	if m := call(t, read, map[string]any{"path": filepath.Join(proj, "s")}); !isError(m) {
		t.Fatal("read_file through proj/s -> Documents/secret.txt was allowed by the proj grant")
	}
	r.expect(r.deny)
	if m := call(t, list, map[string]any{"path": filepath.Join(proj, "d")}); !isError(m) {
		t.Fatal("list_dir through proj/d -> Documents was allowed by the proj grant")
	}
	ps := r.prompts()
	if len(ps) != 2 {
		t.Fatalf("prompts = %d, want 2", len(ps))
	}
	for _, p := range ps {
		if p.Surface.FS.ResolvedPath == "" {
			t.Errorf("prompt for %s has no Points to", p.Surface.FS.CanonicalPath)
		}
	}
}
