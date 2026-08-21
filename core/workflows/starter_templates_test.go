package workflows_test

// starter_templates_test.go — automation-actually-runs-01PMZ404 UNIT-9,
// AC-010. Validates the exact three fixtures
// core/workflows/testdata/starter_templates/*.yaml through the REAL
// backend loader/validator (not a string-comparison of assembled YAML —
// spec's own caution: "fails if the test compares assembled strings to
// expected strings, that passes with path:/no-title: present, which is
// how this shipped"). The SAME fixture files are asserted byte-for-byte
// against frontend/src/views/workflows/SimpleTemplateEditor.vue's
// assembleYaml() output in
// frontend/.../__tests__/SimpleTemplateEditor.spec.ts, so drift between
// the two sides is caught on whichever side changes first.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	coresession "github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/workflows"

	_ "modernc.org/sqlite"
)

func readTemplateFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "starter_templates", name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

// TestStarterTemplates_LoadYAML_PassesTheRealValidator is AC-010's core
// claim: all three starter templates emit YAML the backend actually
// accepts. Before UNIT-9, http_save's `path:` field silently dropped
// (non-strict yaml.Unmarshal) and the document then failed Validate
// with "write_artifact requires title" — every save of that template
// failed.
func TestStarterTemplates_LoadYAML_PassesTheRealValidator(t *testing.T) {
	for _, name := range []string{"single_llm.yaml", "plan_execute.yaml", "http_save.yaml"} {
		t.Run(name, func(t *testing.T) {
			data := readTemplateFixture(t, name)
			wf, err := workflows.LoadYAML(data)
			if err != nil {
				t.Fatalf("LoadYAML(%s): %v", name, err)
			}
			if wf.ID == "" {
				t.Errorf("%s: loaded workflow has empty ID", name)
			}
		})
	}
}

// promptCapturingLLMStreamer records the Prompt of every Stream call, in
// order, and returns a distinguishing canned response per call so a test
// can trace which output fed which downstream prompt.
type promptCapturingLLMStreamer struct {
	prompts   []string
	responses []string // responses[i] answers the i'th call; last one repeats if exhausted
}

func (f *promptCapturingLLMStreamer) Stream(_ context.Context, req workflows.LLMRequest) (workflows.LLMStream, error) {
	f.prompts = append(f.prompts, req.Prompt)
	idx := len(f.prompts) - 1
	resp := "stub"
	if idx < len(f.responses) {
		resp = f.responses[idx]
	} else if len(f.responses) > 0 {
		resp = f.responses[len(f.responses)-1]
	}
	ch := make(chan workflows.LLMStreamEvent, 1)
	ch <- workflows.LLMStreamEvent{Text: resp}
	close(ch)
	return &artifactsFakeLLMStream{text: resp, ch: ch}, nil
}

// TestStarterTemplates_PlanExecute_SecondPromptContainsFirstOutput is
// AC-010b: run the plan_execute fixture; assert the second model_turn's
// prompt contains the first step's text and does NOT contain "{{" — the
// pre-UNIT-9 mustache token that matched nothing in the engine's ref
// grammar and was passed to the model verbatim, leaving the workflow
// "successful" while never actually chaining.
func TestStarterTemplates_PlanExecute_SecondPromptContainsFirstOutput(t *testing.T) {
	data := readTemplateFixture(t, "plan_execute.yaml")
	wf, err := workflows.LoadYAML(data)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	const planOutput = "PLAN_OUTPUT_MARKER_do_x_then_y"
	llm := &promptCapturingLLMStreamer{responses: []string{planOutput, "execute done"}}
	engine := workflows.NewEngineWithDeps(workflows.Deps{LLM: llm, DefaultLLMProfile: "default"})

	run, err := engine.Run(context.Background(), wf, nil, workflows.RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v (status=%q, err=%q)", err, run.Status, run.Err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed (err=%q)", run.Status, run.Err)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("LLM was called %d times, want 2 (plan, execute)", len(llm.prompts))
	}

	secondPrompt := llm.prompts[1]
	if strings.Contains(secondPrompt, "{{") {
		t.Errorf("second prompt still contains the mustache token: %q", secondPrompt)
	}
	if !strings.Contains(secondPrompt, planOutput) {
		t.Errorf("second prompt = %q, want it to contain the first step's output %q — "+
			"the chain is broken (this is the pre-UNIT-9 defect)", secondPrompt, planOutput)
	}
}

// TestStarterTemplates_HttpSave_StoresNonEmptyJSONEnvelope is AC-010c:
// run the http_save fixture against a local test server; assert the
// stored artifact is non-empty and parses as JSON with status, headers,
// body — not the zero-byte artifact the pre-UNIT-9 `content_ref` bug
// produced (spec §1.6), and not the un-expandable mustache/`.body`
// selector X-10 ruled out.
func TestStarterTemplates_HttpSave_StoresNonEmptyJSONEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Probe", "1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	raw := string(readTemplateFixture(t, "http_save.yaml"))
	// The fixture URL is a placeholder (https://example.com/); point it
	// at the local test server instead of hitting the network.
	raw = strings.Replace(raw, "https://example.com/", srv.URL+"/", 1)
	wf, err := workflows.LoadYAML([]byte(raw))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	ctx := context.Background()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	sessStore := coresession.NewSQLStore(coresession.NewStorageDB(db))
	sessMgr := coresession.NewManager(sessStore)
	rec, err := sessMgr.Create(ctx, "http_save probe session")
	if err != nil {
		t.Fatalf("session Create: %v", err)
	}

	media := coreatt.NewSQLMediaStore(db, dir)
	artStore := coreart.NewSQLStore(db)
	artMgr := coreart.NewManager(artStore, media)

	deps := workflows.Deps{
		Artifacts: &testArtifactsAdapter{store: artStore, mgr: artMgr, media: media},
	}
	engine := workflows.NewEngineWithDeps(deps)

	run, err := engine.Run(ctx, wf, nil, workflows.RunOptions{ParentSessionID: rec.ID})
	if err != nil {
		t.Fatalf("Run: %v (status=%q, err=%q, steps=%+v)", err, run.Status, run.Err, run.Steps)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed (err=%q, steps=%+v)", run.Status, run.Err, run.Steps)
	}

	rows, err := artStore.List(ctx, coreart.ArtifactFilter{SessionID: rec.ID})
	if err != nil {
		t.Fatalf("artStore.List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("artifact rows = %d, want 1", len(rows))
	}
	if rows[0].ByteSize == 0 {
		t.Fatal("http_save artifact has ByteSize 0 — the pre-UNIT-9 content_ref defect (spec §1.6)")
	}

	_, body, err := media.GetByHash(ctx, rows[0].ContentHash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("stored bytes are empty")
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("stored artifact does not parse as JSON: %v (body=%q)", err, string(body))
	}
	for _, key := range []string{"status", "headers", "body"} {
		if _, ok := envelope[key]; !ok {
			t.Errorf("stored JSON envelope missing key %q: %v", key, envelope)
		}
	}
}
