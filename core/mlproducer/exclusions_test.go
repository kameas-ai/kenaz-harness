package mlproducer

// exclusions_test.go — ml-producer-01MLPRD01 WP05: the on-device org
// exclusion semantics (exclusions.go) and their effect on what the
// Recorder writes, against real sqlite (recorder_test.go's harness).

import (
	"context"
	"errors"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
)

const testHome = "/Users/alice"

func mustCompile(t *testing.T, e Exclusions) *ExclusionSet {
	t.Helper()
	s, err := CompileExclusions(e, testHome)
	if err != nil {
		t.Fatalf("CompileExclusions(%+v): %v", e, err)
	}
	return s
}

func TestExclusionSet_PathGlobTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		// Contract examples: relative patterns are unanchored.
		{"hr/**", "/Users/alice/acme/hr/plan.md", true},
		{"hr/**", "/hr/x", true},
		{"hr/**", "/Users/alice/acme/hrx/plan.md", false},
		{"hr/**", "/Users/alice/acme/chr/plan.md", false},
		{"**/secrets/*", "/repo/config/secrets/db.yaml", true},
		{"**/secrets/*", "/repo/secrets/nested/deep.yaml", true}, // dir prefix /repo/secrets/nested matches
		{"**/secrets/*", "/repo/secrets", false},
		// Anchored absolute patterns + directory prefixes.
		{"/secret/**", "/secret/a/b.txt", true},
		{"/secret/**", "/secret", true},
		{"/secret", "/secret/a/b.txt", true},
		{"/secret/", "/secret/a/b.txt", true},
		{"/secret/**", "/notsecret/a", false},
		{"/secret/**", "/home/secret/a", false}, // anchored at root
		{"/Users/*/acme/.env", "/Users/alice/acme/.env", true},
		{"/Users/*/acme/.env", "/Users/alice/bob/acme/.env", false},
		// ** in the middle, zero or more segments.
		{"/a/**/z.txt", "/a/z.txt", true},
		{"/a/**/z.txt", "/a/b/c/z.txt", true},
		{"/a/**/z.txt", "/a/b/c/y.txt", false},
		// Single-segment wildcards.
		{"*.pem", "/Users/alice/keys/server.pem", true},
		{"*.pem", "/Users/alice/keys/server.pem.bak", false},
		{"id_rsa?", "/Users/alice/.ssh/id_rsa2", true},
		{"[ab]*.key", "/k/b1.key", true},
		{"[ab]*.key", "/k/c1.key", false},
		// ~ expansion.
		{"~/private/**", "/Users/alice/private/notes.md", true},
		{"~/private/**", "/Users/bob/private/notes.md", false},
		{"~", "/Users/alice/anything", true},
		// Case folding, separators, cleaning.
		{"HR/**", "/users/alice/acme/hr/plan.md", true},
		{"/Secret/**", "/SECRET/x", true},
		{"/a/b", "/a//b/./c", true},
		{"hr/**", "hr/plan.md", true}, // raw relative argument
		// Whole-tree patterns.
		{"**", "/anything/at/all", true},
		{"/", "/anything", true},
	}
	for _, tc := range cases {
		s := mustCompile(t, Exclusions{Paths: []string{tc.pattern}})
		if got := s.MatchPath(tc.path); got != tc.want {
			t.Errorf("MatchPath(%q against %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
	// No patterns, nil set, empty path: nothing matches.
	if (&ExclusionSet{}).MatchPath("/a") || (*ExclusionSet)(nil).MatchPath("/a") {
		t.Error("an empty set excluded a path")
	}
	if mustCompile(t, Exclusions{Paths: []string{"**"}}).MatchPath("") {
		t.Error("an empty path matched")
	}
}

func TestExclusionSet_CommandPrefixTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix string
		cmd    string
		want   bool
	}{
		{"ssh", "ssh prod-db", true},
		{"ssh", "   ssh prod-db", true},
		{"ssh", "sshfs host:/ /mnt", true}, // literal prefix (over-match by design)
		{"ssh", "echo ssh", false},
		{"ssh", "grep -r ssh .", false},
		{"git push", "git push origin main", true},
		{"git push", "git   push\torigin", true},
		{"git push", "GIT PUSH origin", true},
		{"git push", "git pull", false},
		{"git push", "git commit -m push", false},
		// Leading env assignments are skipped (as the minimiser does).
		{"git push", "GIT_SSH_COMMAND='ssh -i k' git push", true},
		{"aws", "AWS_PROFILE=prod AWS_REGION=x aws s3 ls", true},
		// Prefixes are matched against the full line, before truncation.
		{"terraform apply -auto-approve", "terraform apply -auto-approve -var x=1", true},
		{"terraform apply -auto-approve", "terraform apply", false},
		// Shell lists and pipes: each simple command is a candidate.
		{"git push", "cd /repo && git push", true},
		{"git push", "make build; git push", true},
		{"git push", "false || git push", true},
		{"ssh", "cat x | ssh host 'cat > y'", true},
		{"ssh", "(ssh host)", true},
		{"ssh", "echo `ssh host hostname`", true},
		{"ssh", "make\nssh host", true},
		// Wrapper words (and their flags) are skipped.
		{"ssh", "sudo ssh host", true},
		{"ssh", "sudo -E ssh host", true},
		{"ssh", "env FOO=1 ssh host", true},
		{"ssh", "nohup ssh host &", true},
		{"ssh", "time ssh host", true},
		// A prefix written with an assignment still matches the raw line.
		{"FOO=1 make", "FOO=1 make deploy", true},
	}
	for _, tc := range cases {
		s := mustCompile(t, Exclusions{Commands: []string{tc.prefix}})
		if got := s.MatchCommand(tc.cmd); got != tc.want {
			t.Errorf("MatchCommand(%q against %q) = %v, want %v", tc.cmd, tc.prefix, got, tc.want)
		}
	}
	if (*ExclusionSet)(nil).MatchCommand("ssh x") || mustCompile(t, Exclusions{}).MatchCommand("ssh x") {
		t.Error("an empty set excluded a command")
	}
}

// Path exclusions apply to path-looking tokens anywhere in a bash command
// line (review fix: a terminal row about an excluded file is data about
// that file).
func TestExclusionSet_CommandPathTable(t *testing.T) {
	t.Parallel()
	s := mustCompile(t, Exclusions{Paths: []string{"hr/**", "/secret/**", "*.pem"}})
	cases := []struct {
		cmd  string
		cwd  string
		want bool
	}{
		{"cat /Users/alice/acme/hr/plan.md", "", true},                    // absolute
		{"cat plan.md", "/Users/alice/acme/hr", true},                     // relative, resolved against cwd
		{"cat ./plan.md", "/Users/alice/acme/hr", true},                   // ./ relative
		{"cat ../hr/plan.md", "/Users/alice/acme/src", true},              // .. relative
		{"cat hr/plan.md", "", true},                                      // relative, unanchored as written
		{"cat ~/hr/plan.md", "", true},                                    // ~ expanded
		{"cat \"/secret/my file.txt\"", "", true},                         // double-quoted
		{"cat '/secret/a.txt'", "", true},                                 // single-quoted
		{"tool --out=/secret/x", "", true},                                // --flag= value
		{"OUT=/secret/x make", "", true},                                  // VAR= value
		{"echo x > hr/a.txt", "/repo", true},                              // redirect target
		{"echo x >/secret/a.txt", "", true},                               // redirect without space
		{"openssl x509 -in server.pem", "", true},                         // bare name.ext
		{"cd /repo && cat hr/plan.md | wc -l", "", true},                  // inside a list / pipe
		{"cat /Users/alice/acme/src/main.go", "/Users/alice/acme", false}, // non-matching control
		{"go test ./...", "/Users/alice/acme", false},                     // non-matching control
		{"echo hello world", "/Users/alice/acme/hr", false},               // no path tokens
	}
	for _, tc := range cases {
		if got := s.MatchCommandPaths(tc.cmd, tc.cwd); got != tc.want {
			t.Errorf("MatchCommandPaths(%q, cwd=%q) = %v, want %v", tc.cmd, tc.cwd, got, tc.want)
		}
	}
	if (*ExclusionSet)(nil).MatchCommandPaths("cat /secret/x", "") || mustCompile(t, Exclusions{Commands: []string{"x"}}).MatchCommandPaths("cat /secret/x", "") {
		t.Error("a set without path patterns matched a command path")
	}
}

func TestCompileExclusions_RefusesWhatCannotBeHonoured(t *testing.T) {
	t.Parallel()
	for _, e := range []Exclusions{
		{Paths: []string{"hr/[x"}},
		{Paths: []string{"  "}},
		{Commands: []string{" \t "}},
	} {
		if _, err := CompileExclusions(e, testHome); !errors.Is(err, ErrInvalidExclusion) {
			t.Errorf("CompileExclusions(%+v) err = %v, want ErrInvalidExclusion", e, err)
		}
	}
	s := mustCompile(t, Exclusions{Paths: []string{"a/**"}, Commands: []string{"ssh"}, ExcludeBrowser: true, Version: 7})
	if s.Version() != 7 || s.Empty() {
		t.Errorf("compiled set: v%d empty=%v", s.Version(), s.Empty())
	}
	// exclude_browser alone excludes nothing in the harness (no browser
	// data is ever produced here): a documented no-op.
	if !mustCompile(t, Exclusions{ExcludeBrowser: true}).Empty() {
		t.Error("exclude_browser alone produced matchers")
	}
}

// ---- recorder ----

// eventsOutsideTool returns every event the exclusion must suppress.
func suppressedKinds(recs []decoded) []string {
	var out []string
	for _, r := range recs {
		if r.Table != mlstore.TableEvents {
			continue
		}
		switch r.event.Kind {
		case KindFile, KindTerminal, KindCommit, KindPhaseChange:
			out = append(out, r.event.Kind)
		}
	}
	return out
}

// An excluded path or command produces no file / terminal / commit /
// phase_change event and no files key, leaves every counter untouched, and
// is recorded only as agent.tool {task, tool, outcome, dur_ms}.
func TestRecorder_ExcludedPathAndCommand_OnlyAgentTool(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.rec.SetExclusions(mustCompile(t, Exclusions{
		Paths:    []string{"hr/**", "/Users/alice/acme-secret-repo/vault"},
		Commands: []string{"go test", "git commit", "ssh"},
	}))
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"hr/salaries.csv","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	h.call(ctx, "s1", "kenaz__edit_file", `{"path":"/Users/alice/acme-secret-repo/vault/keys.txt","old":"a","new":"b"}`, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashFail)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"cd sub && git commit -m 'q3 layoffs'"}`, coreag.ToolOutcomeOK, bashOK)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"ssh prod-db","run_in_background":true}`, coreag.ToolOutcomeOK, `{"task_id":"t1","status":"running"}`)
	recs := h.outbox()

	if k := suppressedKinds(recs); len(k) != 0 {
		t.Fatalf("excluded calls produced %v", k)
	}
	tools := events(recs, KindTool)
	if len(tools) != 5 {
		t.Fatalf("agent.tool = %d, want 5", len(tools))
	}
	want := []string{"kenaz__write_file", "kenaz__edit_file", "kenaz__bash", "kenaz__bash", "kenaz__bash"}
	for i, ev := range tools {
		p := ev.event.Payload
		if len(p) != 4 || p["tool"] != want[i] || p["outcome"] != "ok" || p["dur_ms"] != float64(25) || p["task"] != h.taskID("s1") {
			t.Errorf("agent.tool[%d] payload = %v, want exactly {task, tool=%s, outcome=ok, dur_ms=25}", i, p, want[i])
		}
	}
	row, ok, err := h.store.LoadTask(ctx, h.taskID("s1"))
	if err != nil || !ok {
		t.Fatalf("task row: ok=%v err=%v", ok, err)
	}
	if len(row.Files) != 0 || row.TestRuns != 0 || row.TestFails != 0 || row.CommitCount != 0 || row.Phase != string(PhaseIdle) {
		t.Errorf("task counters moved under exclusion: %+v", row)
	}
	task := lastTask(t, recs)
	if len(task.Files) != 0 {
		t.Errorf("task upsert files = %v, want none", task.Files)
	}

	// Calls that are NOT excluded are untouched by the set.
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"src/main.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go build ./..."}`, coreag.ToolOutcomeOK, bashOK)
	recs = h.outbox()
	if len(events(recs, KindFile)) != 1 || len(events(recs, KindTerminal)) != 1 {
		t.Errorf("non-excluded calls: file=%d terminal=%d, want 1 and 1", len(events(recs, KindFile)), len(events(recs, KindTerminal)))
	}
}

// A bash command whose argument is an excluded path (no command prefix
// configured) is recorded only as agent.tool, including a relative path
// resolved against the call's working_dir.
func TestRecorder_CommandWithExcludedPathArgument_OnlyAgentTool(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.rec.SetExclusions(mustCompile(t, Exclusions{Paths: []string{"hr/**"}}))
	h.call(ctx, "s1", "kenaz__bash", `{"command":"cat ~/hr/plan.md"}`, coreag.ToolOutcomeOK, bashOK)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m x plan.md","working_dir":"hr"}`, coreag.ToolOutcomeOK, bashOK)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./...","working_dir":"hr"}`, coreag.ToolOutcomeOK, bashFail)
	recs := h.outbox()
	if k := suppressedKinds(recs); len(k) != 0 {
		t.Fatalf("commands on excluded paths produced %v", k)
	}
	if n := len(events(recs, KindTool)); n != 3 {
		t.Fatalf("agent.tool = %d, want 3", n)
	}
	row, _, _ := h.store.LoadTask(ctx, h.taskID("s1"))
	if row.CommitCount != 0 || row.TestRuns != 0 || row.TestFails != 0 {
		t.Errorf("counters moved: %+v", row)
	}
}

// The newest exclusions apply to the next call; removing them (a
// broadening the member has re-acked) applies too.
func TestRecorder_ExclusionsUpdateBetweenCalls(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	write := func(p string) {
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"`+p+`","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	}
	write("docs/a.md")
	h.rec.SetExclusions(mustCompile(t, Exclusions{Paths: []string{"docs/**"}, Version: 2}))
	write("docs/b.md")
	h.rec.SetExclusions(mustCompile(t, Exclusions{Version: 3}))
	write("docs/c.md")
	recs := h.outbox()
	files := events(recs, KindFile)
	if len(files) != 2 {
		t.Fatalf("file events = %d, want 2 (a.md before, c.md after)", len(files))
	}
	if files[0].event.Payload["path"] != h.hash(workspace+"/docs/a.md")+".md" || files[1].event.Payload["path"] != h.hash(workspace+"/docs/c.md")+".md" {
		t.Errorf("file events = %v / %v", files[0].event.Payload, files[1].event.Payload)
	}
	if len(events(recs, KindTool)) != 1 {
		t.Errorf("agent.tool = %d, want 1 (b.md)", len(events(recs, KindTool)))
	}
}

// A call observed under an exclusion stays excluded even if the set is
// replaced before the worker handles it (the union of both is applied).
func TestRecorder_ExclusionInForceAtObservationWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	excl := mustCompile(t, Exclusions{Commands: []string{"ssh"}})
	// Observe and handle under the exclusion, with the worker's view
	// already swapped to an empty set.
	at := h.rec.nowMS()
	h.rec.SetExclusions(&ExclusionSet{})
	h.rec.enqueue(func() {
		h.rec.handleTool(coreag.ToolCallRecord{Ctx: ctx, SessionID: "s1", ToolName: "kenaz__bash", Outcome: coreag.ToolOutcomeOK,
			RawArgs: `{"command":"ssh host"}`, ResultContent: bashOK}, false, at, excl)
	})
	if k := suppressedKinds(h.outbox()); len(k) != 0 {
		t.Fatalf("a call observed under an exclusion produced %v", k)
	}
}

// Planted proof for the exclusion guarantee (WP04's minimisation gate is
// not in this tree; this test plays its role for WP05): the same fixture
// session is recorded twice. Without exclusions, the detector FINDS the
// fixture path's hash and the command's first tokens in the outbox bytes —
// so the detector can fail. With the exclusions installed, neither the raw
// path, its hash, the raw command nor its shipped prefix reaches any
// record.
func TestExclusions_PlantedProof_ExcludedFixtureNeverReachesARecord(t *testing.T) {
	t.Parallel()
	const fixturePath = "/Users/alice/acme-secret-repo/hr/2026-comp-bands.xlsx"
	const fixtureCmd = "scp hr/2026-comp-bands.xlsx backup:/x"
	// The excluded path as an argument of a command that NO prefix
	// matches: only the path exclusion can stop it.
	// (no digits: the minimiser would otherwise redact it as credential-shaped
	// and the planted run could not show the hash leaking)
	const fixtureArgPath = "/Users/alice/acme-secret-repo/hr/salary-bands.md"
	const fixturePathCmd = "cat " + fixtureArgPath
	run := func(t *testing.T, set *ExclusionSet) (string, *harness) {
		h := newHarness(t)
		ctx := context.Background()
		if set != nil {
			h.rec.SetExclusions(set)
		}
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"`+fixturePath+`","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
		h.call(ctx, "s1", "kenaz__bash", `{"command":"`+fixtureCmd+`"}`, coreag.ToolOutcomeOK, bashOK)
		h.call(ctx, "s1", "kenaz__bash", `{"command":"`+fixturePathCmd+`"}`, coreag.ToolOutcomeOK, bashOK)
		var all strings.Builder
		for _, r := range h.outbox() {
			all.Write(r.Body)
			all.WriteByte('\n')
		}
		return all.String(), h
	}
	leaks := func(h *harness, bytes string) []string {
		var out []string
		for _, needle := range []string{fixturePath, "comp-bands", h.hash(fixturePath), h.hash(fixtureArgPath), "salary-bands", fixtureCmd, `"cmd":"scp `, `"cmd":"cat `} {
			if strings.Contains(bytes, needle) {
				out = append(out, needle)
			}
		}
		return out
	}

	planted, hp := run(t, nil)
	if got := leaks(hp, planted); len(got) < 3 || !strings.Contains(strings.Join(got, "|"), `"cmd":"cat `) {
		t.Fatalf("planted run: detector found %v — it must find the path hash, the scp prefix AND the cat-with-path terminal row, or it proves nothing", got)
	}
	if !strings.Contains(planted, `"cmd":"cat `+hp.hash(fixtureArgPath)+`.md"`) {
		t.Fatalf("planted run: the cat row should carry h(path), proving the path inside the command would leak:\n%s", planted)
	}
	clean, hc := run(t, mustCompile(t, Exclusions{Paths: []string{"hr/**"}, Commands: []string{"scp"}}))
	if got := leaks(hc, clean); len(got) != 0 {
		t.Fatalf("excluded fixture reached a record: %v\n%s", got, clean)
	}
	if !strings.Contains(clean, `"kind":"agent.tool"`) {
		t.Fatalf("the excluded calls should still be counted as agent.tool:\n%s", clean)
	}
}
