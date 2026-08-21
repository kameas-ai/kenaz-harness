package workflows_test

// integration_artifacts_test.go — automation-actually-runs-01PMZ404
// UNIT-5, AC-006's "drive doc_generator.yaml end to end against a fake
// LLM" requirement.
//
// Before UNIT-5, corewf.Deps.Artifacts and Deps.SessionID (or its
// run-scoped successor, RunContext.ParentSessionID) were never assigned
// in production, so the shipped doc_generator builtin
// (core/workflows/builtin/doc_generator.yaml) burned a full
// README-synthesis model_turn and then failed on its final
// write_artifact step with "no Artifacts wired". This test wires a
// real sqlite-backed artifacts stack (Store + Manager + MediaStore —
// CLAUDE.md blind spot #2: a memory-store fixture would skip the SQL
// encode/decode path and the D-7 source CHECK constraint) plus a real
// session row, drives the actual builtin YAML (not a synthetic
// approximation) through NewEngineWithDeps with a fake LLMStreamer, and
// asserts the final step succeeds and the artifact row lands with the
// rendered title, the run's session, and Source == SourceModelOutput.

import (
	"context"
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

// testArtifactsAdapter mirrors core/rpc's wfArtifactsAdapter (which
// cannot be imported here — core/workflows must not import core/rpc,
// DIRECTIVE-001 — and this is a test-only, external-package file so
// even that constraint is moot; the duplication instead keeps this
// test independent of the production adapter's own correctness, which
// core/rpc/wf_artifacts_adapter_test.go covers separately).
type testArtifactsAdapter struct {
	store coreart.Store
	mgr   *coreart.Manager
	media coreatt.MediaStore
}

func (a *testArtifactsAdapter) Read(ctx context.Context, id string) (workflows.ArtifactView, error) {
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return workflows.ArtifactView{}, err
	}
	_, body, err := a.media.GetByHash(ctx, row.ContentHash)
	if err != nil {
		return workflows.ArtifactView{}, err
	}
	return workflows.ArtifactView{ID: row.ID, Title: row.Title, MimeType: row.MimeType, Content: body}, nil
}

func (a *testArtifactsAdapter) Write(ctx context.Context, in workflows.ArtifactWrite) (string, error) {
	rows, err := a.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: in.Title, MimeType: in.MimeType, Bytes: in.Content, Source: coreart.SourceModelOutput,
	}}, in.SessionID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].ID, nil
}

// artifactsFakeLLMStreamer always returns a canned README body.
type artifactsFakeLLMStreamer struct {
	body  string
	calls int
}

func (f *artifactsFakeLLMStreamer) Stream(_ context.Context, _ workflows.LLMRequest) (workflows.LLMStream, error) {
	f.calls++
	ch := make(chan workflows.LLMStreamEvent, 1)
	ch <- workflows.LLMStreamEvent{Text: f.body}
	close(ch)
	return &artifactsFakeLLMStream{text: f.body, ch: ch}, nil
}

type artifactsFakeLLMStream struct {
	text string
	ch   chan workflows.LLMStreamEvent
}

func (s *artifactsFakeLLMStream) Events() <-chan workflows.LLMStreamEvent { return s.ch }
func (s *artifactsFakeLLMStream) Final() (string, error)                  { return s.text, nil }

// Test_DocGenerator_EndToEnd_WritesArtifact drives the ACTUAL shipped
// doc_generator builtin (not a synthetic stand-in) through a real
// engine with a fake LLM and a real sqlite artifacts stack.
func Test_DocGenerator_EndToEnd_WritesArtifact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Real sqlite DB — artifacts.Store / Manager / MediaStore all sit on
	// top of the real SQL encode/decode path, and the `source` column's
	// CHECK constraint (D-7) is only enforced here, not in a memory
	// fixture.
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
	rec, err := sessMgr.Create(ctx, "doc-generator probe session")
	if err != nil {
		t.Fatalf("session Create: %v", err)
	}

	media := coreatt.NewSQLMediaStore(db, dir)
	artStore := coreart.NewSQLStore(db)
	artMgr := coreart.NewManager(artStore, media)

	// Load the ACTUAL shipped builtin catalog and pull doc_generator out
	// of it, rather than approximating its YAML — this is the exact
	// workflow spec §1.5 cites as burning a model turn and then failing.
	builtins, loadErrs := workflows.LoadBuiltins()
	for _, e := range loadErrs {
		t.Fatalf("LoadBuiltins error: %v", e)
	}
	var wf workflows.Workflow
	found := false
	for _, w := range builtins {
		if w.ID == "doc_generator" {
			wf = w
			found = true
			break
		}
	}
	if !found {
		t.Fatal("doc_generator not found in LoadBuiltins() — the shipped builtin catalog changed; update this test's target")
	}

	llm := &artifactsFakeLLMStreamer{body: "# core/workflows\n\nA generated README body."}
	deps := workflows.Deps{
		LLM:               llm,
		DefaultLLMProfile: "default",
		Artifacts:         &testArtifactsAdapter{store: artStore, mgr: artMgr, media: media},
	}
	engine := workflows.NewEngineWithDeps(deps)

	// pkg_path's own default is "core/workflows", relative to a repo-root
	// CWD; `go test` runs with CWD already set to this package directory,
	// so override to "." — the shell steps (find/head) then run against
	// THIS package's real files, no fixture directory needed.
	inputs := map[string]workflows.TypedValue{
		"pkg_path": {Type: workflows.ValueTypeText, Text: "."},
	}
	run, err := engine.Run(ctx, wf, inputs, workflows.RunOptions{ParentSessionID: rec.ID})
	if err != nil {
		t.Fatalf("Run: %v (status=%q, err=%q)", err, run.Status, run.Err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed (err=%q)", run.Status, run.Err)
	}

	if llm.calls != 1 {
		t.Errorf("LLM.calls = %d, want 1 (synthesize_readme should run exactly once)", llm.calls)
	}

	var saveStep *workflows.StepResult
	for i := range run.Steps {
		if run.Steps[i].Name == "save" {
			saveStep = &run.Steps[i]
			break
		}
	}
	if saveStep == nil {
		t.Fatal("no \"save\" step in the run — doc_generator's shape changed")
	}
	if saveStep.Status != "completed" {
		t.Fatalf("save step status = %q, err = %q — this is the exact pre-UNIT-5 failure "+
			"(\"no Artifacts wired\" / \"no SessionID configured\")", saveStep.Status, saveStep.Err)
	}

	// Read the artifact row back through the same real Store, asserting
	// on the table — not on the runner's TypedValue — per the mission's
	// "assert on the table" discipline.
	rows, err := artStore.List(ctx, coreart.ArtifactFilter{SessionID: rec.ID})
	if err != nil {
		t.Fatalf("artStore.List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("artifacts rows for session %q = %d, want 1", rec.ID, len(rows))
	}
	got := rows[0]
	if !strings.Contains(got.Title, "README.md") {
		t.Errorf("artifact Title = %q, want it to contain README.md (doc_generator's title template)", got.Title)
	}
	if got.SessionID != rec.ID {
		t.Errorf("artifact SessionID = %q, want %q", got.SessionID, rec.ID)
	}
	if got.Source != coreart.SourceModelOutput {
		t.Errorf("artifact Source = %q, want %q (D-7 — no new source value)", got.Source, coreart.SourceModelOutput)
	}
	if got.ByteSize == 0 {
		t.Error("artifact ByteSize is 0 — the README body was not actually stored")
	}
}
