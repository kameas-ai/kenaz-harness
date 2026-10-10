package mlproducer

// wp07_test.go — ml-producer-01MLPRD01 WP07 items 1–3 at the recorder,
// over real sqlite: reads as `file` events, background bash exits, and
// the branch-length placeholder.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
)

func filePaths(recs []decoded) []string {
	var out []string
	for _, r := range events(recs, KindFile) {
		p := r.event.Payload
		if p["path"] != p["file"] {
			panic(fmt.Sprintf("file event path %v != file %v", p["path"], p["file"]))
		}
		out = append(out, p["path"].(string))
	}
	return out
}

// read_file: agent.tool row AND one file event; a failed read stays
// agent.tool only; reads never reach tasks.files; phase stays exploring.
func TestWP07_ReadFileShipsFileEventPlusAgentTool(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__read_file", `{"path":"pkg/a.go"}`, coreag.ToolOutcomeOK, `{"content":"x","byte_size":1,"truncated":false}`)
	h.call(ctx, "s1", "kenaz__read_file", `{"path":"missing.go"}`, coreag.ToolOutcomeOK, `{"is_error":true,"error":"no such file"}`)
	h.call(ctx, "s1", "kenaz__read_file", `{"path":"denied.go"}`, coreag.ToolOutcomeDenied, "")
	recs := h.outbox()
	if got := len(events(recs, KindTool)); got != 3 {
		t.Fatalf("agent.tool = %d, want 3 (every read keeps its row)", got)
	}
	want := []string{h.hash(workspace+"/pkg/a.go") + ".go"}
	if got := filePaths(recs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("file events = %v, want %v (only the successful read)", got, want)
	}
	for _, r := range events(recs, KindFile) {
		if len(r.event.Payload) != 3 || r.event.Payload["task"] != h.taskID("s1") {
			t.Errorf("file payload = %v, want exactly {task,path,file}", r.event.Payload)
		}
	}
	task := lastTask(t, recs)
	if len(task.Files) != 0 {
		t.Errorf("reads counted into tasks.files: %v", task.Files)
	}
	ph := events(recs, KindPhaseChange)
	if len(ph) != 1 || ph[0].event.Payload["phase"] != "exploring" {
		t.Errorf("phase changes = %v, want one exploring", ph)
	}
}

// grep: paths from the RESULT (deduped, result order), relative ones
// resolved against the grep root; capped at 20 per call.
func TestWP07_GrepResultPathsCappedAt20(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	var b strings.Builder
	b.WriteString(`{"matches":[`)
	var wantAbs []string
	for i := 0; i < 25; i++ {
		f := fmt.Sprintf("/Users/alice/acme-secret-repo/src/f%02d.ts", i)
		if i == 3 {
			f = "rel/inside.py" // relative: resolves against the grep root
		}
		for rep := 0; rep < 2; rep++ { // two matching lines per file
			if b.Len() > len(`{"matches":[`) {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"file":%q,"line":%d,"content":"zz secret line"}`, f, rep+1)
		}
		abs := f
		if i == 3 {
			abs = workspace + "/sub/rel/inside.py"
		}
		wantAbs = append(wantAbs, abs)
	}
	b.WriteString(`],"truncated":false}`)
	// The args name a different path than the results: paths come from the
	// result, the arg is only the root.
	h.call(context.Background(), "s1", "kenaz__grep", `{"pattern":"secret","path":"sub"}`, coreag.ToolOutcomeOK, b.String())
	recs := h.outbox()
	got := filePaths(recs)
	if len(got) != maxReadFiles {
		t.Fatalf("file events = %d, want the cap %d", len(got), maxReadFiles)
	}
	for i, abs := range wantAbs[:maxReadFiles] {
		ext := strings.TrimPrefix(filepath.Ext(abs), ".")
		if want := h.hash(abs) + "." + ext; got[i] != want {
			t.Errorf("file[%d] = %s, want h(%s)", i, got[i], abs)
		}
	}
	if n := len(events(recs, KindTool)); n != 1 {
		t.Errorf("agent.tool = %d, want 1", n)
	}
}

// glob (absolute + relative-to-base_dir) and list_dir (files only,
// relative to the listed directory); exclusions drop a path before it is
// hashed.
func TestWP07_GlobAndListDirAndExclusions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.rec.SetExclusions(mustCompile(t, Exclusions{Paths: []string{"hr/**"}}))
	h.call(ctx, "s1", "kenaz__glob", `{"pattern":"**/*.go","base_dir":"/srv/proj"}`, coreag.ToolOutcomeOK,
		`{"matches":["/srv/proj/a.go","b/c.go","/srv/proj/hr/pay.go"],"truncated":false}`)
	h.call(ctx, "s1", "kenaz__list_dir", `{"path":"docs"}`, coreag.ToolOutcomeOK,
		`{"entries":[{"name":"x.md","type":"file","size":1,"path":"x.md"},{"name":"sub","type":"dir","size":0,"path":"sub"},`+
			`{"name":"ln","type":"symlink","size":0,"path":"ln"},{"name":"y.txt","type":"file","size":1,"path":"sub/y.txt"}],"truncated":false}`)
	h.call(ctx, "s1", "kenaz__list_dir", `{"path":"docs"}`, coreag.ToolOutcomeError, `{"is_error":true,"error":"x"}`)
	recs := h.outbox()
	want := []string{
		h.hash("/srv/proj/a.go") + ".go",
		h.hash("/srv/proj/b/c.go") + ".go",
		h.hash(workspace+"/docs/x.md") + ".md",
		h.hash(workspace+"/docs/sub/y.txt") + ".txt",
	}
	if got := filePaths(recs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("file events =\n %v\nwant\n %v", got, want)
	}
}

func spawnResult(id string) string { return `{"task_id":"` + id + `","status":"running"}` }

// A background test that fails: terminal at spawn (no exit_code), then a
// follow-up terminal with exit_code 1 and test_fails +1 — attributed by
// the SPAWN's attendedness, although the exit ctx is unattended.
func TestWP07_BackgroundExitFollowUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./internal/zzsecret/...","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-1"))
	h.flush()
	h.clock.advance(time.Minute)
	h.rec.BackgroundEnded(runposture.Unattended(context.Background()), "bg-1", 1)
	h.rec.BackgroundEnded(ctx, "bg-unknown", 3) // never spawned here: ignored
	recs := h.outbox()
	terms := events(recs, KindTerminal)
	if len(terms) != 2 {
		t.Fatalf("terminal events = %d, want spawn + follow-up", len(terms))
	}
	if _, has := terms[0].event.Payload["exit_code"]; has {
		t.Error("spawn terminal carried an exit_code")
	}
	f := terms[1].event.Payload
	if f["cmd"] != "go test" || f["exit_code"] != float64(1) || f["task"] != h.taskID("s1") || len(f) != 3 {
		t.Errorf("follow-up terminal = %v, want {task, cmd=go test, exit_code=1}", f)
	}
	row, _, _ := h.store.LoadTask(ctx, h.taskID("s1"))
	if row.TestRuns != 1 || row.TestFails != 1 {
		t.Errorf("counters = %d/%d, want 1/1", row.TestRuns, row.TestFails)
	}
	if err := h.rec.FlushTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if task := lastTask(t, h.outbox()); task.TestFails != 1 {
		t.Errorf("task upsert test_fails = %d, want 1", task.TestFails)
	}
	h.rec.mu.Lock()
	pending, parked := len(h.rec.bgPending), len(h.rec.bgParked)
	h.rec.mu.Unlock()
	if pending != 0 || parked != 1 {
		t.Errorf("pending/parked = %d/%d, want 0/1 (the unknown exit parked)", pending, parked)
	}
}

// The exit can land before the spawn's observer call; it is parked and
// matched. An exit-0 background commit counts like a foreground one.
func TestWP07_BackgroundExitBeforeSpawnAndCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.rec.BackgroundEnded(ctx, "bg-fast", 0)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m wip","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-fast"))
	recs := h.outbox()
	terms := events(recs, KindTerminal)
	if len(terms) != 2 || terms[1].event.Payload["exit_code"] != float64(0) {
		t.Fatalf("terminals = %v, want spawn + follow-up exit 0", terms)
	}
	if n := len(events(recs, KindCommit)); n != 1 {
		t.Errorf("commit events = %d, want 1", n)
	}
	if row, _, _ := h.store.LoadTask(ctx, h.taskID("s1")); row.CommitCount != 1 || row.TestFails != 0 {
		t.Errorf("row = %+v, want commit_count 1", row)
	}
}

// Excluded spawns, unattended unlinked spawns and a closed gate ship no
// follow-up; a purge forgets pending spawns.
func TestWP07_BackgroundExitSuppressed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.rec.SetExclusions(mustCompile(t, Exclusions{Commands: []string{"ssh"}}))
	h.call(ctx, "s1", "kenaz__bash", `{"command":"ssh prod 'go test'","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-ex"))
	h.call(runposture.Unattended(ctx), "sched", "kenaz__bash", `{"command":"go test ./...","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-sched"))
	h.flush()
	h.rec.BackgroundEnded(ctx, "bg-ex", 1)
	h.rec.BackgroundEnded(ctx, "bg-sched", 1)
	recs := h.outbox()
	if n := len(events(recs, KindTerminal)); n != 0 {
		t.Fatalf("terminal events = %d, want 0", n)
	}
	h.rec.mu.Lock()
	pending := len(h.rec.bgPending)
	h.rec.mu.Unlock()
	if pending != 0 {
		t.Errorf("pending = %d after the excluded exit, want 0", pending)
	}

	h.rec.SetExclusions(nil)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./...","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-purged"))
	h.flush()
	if err := h.rec.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	h.rec.BackgroundEnded(ctx, "bg-purged", 1)
	if n := len(events(h.outbox(), KindTerminal)); n != 0 {
		t.Fatalf("a purged spawn shipped %d terminal(s)", n)
	}

	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./...","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-closed"))
	h.flush()
	h.gate.Store(false)
	h.rec.BackgroundEnded(ctx, "bg-closed", 1)
	h.flush()
	h.gate.Store(true)
	if n := len(events(h.outbox(), KindTerminal)); n != 1 {
		t.Fatalf("terminal events = %d, want only the spawn (gate closed at exit)", n)
	}
}

// Bounded memory: entries older than 24h go, then the oldest past the cap.
func TestWP07_PruneByAge(t *testing.T) {
	t.Parallel()
	now := int64(100 * 24 * time.Hour / time.Millisecond)
	m := map[string]bgPending{"old": {at: now - bgMaxAge.Milliseconds() - 1}}
	for i := 0; i < bgMaxPending; i++ {
		m[fmt.Sprint("k", i)] = bgPending{at: now - int64(bgMaxPending-i)}
	}
	pruneByAge(m, now, func(e bgPending) int64 { return e.at }, bgMaxPending)
	if _, ok := m["old"]; ok {
		t.Error("a 24h-old entry survived")
	}
	if len(m) != bgMaxPending-1 {
		t.Errorf("len = %d, want %d (room for one more)", len(m), bgMaxPending-1)
	}
	if _, ok := m["k0"]; ok {
		t.Error("the oldest entry survived the cap")
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWP07_ReadGitBranch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feature/zz-ünïcode\n")
	if err := os.MkdirAll(filepath.Join(repo, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Linked worktree: .git is a file pointing at the per-worktree git dir.
	wt := filepath.Join(root, "wt")
	writeFile(t, filepath.Join(repo, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/wt-branch\n")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: ../repo/.git/worktrees/wt\n")
	detached := filepath.Join(root, "detached")
	writeFile(t, filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	other := filepath.Join(root, "other")
	writeFile(t, filepath.Join(other, ".git", "HEAD"), "ref: refs/remotes/origin/main\n")
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dir, want string }{
		{repo, "feature/zz-ünïcode"},
		{filepath.Join(repo, "a", "b"), "feature/zz-ünïcode"},
		{wt, "wt-branch"},
		{detached, ""},
		{other, ""},
		{"", ""},
	} {
		if got := ReadGitBranch(c.dir); got != c.want {
			t.Errorf("ReadGitBranch(%s) = %q, want %q", c.dir, got, c.want)
		}
	}
	if got := branchPlaceholder("feature/zz-ünïcode"); got != strings.Repeat("x", 18) {
		t.Errorf("placeholder = %q, want 18 x (runes, not bytes)", got)
	}
}

// tasks.branch is "x"×runes of the workspace's branch, re-read (throttled)
// on upsert; never the name.
func TestWP07_TaskBranchPlaceholder(t *testing.T) {
	t.Parallel()
	ws := filepath.Join(t.TempDir(), "zz-ws")
	head := filepath.Join(ws, ".git", "HEAD")
	writeFile(t, head, "ref: refs/heads/zz-secret-ünï\n")
	dir := t.TempDir()
	db, store := openDB(t, dir)
	clock := &fakeClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	rec := NewRecorder(Config{Store: store, Hasher: NewHasher(dir), Gate: GateFunc(func() bool { return true }),
		Workspace: func() string { return ws }, Now: clock.Now, SweepInterval: -1})
	t.Cleanup(func() { rec.Close(); _ = db.Close(context.Background()) })
	h := &harness{t: t, dir: dir, db: db, store: store, rec: rec, clock: clock}
	h.call(context.Background(), "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, `{"matches":[]}`)
	recs := h.outbox()
	if task := lastTask(t, recs); task.Branch != strings.Repeat("x", 13) {
		t.Fatalf("branch = %q, want 13 x", task.Branch)
	}
	for _, r := range recs {
		if strings.Contains(string(r.Body), "zz-secret") {
			t.Fatalf("the branch name left the device: %s", r.Body)
		}
	}
	// Switch to a detached HEAD; past the throttle the next upsert says "".
	writeFile(t, head, "0123456789abcdef0123456789abcdef01234567\n")
	clock.advance(2 * time.Minute)
	h.call(context.Background(), "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, `{"matches":[]}`)
	if task := lastTask(t, h.outbox()); task.Branch != "" {
		t.Fatalf("branch after detach = %q, want \"\"", task.Branch)
	}
}
