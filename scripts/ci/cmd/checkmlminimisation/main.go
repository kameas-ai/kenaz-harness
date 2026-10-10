// Command checkmlminimisation is the spec §7 / §12 A-10 minimisation gate
// for the harness ML producer (ml-producer-01MLPRD01 WP04). It has two
// halves, and either one failing fails the build.
//
// # Static half (the kind allowlist)
//
//  1. mlproducer.KindTable — read from the COMPILED package, so what is
//     checked is what ships — must be EXACTLY the six A-10 kinds, each
//     with exactly its A-10 payload keys. A kind added (a daemon-only
//     kind such as `hyprland`), a kind dropped, or a key added or dropped
//     is a violation. The contract table below is this gate's own copy of
//     the spec; it is deliberately NOT derived from KindTable, so a drift
//     in KindTable cannot loosen the gate.
//  2. Every non-test .go file under core/mlproducer is parsed. A string
//     literal in an event-kind position — a `Kind*` constant's value, the
//     kind argument of encodeEvent, a `Kind:` keyed field, or the `kind`
//     field of a struct literal declared in the package — must name a
//     contract kind. Floor: the six Kind* constants must be found, so a
//     moved or renamed record.go is a scan failure, not a clean pass.
//
// # Dynamic half (the real recorder over a fixture corpus)
//
// The real mlproducer.Recorder runs in-process over a temp sqlite
// database (storagesqlite.Open, so the real ml-producer migrations apply)
// with a fixed hash key and an open gate, and is fed a fixture corpus of
// tool calls built to tempt every minimisation rule: writes/edits at deep
// absolute, relative and Windows paths; bash with long commands carrying
// paths, secrets, env assignments and URLs; a custom (user-named) MCP
// server; git commit; test commands; refused and denied calls; background
// bash and its exit (WP07: a failing background test); reads whose
// results name canary paths (read_file, grep, glob, list_dir; WP07: reads
// ship `file` events); an invented tool name; turn ends; a subagent child;
// a session delete. The workspace branch is a canary name read from a
// fake repository by the real HEAD reader. The outbox is then read back and every record is checked
// against §7:
//
//   - a kind outside the contract table;
//   - a payload key not allowed for that kind;
//   - a value that looks like a raw path (`/` or `\` with >1 segment);
//   - a `cmd` longer than two tokens;
//   - a credential-looking token;
//   - any fixture canary (raw path, raw repo root, raw command text, the
//     secrets and the private server name) appearing verbatim;
//   - a task `files` key that is not h+ext;
//   - a task `repo_root` that is not h(workspace) under the fixed key —
//     recomputed here with crypto/hmac, not with the package's Hasher;
//   - a task `branch` that does not match `^x*$` (WP07: only the branch
//     name's length ships, as a placeholder; the name never leaves the
//     device).
//
// Floor: every contract kind, at least one task upsert and at least one
// non-empty branch placeholder must appear in the outbox, so a recorder that silently records nothing cannot pass.
//
// Exit codes (when run directly): 0 clean; 1 the scan or the harness
// itself failed; 2 at least one violation. check-ml-producer-
// minimisation.sh runs this via `go run`, which collapses any non-zero
// exit to 1 — callers match on the "[ml-minimisation] FAIL:" lines.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

const tag = "[ml-minimisation]"

// contract is spec §12 A-10's table, copied by hand. Change it only with
// the spec.
var contract = map[string][]string{
	"file":         {"task", "path", "file"},
	"terminal":     {"task", "cmd", "exit_code"},
	"commit":       {"task"},
	"phase_change": {"task", "phase"},
	"agent.tool":   {"task", "tool", "outcome", "dur_ms"},
	"agent.turn":   {"task", "dur_ms", "model_calls", "tool_calls", "outcome"},
}

// taskKeys is spec §3.1's task body.
var taskKeys = map[string]bool{
	"id": true, "repo_root": true, "branch": true, "phase": true, "files": true,
	"started_at": true, "last_active": true, "completed_at": true,
	"commit_count": true, "test_runs": true, "test_fails": true,
}

var eventKeys = map[string]bool{"id": true, "kind": true, "source": true, "ts": true, "payload": true}

var phases = map[string]bool{"idle": true, "exploring": true, "coding": true, "testing": true, "reviewing": true}

const pkgDir = "core/mlproducer"

func main() {
	root, err := repoRoot()
	if err != nil {
		fail1(err)
	}
	if err := os.Chdir(root); err != nil {
		fail1(err)
	}

	var violations []string
	sv, err := staticHalf()
	if err != nil {
		fail1(err)
	}
	violations = append(violations, sv...)

	dv, err := dynamicHalf()
	if err != nil {
		fail1(err)
	}
	violations = append(violations, dv...)

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, tag, "FAIL:", v)
		}
		fmt.Fprintf(os.Stderr, "%s %d violation(s). Spec ml-producer-01MLPRD01 §7 / §12 A-10: the harness ships only "+
			"the six contract kinds with exactly their payload keys, hashed paths, two-token commands and no secrets.\n",
			tag, len(violations))
		os.Exit(2)
	}
	fmt.Printf("%s clean — KindTable is exactly the %d A-10 kinds; %s; %s.\n", tag, len(contract), stats.static, stats.dynamic)
}

func fail1(err error) {
	fmt.Fprintln(os.Stderr, tag, "FAIL:", err)
	os.Exit(1)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root not found (no git, no go.mod above cwd)")
		}
		dir = parent
	}
}

type runStats struct{ static, dynamic string }

var stats runStats

// ---------------------------------------------------------------- static

func staticHalf() ([]string, error) {
	var v []string

	// 1. KindTable, as compiled.
	seen := map[string]bool{}
	for _, k := range mlproducer.KindTable {
		if seen[k.Kind] {
			v = append(v, fmt.Sprintf("KindTable lists kind %q twice", k.Kind))
			continue
		}
		seen[k.Kind] = true
		want, ok := contract[k.Kind]
		if !ok {
			v = append(v, fmt.Sprintf("KindTable has kind %q, which is not one of the six A-10 kinds "+
				"(a daemon-only or invented kind must never ship from the harness)", k.Kind))
			continue
		}
		if d := keyDiff(want, k.PayloadKeys); d != "" {
			v = append(v, fmt.Sprintf("KindTable kind %q payload keys drifted from A-10: %s", k.Kind, d))
		}
	}
	for k := range contract {
		if !seen[k] {
			v = append(v, fmt.Sprintf("KindTable is missing A-10 kind %q (the table drifted from the contract)", k))
		}
	}

	// 2. Kind literals in the source.
	if fi, err := os.Stat(pkgDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("scan root %s does not exist — a gate cannot pass by having nothing to look at", pkgDir)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	err := filepath.WalkDir(pkgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", pkgDir, err)
	}

	// Struct types declared anywhere in the package (incl. function-local
	// ones such as recorder.go's `draft`): name -> index of a field named
	// kind/Kind.
	kindField := map[string]int{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			idx := 0
			for _, fld := range st.Fields.List {
				names := len(fld.Names)
				if names == 0 {
					names = 1
				}
				for _, nm := range fld.Names {
					if strings.EqualFold(nm.Name, "kind") {
						kindField[ts.Name.Name] = idx
					}
					idx++
				}
				if len(fld.Names) == 0 {
					idx += names
				}
			}
			return true
		})
	}

	kindConsts := 0
	literalSites := 0
	check := func(pos token.Pos, e ast.Expr, where string) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		literalSites++
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		if _, ok := contract[val]; !ok {
			v = append(v, fmt.Sprintf("%s: %s uses event kind %q, which is not one of the six A-10 kinds",
				fset.Position(pos), where, val))
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ValueSpec:
				for i, nm := range x.Names {
					if !strings.HasPrefix(nm.Name, "Kind") || len(nm.Name) < 5 || i >= len(x.Values) {
						continue
					}
					if lit, ok := x.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						kindConsts++
						check(nm.Pos(), lit, "constant "+nm.Name)
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "encodeEvent" && len(x.Args) >= 2 {
					check(x.Pos(), x.Args[1], "encodeEvent")
				}
			case *ast.KeyValueExpr:
				if id, ok := x.Key.(*ast.Ident); ok && strings.EqualFold(id.Name, "kind") {
					check(x.Pos(), x.Value, "a "+id.Name+": field")
				}
			case *ast.CompositeLit:
				id, ok := x.Type.(*ast.Ident)
				if !ok {
					return true
				}
				idx, ok := kindField[id.Name]
				if !ok || idx >= len(x.Elts) {
					return true
				}
				if _, keyed := x.Elts[0].(*ast.KeyValueExpr); keyed {
					return true // handled by the KeyValueExpr case
				}
				check(x.Pos(), x.Elts[idx], id.Name+"{} literal")
			}
			return true
		})
	}
	if kindConsts < len(contract) {
		return nil, fmt.Errorf("discovery floor: found %d Kind* string constants under %s, want ≥ %d — "+
			"the scan is broken (record.go moved or renamed), not clean", kindConsts, pkgDir, len(contract))
	}
	stats.static = fmt.Sprintf("%d file(s) parsed, %d Kind* constant(s) and %d kind-position literal(s) checked",
		len(files), kindConsts, literalSites)
	return v, nil
}

func keyDiff(want, got []string) string {
	w, g := map[string]bool{}, map[string]bool{}
	for _, k := range want {
		w[k] = true
	}
	for _, k := range got {
		g[k] = true
	}
	var extra, missing []string
	for k := range g {
		if !w[k] {
			extra = append(extra, k)
		}
	}
	for k := range w {
		if !g[k] {
			missing = append(missing, k)
		}
	}
	if len(got) != len(g) {
		extra = append(extra, "(duplicate key)")
	}
	sort.Strings(extra)
	sort.Strings(missing)
	var parts []string
	if len(extra) > 0 {
		parts = append(parts, "extra "+strings.Join(extra, ","))
	}
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ","))
	}
	return strings.Join(parts, "; ")
}

// --------------------------------------------------------------- dynamic

// fixedKey is the gate's hash key; repo_root is recomputed from it here.
var fixedKey = []byte("ml-minimisation-gate-fixed-key-0")

const workspace = "/Users/zzcanaryuser/src/zzcanaryrepo"

// privateServer is a user-named MCP server; its name must never ship.
const privateServer = "zzprivateserver"

// canaryBranch is the workspace's git branch in the fixture repository
// (multi-byte on purpose: the placeholder counts runes). Only "x"×len
// may ship.
const canaryBranch = "zzcanary/feature-ünïcode"

type fixture struct {
	session    string
	unattended bool
	tool       string
	outcome    coreag.ToolOutcome
	args       map[string]any
	result     string
	canaries   []string
}

func bashArgs(cmd string) map[string]any { return map[string]any{"command": cmd} }

func bashResult(exit int) string {
	return fmt.Sprintf(`{"stdout":"zzcanary-stdout /Users/zzcanaryuser/out","stderr":"","exit_code":%d,"truncated":false}`, exit)
}

func corpus() []fixture {
	const s = "zzcanary-session-attended"
	const deep = workspace + "/internal/zzcanarydeep/a/b/c/zzcanaryplan.go"
	cmds := []struct {
		cmd    string
		exit   int
		extras []string
	}{
		{"go test ./internal/zzcanarydeep/... -run TestZZCanary", 1, nil},
		{"GITHUB_TOKEN=ghp_zzCanary1234567890abcdefABCDEF git push https://zzcanaryuser:zzhunter2@github.com/zzcanaryorg/zzcanaryrepo.git", 0,
			[]string{"ghp_zzCanary", "zzhunter2", "github.com/zzcanaryorg"}},
		{"export AWS_SECRET_ACCESS_KEY=zzCanarySecret9876543210abcdXYZ", 0, []string{"zzCanarySecret", "AWS_SECRET_ACCESS_KEY="}},
		{"curl https://zzcanary.example.com/api?token=zzcanarytok123 -o /tmp/zzcanaryout", 0, []string{"zzcanary.example.com", "zzcanarytok123"}},
		{"/usr/local/bin/zzcanarytool --config /Users/zzcanaryuser/.zzcanaryrc", 0, []string{"/usr/local/bin/zzcanarytool"}},
		{"cat ./zzcanarydir/zzcanaryfile.txt | grep zzcanarypattern", 0, []string{"zzcanarydir", "zzcanaryfile.txt"}},
		{"ZZ_CANARY_ENV=zzcanaryvalue npm run test -- --grep zzcanary", 0, []string{"zzcanaryvalue"}},
		{`git commit -m "zzcanary commit message for /Users/zzcanaryuser"`, 0, []string{"zzcanary commit message"}},
		{"git diff HEAD~1 -- " + workspace + "/zzcanarydiff.go", 0, []string{"zzcanarydiff.go"}},
		{"mysql --user zzcanaryadmin -pzzhunter4secretpw zzcanarydb", 0, []string{"zzhunter4", "zzcanaryadmin"}},
		// Assembled at runtime so check-no-cred-bytes-in-rpc.sh (which greps
		// source for sk-* literals) does not read this canary as a real key.
		{"sk" + "-zzCanaryLiveKey0123456789abcdef --flag", 0, []string{"sk" + "-zzCanary"}},
		{`C:\Users\zzcanaryuser\zzwin\tool.exe --run`, 0, []string{`C:\Users\zzcanaryuser`}},
	}
	var fx []fixture
	add := func(f fixture) {
		if f.session == "" {
			f.session = s
		}
		if f.outcome == "" {
			f.outcome = coreag.ToolOutcomeOK
		}
		fx = append(fx, f)
	}
	// File writes / edits: deep absolute, relative, Windows-shaped.
	add(fixture{tool: "kenaz__write_file", args: map[string]any{"path": deep, "content": "zzcanary content"},
		result: `{"bytes_written":16}`, canaries: []string{deep, "zzcanaryplan", "internal/zzcanarydeep"}})
	add(fixture{tool: "kenaz__edit_file", args: map[string]any{"path": "internal/zzcanaryrel/handler_zzcanary.ts", "old_string": "a", "new_string": "b"},
		result: `{"replacements":1}`, canaries: []string{"zzcanaryrel", "handler_zzcanary"}})
	add(fixture{tool: "kenaz__write_file", args: map[string]any{"path": `C:\Users\zzcanaryuser\zzwin\notes.md`, "content": "x"},
		result: `{"bytes_written":1}`, canaries: []string{`zzwin`}})
	add(fixture{tool: "kenaz__write_file", args: map[string]any{"path": "/Users/zzcanaryuser/no_ext_zzcanary", "content": "x"},
		result: `{"bytes_written":1}`, canaries: []string{"no_ext_zzcanary"}})
	// Denied and failed writes ship as agent.tool.
	add(fixture{tool: "kenaz__write_file", outcome: coreag.ToolOutcomeDenied, args: map[string]any{"path": "/etc/zzcanary/passwd"},
		canaries: []string{"/etc/zzcanary"}})
	add(fixture{tool: "kenaz__edit_file", args: map[string]any{"path": workspace + "/zzcanarymissing.go"},
		result:   `{"is_error":true,"error":"no such file /Users/zzcanaryuser/src/zzcanaryrepo/zzcanarymissing.go"}`,
		canaries: []string{"zzcanarymissing"}})
	add(fixture{tool: "kenaz__read_file", args: map[string]any{"path": workspace + "/README_zzcanary.md"},
		result: "zzcanary file contents", canaries: []string{"README_zzcanary"}})
	// WP07 reads: every path named by the RESULT ships as a `file` event
	// (h+ext), relative ones resolved against the tool's root.
	add(fixture{tool: "kenaz__read_file", args: map[string]any{"path": "internal/zzcanaryread/secret_zzcanary.go", "offset": 0},
		result:   `{"content":"zzcanary contents","byte_size":17,"truncated":false}`,
		canaries: []string{"zzcanaryread", "secret_zzcanary"}})
	add(fixture{tool: "kenaz__grep", args: map[string]any{"pattern": "zzcanarypattern", "path": "internal"},
		result: `{"matches":[{"file":"` + workspace + `/internal/zzcanarygrep/a.go","line":3,"content":"zzcanary match line"},` +
			`{"file":"` + workspace + `/internal/zzcanarygrep/a.go","line":9,"content":"zzcanary again"},` +
			`{"file":"zzcanaryrelgrep/b.py","line":1,"content":"zzcanary rel"}],"truncated":false}`,
		canaries: []string{"zzcanarygrep", "zzcanaryrelgrep", "zzcanary match line"}})
	add(fixture{tool: "kenaz__glob", args: map[string]any{"pattern": "**/*.ts", "base_dir": workspace + "/web"},
		result:   `{"matches":["` + workspace + `/web/zzcanaryglob/x.ts","zzcanaryglobrel/y.tsx"],"truncated":false}`,
		canaries: []string{"zzcanaryglob"}})
	add(fixture{tool: "kenaz__list_dir", args: map[string]any{"path": "docs/zzcanarylist", "recursive": true},
		result: `{"entries":[{"name":"plan_zzcanary.md","type":"file","size":1,"path":"plan_zzcanary.md"},` +
			`{"name":"zzcanarysub","type":"dir","size":0,"path":"zzcanarysub"},` +
			`{"name":"n.txt","type":"file","size":1,"path":"zzcanarysub/n.txt"}],"truncated":false}`,
		canaries: []string{"zzcanarylist", "plan_zzcanary", "zzcanarysub"}})
	// Bash that ran.
	for _, c := range cmds {
		add(fixture{tool: "kenaz__bash", args: bashArgs(c.cmd), result: bashResult(c.exit),
			canaries: append([]string{c.cmd}, c.extras...)})
	}
	// Bash refused by its own gate, and denied before dispatch.
	add(fixture{tool: "kenaz__bash", args: bashArgs("rm -rf " + workspace + "/zzcanaryrm"),
		result:   `{"stdout":"","stderr":"refused","exit_code":-1,"refused":true}`,
		canaries: []string{"zzcanaryrm"}})
	add(fixture{tool: "kenaz__bash", outcome: coreag.ToolOutcomeDenied, args: bashArgs("sudo zzcanary-escalate --password zzhunter3"),
		canaries: []string{"zzhunter3", "zzcanary-escalate"}})
	add(fixture{tool: "kenaz__bash", outcome: coreag.ToolOutcomeCancelled, args: bashArgs("sleep 1000 && zzcanary-later")})
	// Background bash: records at spawn, no exit_code.
	add(fixture{tool: "kenaz__bash", args: map[string]any{"command": "go test ./zzcanarybg/... -count=1", "run_in_background": true},
		result: `{"task_id":"bg-zzcanary","status":"running"}`, canaries: []string{"zzcanarybg"}})
	// MCP: a user-named (custom) server and a catalog one.
	add(fixture{tool: privateServer + "__deploy", args: map[string]any{"target": "/srv/zzcanary/prod", "token": "zzhunter5"},
		result: "zzcanary deployed", canaries: []string{privateServer, "zzhunter5"}})
	add(fixture{tool: "github__create_issue", args: map[string]any{"title": "zzcanary issue title", "body": "zzcanary"},
		result: `{"url":"https://github.com/zzcanaryorg/zzcanaryrepo/issues/1"}`})
	add(fixture{tool: "kenaz__web_fetch", args: map[string]any{"url": "https://zzcanary.internal/secret?k=zzhunter6"},
		result: "zzcanary page", canaries: []string{"zzhunter6"}})
	// A model-invented tool name that reached dispatch.
	add(fixture{tool: "../../zzcanaryinvented tool", outcome: coreag.ToolOutcomeError, args: map[string]any{}})
	// A subagent child of the attended session, attributed to the root.
	add(fixture{session: "zzcanary-child", unattended: true, tool: "kenaz__write_file",
		args: map[string]any{"path": workspace + "/zzcanarychild/child.py"}, result: `{"bytes_written":1}`,
		canaries: []string{"zzcanarychild"}})
	// An unattended (scheduled) session with no link: must record nothing.
	add(fixture{session: "zzcanary-scheduled", unattended: true, tool: "kenaz__bash",
		args: bashArgs("zzcanaryscheduled run"), result: bashResult(0)})
	return fx
}

// Generic canaries checked against every record, in addition to each
// fixture's own list: every canary value in the corpus embeds one.
var globalCanaries = []string{"zzcanary", "zzhunter", privateServer, workspace, "/Users/zzcanaryuser", canaryBranch, "feature-"}

func dynamicHalf() ([]string, error) {
	tmp, err := os.MkdirTemp("", "ml-minimisation-gate-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// Keep the harness logger (migrations, recorder) off the developer's
	// live ~/.kenaz log: it opens lazily on first use, so point it here.
	logging.Configure(tmp)

	db, err := storagesqlite.Open(storage.Config{DataDir: tmp, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		return nil, fmt.Errorf("open temp sqlite: %w", err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		return nil, fmt.Errorf("storage.DB has no SQL()")
	}
	store := mlstore.New(h.SQL())

	// WP07: the workspace's branch is a canary name, served from a fake
	// repository through the real HEAD reader. Only "x"×len may ship.
	fakeRepo := filepath.Join(tmp, "zzcanaryrepo")
	if err := os.MkdirAll(filepath.Join(fakeRepo, ".git"), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(fakeRepo, ".git", "HEAD"), []byte("ref: refs/heads/"+canaryBranch+"\n"), 0o644); err != nil {
		return nil, err
	}
	if got := mlproducer.ReadGitBranch(fakeRepo); got != canaryBranch {
		return nil, fmt.Errorf("fake repository branch reads as %q, want %q — the branch fixture is broken", got, canaryBranch)
	}

	clock := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { clock = clock.Add(90 * time.Second); return clock }
	rec := mlproducer.NewRecorder(mlproducer.Config{
		Store:         store,
		Hasher:        mlproducer.NewHasherWithKey(fixedKey),
		Gate:          mlproducer.GateFunc(func() bool { return true }),
		Servers:       mlproducer.ServerClassifierFunc(func(s string) bool { return s == privateServer }),
		Workspace:     func() string { return workspace },
		GitBranch:     func(string) string { return mlproducer.ReadGitBranch(fakeRepo) },
		Now:           now,
		SweepInterval: -1,
	})
	defer rec.Close()
	rec.LinkChild("zzcanary-child", "zzcanary-session-attended")

	fx := corpus()
	var allCanaries []string
	for _, f := range fx {
		ctx := context.Background()
		if f.unattended {
			ctx = runposture.Unattended(ctx)
		}
		raw, _ := json.Marshal(f.args)
		rec.ToolCallCompleted(coreag.ToolCallRecord{
			Ctx: ctx, SessionID: f.session, ToolName: f.tool, Outcome: f.outcome,
			Duration: 42 * time.Millisecond, RawArgs: string(raw), ResultContent: f.result,
		})
		allCanaries = append(allCanaries, f.canaries...)
		// Raw path args are canaries too.
		if p, ok := f.args["path"].(string); ok {
			allCanaries = append(allCanaries, p)
		}
	}
	ctx := context.Background()
	// WP07: the background `go test` spawned above fails; its exit arrives
	// on a detached, unattended ctx and ships a follow-up terminal.
	rec.BackgroundEnded(runposture.Unattended(ctx), "bg-zzcanary", 1)
	rec.TurnEnded(ctx, "zzcanary-session-attended", "completed", 3, 9, 1500*time.Millisecond)
	rec.TurnEnded(runposture.Unattended(ctx), "zzcanary-child", "stopped", 1, 1, time.Second)
	rec.TurnEnded(ctx, "zzcanary-session-attended", "failed", 1, 0, time.Second) // no tool call → idle
	_ = rec.SessionDeleted(ctx, "zzcanary-session-attended")
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := rec.Flush(fctx); err != nil {
		return nil, fmt.Errorf("recorder flush: %w", err)
	}
	if d := rec.Dropped(); d != 0 {
		return nil, fmt.Errorf("recorder dropped %d work item(s); the corpus did not run", d)
	}
	recs, err := store.ReadBatch(ctx, 0, 100000)
	if err != nil {
		return nil, fmt.Errorf("read outbox: %w", err)
	}

	var v []string
	kindsSeen := map[string]int{}
	tasks, branches := 0, 0
	wantRepoRoot := hmacHex(workspace)
	for _, r := range recs {
		where := fmt.Sprintf("outbox seq %d (%s/%s)", r.Seq, r.Table, r.Op)
		body := string(r.Body)
		for _, c := range append(allCanaries, globalCanaries...) {
			if c != "" && strings.Contains(strings.ToLower(body), strings.ToLower(c)) {
				v = append(v, fmt.Sprintf("%s: carries fixture canary %q verbatim (a raw path, repo root, command or secret leaked)", where, c))
			}
		}
		switch {
		case r.Table == mlstore.TableEvents && r.Op == mlstore.OpInsert:
			kind, ev := checkEvent(where, r, &v)
			if kind != "" {
				kindsSeen[kind]++
			}
			_ = ev
		case r.Table == mlstore.TableTasks && r.Op == mlstore.OpUpsert:
			tasks++
			if checkTask(where, r, wantRepoRoot, &v) {
				branches++
			}
		default:
			v = append(v, fmt.Sprintf("%s: unknown table/op", where))
		}
	}

	// Non-vacuous floor: the corpus must have exercised every contract
	// kind and produced a task upsert.
	var missing []string
	for k := range contract {
		if kindsSeen[k] == 0 {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		v = append(v, fmt.Sprintf("dynamic floor: the fixture corpus produced no %s record(s) — the recorder "+
			"stopped emitting a contract kind, or the corpus no longer reaches it", strings.Join(missing, ", ")))
	}
	if tasks == 0 {
		v = append(v, "dynamic floor: the fixture corpus produced no task upsert")
	}
	if branches == 0 {
		v = append(v, "dynamic floor: no task upsert carried a non-empty branch placeholder — the branch "+
			"read is not reaching the wire, so the ^x*$ rule checked nothing")
	}
	var seen []string
	for k, n := range kindsSeen {
		seen = append(seen, fmt.Sprintf("%s×%d", k, n))
	}
	sort.Strings(seen)
	stats.dynamic = fmt.Sprintf("%d fixture call(s) through the real recorder → %d outbox record(s) [%s, task×%d] all within §7",
		len(fx), len(recs), strings.Join(seen, " "), tasks)
	return v, nil
}

func hmacHex(x string) string {
	m := hmac.New(sha256.New, fixedKey)
	_, _ = m.Write([]byte(x))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

var (
	reTaskID   = regexp.MustCompile(`^agent-[0-9a-f]{16}$`)
	reHash     = regexp.MustCompile(`^[0-9a-f]{16}$`)
	rePathTok  = regexp.MustCompile(`^[0-9a-f]{16}(\.[a-z0-9]{1,12})?$`)
	reToolName = regexp.MustCompile(`^(custom__[0-9a-f]{16}__[A-Za-z0-9_.-]{1,128}|invalid__[0-9a-f]{16}|[A-Za-z0-9_.-]{1,128})$`)
	reHashPfx  = regexp.MustCompile(`^(custom__[0-9a-f]{16}__|invalid__[0-9a-f]{16})`)
	reBranch   = regexp.MustCompile(`^x*$`)
)

var toolOutcomes = map[string]bool{"ok": true, "error": true, "denied": true, "cancelled": true}
var turnOutcomes = map[string]bool{"completed": true, "stopped": true, "failed": true}

func decodeObj(b []byte) (map[string]any, error) {
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func isInt(x any) bool {
	n, ok := x.(json.Number)
	if !ok {
		return false
	}
	_, err := n.Int64()
	return err == nil
}

func checkEvent(where string, r mlstore.Record, v *[]string) (string, map[string]any) {
	ev, err := decodeObj(r.Body)
	if err != nil {
		*v = append(*v, fmt.Sprintf("%s: body is not a JSON object: %v", where, err))
		return "", nil
	}
	for k := range ev {
		if !eventKeys[k] {
			*v = append(*v, fmt.Sprintf("%s: event body key %q is not in the record shape", where, k))
		}
	}
	if s, _ := ev["source"].(string); s != mlproducer.EventSource {
		*v = append(*v, fmt.Sprintf("%s: source = %v", where, ev["source"]))
	}
	if n, ok := ev["id"].(json.Number); !ok || n.String() != r.RowID {
		*v = append(*v, fmt.Sprintf("%s: body id %v != row_id %s", where, ev["id"], r.RowID))
	}
	kind, _ := ev["kind"].(string)
	allowed, ok := contract[kind]
	if !ok {
		*v = append(*v, fmt.Sprintf("%s: kind %q is outside the A-10 table (daemon overlap / invented kind)", where, kind))
		return kind, ev
	}
	payload, ok := ev["payload"].(map[string]any)
	if !ok {
		*v = append(*v, fmt.Sprintf("%s: %s has no payload object", where, kind))
		return kind, ev
	}
	allow := map[string]bool{}
	for _, k := range allowed {
		allow[k] = true
	}
	if _, ok := payload["task"]; !ok {
		*v = append(*v, fmt.Sprintf("%s: %s payload has no task", where, kind))
	}
	for k, val := range payload {
		if !allow[k] {
			*v = append(*v, fmt.Sprintf("%s: %s payload key %q is not allowed for that kind (A-10 allows %s)",
				where, kind, k, strings.Join(allowed, ",")))
			// fall through: still check the value for leaks
		}
		checkValue(where, kind, k, val, v)
	}
	return kind, ev
}

func checkValue(where, kind, key string, val any, v *[]string) {
	bad := func(format string, a ...any) {
		*v = append(*v, fmt.Sprintf("%s: %s.%s ", where, kind, key)+fmt.Sprintf(format, a...))
	}
	s, isStr := val.(string)
	switch key {
	case "task":
		if !isStr || !reTaskID.MatchString(s) {
			bad("= %v is not agent-<h>", val)
		}
		return
	case "path", "file":
		if !isStr || !rePathTok.MatchString(s) {
			bad("= %q is not h(abs)+ext", val)
		}
		return
	case "exit_code", "dur_ms", "model_calls", "tool_calls":
		if !isInt(val) {
			bad("= %v is not an integer", val)
		}
		return
	case "phase":
		if !isStr || !phases[s] {
			bad("= %v is not a phase", val)
		}
		return
	case "outcome":
		ok := isStr && ((kind == "agent.turn" && turnOutcomes[s]) || (kind != "agent.turn" && toolOutcomes[s]))
		if !ok {
			bad("= %v is not an outcome", val)
		}
		return
	case "tool":
		if !isStr || !reToolName.MatchString(s) {
			bad("= %q is not a plain tool name", val)
			return
		}
		rest := reHashPfx.ReplaceAllString(s, "")
		if rawPath(rest) || credential(rest) {
			bad("= %q carries a path or credential", s)
		}
		return
	case "cmd":
		if !isStr {
			bad("is not a string")
			return
		}
		toks := strings.Fields(s)
		if len(toks) > 2 {
			bad("= %q has %d tokens; the contract allows the first two only", s, len(toks))
		}
		for _, t := range toks {
			if t == "[redacted]" || rePathTok.MatchString(t) {
				continue
			}
			if rawPath(t) {
				bad("token %q looks like a raw path", t)
			}
			if credential(t) {
				bad("token %q looks like a credential", t)
			}
		}
		return
	}
	// An unknown key: still refuse leaks in its value.
	if isStr && (rawPath(s) || credential(s)) {
		bad("= %q looks like a raw path or credential", s)
	}
}

// checkTask checks one task upsert; it reports whether the task carried a
// non-empty (and valid) branch placeholder.
func checkTask(where string, r mlstore.Record, wantRepoRoot string, v *[]string) bool {
	t, err := decodeObj(r.Body)
	if err != nil {
		*v = append(*v, fmt.Sprintf("%s: body is not a JSON object: %v", where, err))
		return false
	}
	for k := range t {
		if !taskKeys[k] {
			*v = append(*v, fmt.Sprintf("%s: task key %q is not in spec §3.1", where, k))
		}
	}
	if id, _ := t["id"].(string); !reTaskID.MatchString(id) || id != r.RowID {
		*v = append(*v, fmt.Sprintf("%s: task id %v (row_id %s) is not agent-<h>", where, t["id"], r.RowID))
	}
	if rr, _ := t["repo_root"].(string); !reHash.MatchString(rr) || rr != wantRepoRoot {
		*v = append(*v, fmt.Sprintf("%s: repo_root %v is not h(workspace) under the install key", where, t["repo_root"]))
	}
	branchOK := false
	if b, ok := t["branch"].(string); !ok || !reBranch.MatchString(b) {
		*v = append(*v, fmt.Sprintf("%s: branch %q does not match ^x*$ (only the name's length may ship; the name never leaves the device)",
			where, fmt.Sprint(t["branch"])))
	} else if b != "" {
		branchOK = true
		if n := utf8.RuneCountInString(canaryBranch); len(b) != n {
			*v = append(*v, fmt.Sprintf("%s: branch placeholder has length %d, want %d (the fixture branch's rune count)", where, len(b), n))
		}
	}
	if p, _ := t["phase"].(string); !phases[p] {
		*v = append(*v, fmt.Sprintf("%s: phase %v is not a phase", where, t["phase"]))
	}
	files, ok := t["files"].(map[string]any)
	if !ok {
		*v = append(*v, fmt.Sprintf("%s: files is not an object", where))
	}
	for k, n := range files {
		if !rePathTok.MatchString(k) {
			*v = append(*v, fmt.Sprintf("%s: files key %q is not h+ext", where, k))
		}
		if !isInt(n) {
			*v = append(*v, fmt.Sprintf("%s: files[%q] is not an integer", where, k))
		}
	}
	for _, k := range []string{"started_at", "last_active", "commit_count", "test_runs", "test_fails"} {
		if !isInt(t[k]) {
			*v = append(*v, fmt.Sprintf("%s: task %s is not an integer", where, k))
		}
	}
	return branchOK
}

// rawPath: a `/` or `\` with more than one non-empty segment.
func rawPath(s string) bool {
	if !strings.ContainsAny(s, `/\`) {
		return false
	}
	n := 0
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg != "" {
			n++
		}
	}
	return n > 1
}

// credential is this gate's own detector, independent of minimise.go's
// (so weakening the producer's detector cannot weaken the gate).
var credPrefixes = []string{
	"sk-", "sk_", "pk_", "rk_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
	"xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "akia", "asia", "aiza", "eyj", "ya29.", "npm_", "pypi-", "hf_",
}

var userPass = regexp.MustCompile(`[^\s:/@]+:[^\s@]+@`)

func credential(tok string) bool {
	t := strings.Trim(tok, `"'`)
	l := strings.ToLower(t)
	for _, p := range credPrefixes {
		if strings.HasPrefix(l, p) && len(t) > len(p)+3 {
			return true
		}
	}
	if strings.Contains(t, "://") || userPass.MatchString(t) {
		return true
	}
	if i := strings.Index(t, "="); i > 0 && i < len(t)-1 {
		return true
	}
	if strings.HasPrefix(t, "-p") && len(t) > 4 { // mysql -p<password>
		return true
	}
	if len(t) >= 20 {
		var letters, digits int
		for _, r := range t {
			switch {
			case r >= '0' && r <= '9':
				digits++
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
				letters++
			}
		}
		if letters > 0 && digits > 0 {
			return true
		}
	}
	return false
}
