package rpc

// mlproducer_acceptance_test.go — ml-producer-01MLPRD01 WP04: spec §8
// acceptance, end to end. A scripted Anthropic-protocol model drives a
// real chat turn (newLLMStack's chat runner over newGraphManagerWithDeps'
// kernel), the agent's tool calls run through the real fs builtins and
// the real kenaz__bash (Cedar-granted), the real recorder records them,
// and the real shipper posts protobuf through fleet.Client to the fake
// Fleet of mlproducer_wiring_test.go, which decodes it the way its
// receiver does. Assertions are on what Fleet received.
//
// §8.2 (gate matrix + ml_not_effective mid-stream), §8.3 (notice) and
// §8.4 (process markers) are mapped in docs/dogfood/2026-10-09-ml-producer.md;
// the tests that close the §8.2 / §8.4 gaps live below.
//
// NOT t.Parallel: fleet.SetExternalTokenSource is process-global.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/session"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	corefsbuiltins "github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
)

// mlAcceptOrg is the org every acceptance rig enrolls as. Any org ships
// once its gate is open (WP05 removed the dev-org-only guard); this is the
// owner's dev org so the fixtures match the §8.5 live target.
const mlAcceptOrg = "fa81ec53-d374-4c48-8b72-dd6b8584d968"

// scriptedToolCall is one model step: a single tool_use block.
type scriptedToolCall struct {
	name string
	args map[string]any
}

// scriptedAnthropic serves the steps in order (one per model request),
// then a final text "done" turn. Request n (1-based) gets steps[n-1].
func scriptedAnthropic(t *testing.T, steps []scriptedToolCall) (*httptest.Server, *int32) {
	t.Helper()
	var reqN int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := int(atomic.AddInt32(&reqN, 1))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		var frames []string
		if n <= len(steps) {
			st := steps[n-1]
			args, _ := json.Marshal(st.args)
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_` + strconv.Itoa(n) + `","role":"assistant","model":"zz-ml-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_` + strconv.Itoa(n) + `","name":"` + st.name + `","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + strconv.Quote(string(args)) + `}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		} else {
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_end","role":"assistant","model":"zz-ml-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &reqN
}

// cedarBashGrant is the policy text that allows exactly cmd's pattern.
func cedarBashGrant(cmd string) string {
	pattern := corebash.DerivePattern(corebash.FirstSegmentArgv(cmd))
	return "permit(\n  principal,\n  action == Action::\"run_bash_command\",\n  resource == BashCommand::\"" + pattern + "\"\n);\n"
}

// mlChat is a production chat stack (newLLMStack over
// newGraphManagerWithDeps, the rig's producer wiring threaded through
// both) talking to a scripted model.
type mlChat struct {
	t     *testing.T
	r     *mlWiringRig
	stack llmStack
	prof  corellm.ProviderProfile
	reqN  *int32
}

func newMLChat(t *testing.T, r *mlWiringRig, steps []scriptedToolCall, taskReg *coretasks.Registry) *mlChat {
	t.Helper()
	srv, reqN := scriptedAnthropic(t, steps)
	cedarEngine := buildCedarEngineOrNil(r.dataDir, nil)
	memStore := openMemoryStore(r.c)
	if cedarEngine == nil || memStore == nil {
		t.Fatal("cedar engine / memory store unavailable over a real DataDir")
	}
	bashStore := corebash.NewStore()
	graphMgr, _, _, _ := newGraphManagerWithDeps(r.c, nil, nil, memStore, nil, bashStore, nil, cedarEngine, nil, nil, r.w)
	stack := newLLMStack(r.c, NewStreamBroker(NewMultiEmitter()), newPersonalStore(r.c), nil, nil, func() bool { return false },
		nil, nil, r.api, bashStore, nil, graphMgr, nil, nil, nil, nil,
		nil, nil, nil, nil, confirmAuditEmitter{}, nil, cedarEngine, taskReg, nil, nil, nil, r.w)
	if stack.compactionScheduler != nil {
		t.Cleanup(stack.compactionScheduler.Stop)
	}
	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))
	t.Setenv("ZZ_ML_ACCEPT_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{ID: "zz-ml-accept", Kind: anthropic.Kind, Model: "default",
		Cred: corellm.CredentialReference{Kind: "env", Locator: "ZZ_ML_ACCEPT_KEY"}}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	return &mlChat{t: t, r: r, stack: stack, prof: prof, reqN: reqN}
}

// start creates an attended session and starts one user turn in it.
func (m *mlChat) start(name string) session.Record {
	m.t.Helper()
	ctx := context.Background()
	sess, err := m.r.c.SessionManager().Create(ctx, name)
	if err != nil {
		m.t.Fatal(err)
	}
	row, err := m.r.c.SessionManager().AppendMessage(ctx, sess.ID, session.Message{Role: session.RoleUser, Content: "go"})
	if err != nil {
		m.t.Fatal(err)
	}
	if _, err := m.stack.chatRunner.StartStream(ctx, m.prof.ID, sess.ID, "", chat.UserTurn{MessageID: row.ID, Text: "go", Announce: true}); err != nil {
		m.t.Fatalf("StartStream: %v", err)
	}
	return sess
}

// toolOutputs waits for the turn's final "done" and returns the JSON tool
// results in order.
func (m *mlChat) toolOutputs(sessionID string) []string {
	m.t.Helper()
	var outs []string
	waitForCond(m.t, func() bool {
		outs = outs[:0]
		msgs, _ := m.stack.historyAdapter.ListMessages(context.Background(), sessionID)
		done := false
		for _, msg := range msgs {
			if msg.Role == string(session.RoleTool) && strings.HasPrefix(strings.TrimSpace(msg.Content), "{") {
				outs = append(outs, msg.Content)
			}
			if msg.Role == string(session.RoleAssistant) && strings.Contains(msg.Content, "done") {
				done = true
			}
		}
		return done
	}, "the scripted turn to finish")
	return outs
}

// wireRecord is one OTLP log record as Fleet's ML lane reads it.
type wireRecord struct {
	attrs map[string]any
	body  map[string]any
	raw   string
}

func wireRecords(t *testing.T, posts []*collogs.ExportLogsServiceRequest) []wireRecord {
	t.Helper()
	var out []wireRecord
	for _, p := range posts {
		for _, rl := range p.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				for _, lr := range sl.GetLogRecords() {
					wr := wireRecord{attrs: map[string]any{}}
					for _, kv := range lr.GetAttributes() {
						if s := kv.GetValue().GetStringValue(); s != "" {
							wr.attrs[kv.GetKey()] = s
						} else {
							wr.attrs[kv.GetKey()] = kv.GetValue().GetIntValue()
						}
					}
					wr.raw = lr.GetBody().GetStringValue()
					if err := json.Unmarshal([]byte(wr.raw), &wr.body); err != nil {
						t.Fatalf("record body is not JSON: %v (%q)", err, wr.raw)
					}
					out = append(out, wr)
				}
			}
		}
	}
	return out
}

// §8.1: a session that writes 3 files, runs `go test ./...` (exit 1) and
// `git commit -m x` (exit 0) ships, per A-10, one coalesced task upsert
// and file×3, terminal×2, commit×1, phase_change (coding, testing) and
// agent.turn — with exactly the contract attributes and nothing raw.
func TestMLAcceptance_8_1_ChatSessionShipsContractRecords(t *testing.T) {
	const testCmd = "go test ./..."
	const commitCmd = "git commit -m x"

	// The real toolchain runs: `go test ./...` in a workspace with no
	// go.mod exits 1 (a failing suite, as far as the agent can tell), and
	// the workspace is made a repository with one staged file so
	// `git commit -m x` exits 0.
	for _, bin := range []string{"go", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}

	store := newTestStore(t)
	if err := store.SaveFSWriteEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBash(true); err != nil {
		t.Fatal(err)
	}
	r := newMLWiringRigWithStore(t, mlAcceptOrg, func(dataDir string) {
		polDir := filepath.Join(dataDir, cedar.PolicyDir)
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			t.Fatal(err)
		}
		grants := cedarBashGrant(testCmd) + cedarBashGrant(commitCmd) +
			"permit(\n  principal,\n  action == Action::\"" + cedar.ActionWriteFilesystem + "\",\n  resource\n);\n"
		if err := os.WriteFile(filepath.Join(polDir, "zz_ml_accept.cedar"), []byte(grants), 0o644); err != nil {
			t.Fatal(err)
		}
	}, store)

	ws := r.c.WorkspaceDir()
	if ws == "" {
		t.Fatal("no workspace dir")
	}
	files := []string{
		filepath.Join(ws, "zzaccept", "deep", "alpha.go"),
		filepath.Join(ws, "zzaccept", "deep", "beta.go"),
		filepath.Join(ws, "zzaccept", "gamma_test.go"),
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vcs := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	vcs("init", "-q")
	vcs("config", "user.name", "zz accept")
	vcs("config", "user.email", "zz@accept.invalid")
	vcs("config", "commit.gpgsign", "false")
	vcs("config", "core.hooksPath", "/dev/null")
	if err := os.WriteFile(filepath.Join(ws, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vcs("add", "seed.txt")
	steps := []scriptedToolCall{
		{corefsbuiltins.NameWriteFile, map[string]any{"path": files[0], "content": "package deep\n"}},
		{corefsbuiltins.NameWriteFile, map[string]any{"path": files[1], "content": "package deep\n"}},
		{corefsbuiltins.NameWriteFile, map[string]any{"path": files[2], "content": "package zzaccept\n"}},
		{corebash.Name, map[string]any{"command": testCmd}},
		{corebash.Name, map[string]any{"command": commitCmd}},
	}
	ctx := context.Background()
	if !r.w.gate.Refresh(ctx).Open {
		t.Fatalf("gate closed: %+v", r.w.gate.Last())
	}
	mc := newMLChat(t, r, steps, nil)
	sess := mc.start("zz-ml-accept")
	// The turn is over when agent.turn is in the outbox.
	waitForCond(t, func() bool {
		for _, rec := range r.outbox() {
			if strings.Contains(string(rec.Body), `"kind":"`+mlproducer.KindTurn+`"`) {
				return true
			}
		}
		return false
	}, "agent.turn in the outbox")
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("the agent's write did not happen (the tool never ran): %v", err)
		}
	}
	if got := atomic.LoadInt32(mc.reqN); got != int32(len(steps)+1) {
		t.Fatalf("model requests = %d, want %d", got, len(steps)+1)
	}
	if posts, _, _, _ := r.fleet.snapshot(); len(posts) != 0 {
		t.Fatalf("shipped %d batch(es) before the drain; the shipper was not started", len(posts))
	}

	// App shutdown: recorder flush, trailing task upsert, final ship.
	r.w.shutdown()

	posts, cts, _, _ := r.fleet.snapshot()
	if len(posts) == 0 {
		t.Fatal("nothing reached Fleet on the shutdown drain")
	}
	for _, ct := range cts {
		if ct != mlproducer.OTLPContentType {
			t.Fatalf("content type %q, want %s", ct, mlproducer.OTLPContentType)
		}
	}
	recs := wireRecords(t, posts)

	hasher := mlproducer.NewHasher(r.dataDir)
	sh, err := hasher.H(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	taskID := mlproducer.TaskIDPrefix + sh
	wantRepo, _ := hasher.H(ws)

	kinds := map[string]int{}
	var tasks, terms, phases []map[string]any
	var turn map[string]any
	wantAttrKeys := []string{"kameas.ml.table", "kameas.ml.op", "kameas.ml.row_id", "kameas.ml.source", "kameas.ml.schema_version"}
	for _, wr := range recs {
		if len(wr.attrs) != len(wantAttrKeys) {
			t.Errorf("record attrs = %v, want exactly %v", wr.attrs, wantAttrKeys)
		}
		for _, k := range wantAttrKeys {
			if _, ok := wr.attrs[k]; !ok {
				t.Errorf("record attrs %v lack %s", wr.attrs, k)
			}
		}
		if wr.attrs["kameas.ml.source"] != "harness" || wr.attrs["kameas.ml.schema_version"] != int64(1) {
			t.Errorf("source / schema_version = %v / %v", wr.attrs["kameas.ml.source"], wr.attrs["kameas.ml.schema_version"])
		}
		switch wr.attrs["kameas.ml.table"] {
		case "tasks":
			if wr.attrs["kameas.ml.op"] != "upsert" || wr.attrs["kameas.ml.row_id"] != taskID {
				t.Errorf("task record attrs = %v", wr.attrs)
			}
			tasks = append(tasks, wr.body)
		case "events":
			if wr.attrs["kameas.ml.op"] != "insert" || fmt.Sprint(wr.body["id"]) != wr.attrs["kameas.ml.row_id"] {
				t.Errorf("event record attrs = %v body id %v", wr.attrs, wr.body["id"])
			}
			kind, _ := wr.body["kind"].(string)
			kinds[kind]++
			payload, _ := wr.body["payload"].(map[string]any)
			if payload["task"] != taskID {
				t.Errorf("%s payload task = %v, want %s", kind, payload["task"], taskID)
			}
			allowed := map[string]bool{}
			for _, k := range mlproducer.PayloadKeys(kind) {
				allowed[k] = true
			}
			for k := range payload {
				if !allowed[k] {
					t.Errorf("%s payload key %q is outside A-10", kind, k)
				}
			}
			switch kind {
			case mlproducer.KindTerminal:
				terms = append(terms, payload)
			case mlproducer.KindPhaseChange:
				phases = append(phases, payload)
			case mlproducer.KindTurn:
				turn = payload
			}
		default:
			t.Errorf("unknown table %v", wr.attrs["kameas.ml.table"])
		}
	}

	want := map[string]int{
		mlproducer.KindFile: 3, mlproducer.KindTerminal: 2, mlproducer.KindCommit: 1,
		mlproducer.KindPhaseChange: 2, mlproducer.KindTurn: 1,
	}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("%s × %d on the wire, want %d (all kinds: %v)", k, kinds[k], n, kinds)
		}
	}
	if len(kinds) != len(want) {
		t.Errorf("kinds on the wire = %v, want exactly %v (no agent.tool: every call has an engine kind)", kinds, want)
	}
	if len(phases) == 2 && (phases[0]["phase"] != "coding" || phases[1]["phase"] != "testing") {
		t.Errorf("phase_change = %v, want coding then testing", phases)
	}
	if len(terms) == 2 {
		if terms[0]["cmd"] != "go test" || terms[0]["exit_code"] != float64(1) ||
			terms[1]["cmd"] != "git commit" || terms[1]["exit_code"] != float64(0) {
			t.Errorf("terminal payloads = %v", terms)
		}
	}
	if turn == nil || turn["tool_calls"] != float64(5) || turn["model_calls"] != float64(6) || turn["outcome"] != "completed" {
		t.Errorf("agent.turn payload = %v, want tool_calls 5, model_calls 6, completed", turn)
	}

	if len(tasks) != 1 {
		t.Fatalf("task upserts on the wire = %d, want 1 (coalesced)", len(tasks))
	}
	task := tasks[0]
	fileKeys, _ := task["files"].(map[string]any)
	if task["id"] != taskID || task["repo_root"] != wantRepo || task["branch"] != "" ||
		task["test_runs"] != float64(1) || task["test_fails"] != float64(1) || task["commit_count"] != float64(1) ||
		len(fileKeys) != 3 || task["phase"] != "testing" {
		t.Errorf("task = %v; want id %s, repo_root h(ws), test_runs 1, test_fails 1, commit_count 1, 3 files, phase testing", task, taskID)
	}
	for k := range fileKeys {
		if len(k) != 16+len(".go") || !strings.HasSuffix(k, ".go") {
			t.Errorf("files key %q is not h(abs)+.go", k)
		}
	}

	// The request bytes: no raw path, no full command, no forbidden kind.
	for i, p := range posts {
		raw, err := proto.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		leaks := []string{ws, "zzaccept", "alpha.go", "beta.go", "gamma_test", testCmd, commitCmd, "./...", sess.ID, "package deep"}
		for _, f := range files {
			leaks = append(leaks, f)
		}
		for _, leak := range leaks {
			if strings.Contains(s, leak) {
				t.Errorf("post %d bytes contain %q", i, leak)
			}
		}
		for _, k := range []string{"edit", "file_edit", "save", "git", "process", "hyprland", "browser", "power", "agent.commit", "agent.phase"} {
			if strings.Contains(s, `"kind":"`+k+`"`) {
				t.Errorf("post %d carries forbidden kind %q", i, k)
			}
		}
	}
}

// §8.2's mid-stream half, end to end through the running shipper loop:
// records are flowing, then Fleet withdraws consent and answers the next
// batch 403 ml_not_effective — the loop stops, the unsent outbox is
// purged, the gate closes, and nothing recorded afterwards is ever sent.
// (The effective-rule matrix itself: TestMLWiring_GateMatrixOverContract-
// EffectiveTable; the single-batch 403: TestMLWiring_ResponseTable-
// ThroughRealClient.)
func TestMLAcceptance_8_2_NotEffectiveMidStreamStopsAndPurges(t *testing.T) {
	r := newMLWiringRig(t, mlAcceptOrg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.w.start(ctx)
	waitForCond(t, r.w.shipper.Running, "shipper starts once the gate opens")

	r.toolCall("s1", "kenaz__read_file", `{"path":"a.go"}`, "x")
	_ = r.w.rec.Flush(context.Background())
	r.w.shipper.Nudge()
	waitForCond(t, func() bool { posts, _, _, _ := r.fleet.snapshot(); return len(posts) >= 1 }, "first batch accepted")

	// The member withdraws while a batch is in flight: the pre-batch
	// /me/ml read still says effective, ingest refuses the batch, and
	// /me/ml says not-effective from then on.
	var fleetRef = r.fleet
	r.fleet.set(func(f *mlFleet) {
		f.otlp = func(int, *collogs.ExportLogsServiceRequest) otlpReply {
			fleetRef.set(func(f *mlFleet) { f.meML = mlEffectiveBody(true, "member_choice", false, true) })
			return otlpReply{status: 403, body: `{"code":"ml_not_effective","message":"withdrawn"}`}
		}
	})
	// Seed the outbox directly: the recording gate's ≤60 s cache may still
	// read open, but these rows must be purged, never sent.
	r.toolCall("s1", "kenaz__read_file", `{"path":"b.go"}`, "x")
	r.toolCall("s1", "kenaz__read_file", `{"path":"c.go"}`, "x")
	_ = r.w.rec.Flush(context.Background())
	if len(r.outbox()) == 0 {
		t.Fatal("nothing pending before the withdrawal batch")
	}
	before, _, _, _ := r.fleet.snapshot()
	r.w.shipper.Nudge()
	waitForCond(t, func() bool { return !r.w.shipper.Running() }, "shipper stops on ml_not_effective")
	if left := len(r.outbox()); left != 0 {
		t.Fatalf("outbox = %d after ml_not_effective, want purged", left)
	}
	if r.w.gate.Recording() {
		t.Fatal("recording gate still open after ml_not_effective")
	}
	r.toolCall("s1", "kenaz__read_file", `{"path":"d.go"}`, "x")
	if left := len(r.outbox()); left != 0 {
		t.Fatalf("recorded %d rows after withdrawal", left)
	}
	time.Sleep(200 * time.Millisecond)
	after, _, _, _ := r.fleet.snapshot()
	if len(after) != len(before)+1 {
		t.Fatalf("posts %d → %d; want exactly the one refused batch after withdrawal", len(before), len(after))
	}
	if v := r.w.shippingStatus(); v.StopReason == "" {
		t.Error("panel shows no stop reason after ml_not_effective")
	}
}

// §8.4, bash half, through production wiring: a chat turn's foreground
// AND background kenaz__bash children carry KENAZ_ACTOR=agent and
// KENAZ_SESSION = h(session) under the install key — the value the
// daemon will match on. (MCP stdio: TestPoolOpen_SpawnedServerCarries-
// AgentActor in core/mcp/transport/stdio; the unit halves:
// core/tools/bash/env_provider_test.go.) The markers do not depend on
// consent: the gate is left closed here on purpose.
func TestMLAcceptance_8_4_BashForegroundAndBackgroundCarryMarkers(t *testing.T) {
	const fgCmd = "env"
	store := newTestStore(t)
	if err := store.SaveBash(true); err != nil {
		t.Fatal(err)
	}
	r := newMLWiringRigWithStore(t, mlAcceptOrg, func(dataDir string) {
		polDir := filepath.Join(dataDir, cedar.PolicyDir)
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(polDir, "zz_ml_markers.cedar"), []byte(cedarBashGrant(fgCmd)), 0o644); err != nil {
			t.Fatal(err)
		}
	}, store)
	r.fleet.set(func(f *mlFleet) { f.meML = mlEffectiveBody(true, "off", false, true) })
	if r.w.gate.Refresh(context.Background()).Open {
		t.Fatal("gate open; this test proves the markers do not depend on it")
	}

	taskReg := coretasks.NewRegistry(coretasks.Options{})
	mc := newMLChat(t, r, []scriptedToolCall{
		{corebash.Name, map[string]any{"command": fgCmd}},
		{corebash.Name, map[string]any{"command": fgCmd, "run_in_background": true}},
	}, taskReg)
	sess := mc.start("zz-ml-markers")
	outs := mc.toolOutputs(sess.ID)
	if len(outs) != 2 {
		t.Fatalf("tool results = %d (%v), want 2", len(outs), outs)
	}
	wantSession, err := mlproducer.NewHasher(r.dataDir).H(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	markers := func(envDump string) (actor, sess string) {
		for _, line := range strings.Split(envDump, "\n") {
			if v, ok := strings.CutPrefix(line, "KENAZ_ACTOR="); ok {
				actor = v
			}
			if v, ok := strings.CutPrefix(line, "KENAZ_SESSION="); ok {
				sess = v
			}
		}
		return
	}

	var fg struct {
		Stdout   string `json:"stdout"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(outs[0]), &fg); err != nil || fg.ExitCode != 0 {
		t.Fatalf("foreground result %q: %v", outs[0], err)
	}
	if a, s := markers(fg.Stdout); a != "agent" || s != wantSession {
		t.Errorf("foreground env: KENAZ_ACTOR=%q KENAZ_SESSION=%q, want agent / %s", a, s, wantSession)
	}

	var bg struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(outs[1]), &bg); err != nil || bg.TaskID == "" {
		t.Fatalf("background result %q: %v", outs[1], err)
	}
	var dump string
	waitForCond(t, func() bool {
		lines, _, ok := taskReg.Tail(bg.TaskID, 0)
		if !ok {
			return false
		}
		var b strings.Builder
		for _, l := range lines {
			if l.Stream == "stdout" {
				b.WriteString(l.Text + "\n")
			}
		}
		dump = b.String()
		return strings.Contains(dump, "KENAZ_SESSION=")
	}, "background env output")
	if a, s := markers(dump); a != "agent" || s != wantSession {
		t.Errorf("background env: KENAZ_ACTOR=%q KENAZ_SESSION=%q, want agent / %s", a, s, wantSession)
	}
}

// A-3 end to end: a subagent dispatched from an attended session, through
// the real spawner (NewSubagentRunSpawner with MLParent = the producer's
// linker, as api.go wires it) and a real chat runner, makes a tool call;
// the call and the child's turn land on the ROOT's task. The child run is
// unattended (the spawner marks it so), so without the link the recorder
// would drop it — the control half dispatches from a scheduled
// (unattended, unlinked) parent and records nothing.
func TestMLAcceptance_SubagentOfAttendedSessionLandsOnRootTask(t *testing.T) {
	r := newMLWiringRig(t, mlAcceptOrg, nil)
	if !r.w.gate.Refresh(context.Background()).Open {
		t.Fatalf("gate closed: %+v", r.w.gate.Last())
	}
	var calls atomic.Int32
	model := coreag.LLMProviderFunc(func(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
		// Odd calls: the child asks for a tool; even calls: it answers.
		if calls.Add(1)%2 == 1 {
			return coreag.LLMResponse{FinishReason: "tool_calls", ToolCalls: []coreag.ToolCallRequest{
				{ID: "tc-" + strconv.Itoa(int(calls.Load())), Name: "kenaz__glob", Arguments: `{"pattern":"**/*.go"}`},
			}}, nil
		}
		if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
			sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "child done"})
		}
		return coreag.LLMResponse{Content: "child done", FinishReason: "stop"}, nil
	})
	stack := buildSubagentSpawnerTestStackWithLLM(t, model, func(cfg *chat.Config) {
		prev := cfg.EnvDefaults
		cfg.EnvDefaults = func(env *coreag.Env) {
			prev(env)
			env.ToolCalls = r.w.toolCallObserver()
		}
		cfg.TurnUsage = r.w.turnObserver()
	})
	stack.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM:            stack.llmAPI,
		Bus:            stack.bus,
		Tasks:          stack.tasks,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        10 * time.Second,
		MLParent:       r.w.parentLinker(),
	}))
	tool := coresubagent.New(coresubagent.Options{DataDir: t.TempDir(), Seam: stack.seam})
	dispatch := func(ctx context.Context, parentID string) {
		t.Helper()
		raw, err := tool.Call(toolloop.WithSessionID(ctx, parentID),
			json.RawMessage(`{"profile":"explore","prompt":"look around","run_in_background":false}`))
		if err != nil {
			t.Fatalf("subagent dispatch: %v", err)
		}
		var res map[string]any
		_ = json.Unmarshal(raw, &res)
		if res["status"] != "complete" {
			t.Fatalf("subagent result = %v", res)
		}
	}
	kindsByTask := func() map[string]map[string]int {
		out := map[string]map[string]int{}
		for _, rec := range r.outbox() {
			if rec.Table != "events" {
				continue
			}
			var ev struct {
				Kind    string         `json:"kind"`
				Payload map[string]any `json:"payload"`
			}
			_ = json.Unmarshal(rec.Body, &ev)
			task, _ := ev.Payload["task"].(string)
			if out[task] == nil {
				out[task] = map[string]int{}
			}
			out[task][ev.Kind]++
		}
		return out
	}

	// Control: a scheduled (unattended) parent's subagent records nothing.
	sched, err := stack.sessionsAPI.Create(context.Background(), "scheduled parent")
	if err != nil {
		t.Fatal(err)
	}
	dispatch(runposture.Unattended(context.Background()), sched.ID)
	time.Sleep(100 * time.Millisecond)
	if got := kindsByTask(); len(got) != 0 {
		t.Fatalf("a scheduled parent's subagent recorded %v", got)
	}

	// Attended parent: the child's work lands on the parent's task.
	parent, err := stack.sessionsAPI.Create(context.Background(), "attended parent")
	if err != nil {
		t.Fatal(err)
	}
	dispatch(context.Background(), parent.ID)
	h, err := mlproducer.NewHasher(r.dataDir).H(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	rootTask := mlproducer.TaskIDPrefix + h
	var got map[string]map[string]int
	waitForCond(t, func() bool {
		got = kindsByTask()
		return got[rootTask][mlproducer.KindTurn] >= 1
	}, "the child's agent.turn on the root task")
	if len(got) != 1 {
		t.Fatalf("events spread over %d tasks %v, want only the root's %s", len(got), got, rootTask)
	}
	if got[rootTask][mlproducer.KindTool] < 1 {
		t.Fatalf("root task events = %v, want the child's kenaz__glob call as agent.tool", got[rootTask])
	}
}
