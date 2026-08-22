package rpc

// wf_artifacts_adapter_test.go — automation-actually-runs-01PMZ404 UNIT-5
// (AC-006). Drives the REAL production wiring (core.New + rpc.New over a
// real sqlite DataDir, api_cedar_gate_wiring_test.go's pattern) through
// api.Workflows().RunWithOptions with a real session id, proving
// wfDeps.Artifacts is reached end to end: the artifact row lands in the
// real artifacts table (not a memory-store fixture — CLAUDE.md blind
// spot #2), with the expected session, title, and Source ==
// SourceModelOutput (D-7).
//
// Before this unit, corewf.Deps.Artifacts was never assigned in
// production, so every write_artifact step failed with "no Artifacts
// wired" regardless of session — an engine that is never wired passes
// every core/workflows-package unit test (those construct their own
// Deps with an explicit fake). TestWfArtifactsAdapter_ProductionPath
// distinguishes that from "wired but session-less" by running the SAME
// workflow with and without a SessionID.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

const wfArtifactsProbeYAML = `
id: zz-wfartifacts-probe
name: "wf artifacts probe"
version: 1
steps:
  - name: save
    kind: write_artifact
    title: "probe.md"
    content: "hello from the workflow engine"
    mime_type: "text/markdown"
`

// TestWfArtifactsAdapter_ProductionPath_WriteArtifactSucceeds is the
// AC-006 production-path assertion: a real session-carrying run reaches
// the real Deps.Artifacts, and the artifact row is readable back through
// the artifacts RPC surface with the fields UNIT-5 promised.
func TestWfArtifactsAdapter_ProductionPath_WriteArtifactSucceeds(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	sess, err := api.Sessions().Create(ctx, "wf artifacts probe session")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wfArtifactsProbeYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := api.Workflows().RunWithOptions(ctx, workflowsview.RunRequest{
		ID:        saved.ID,
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("run status = %q, want completed (err=%q, steps=%+v)", res.Status, res.Err, res.Steps)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(res.Steps))
	}
	if res.Steps[0].Err != "" {
		t.Fatalf("write_artifact step failed: %s — wfDeps.Artifacts was not reached", res.Steps[0].Err)
	}

	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(res.Steps[0].Output), &payload); err != nil {
		t.Fatalf("step output %q did not decode as {\"id\":...}: %v", res.Steps[0].Output, err)
	}
	if payload.ID == "" {
		t.Fatal("write_artifact step returned an empty artifact id")
	}

	got, err := api.Artifacts().Get(ctx, payload.ID)
	if err != nil {
		t.Fatalf("Artifacts().Get(%q): %v", payload.ID, err)
	}
	if got.Artifact.SessionID != sess.ID {
		t.Errorf("artifact.SessionID = %q, want %q", got.Artifact.SessionID, sess.ID)
	}
	if got.Artifact.Title != "probe.md" {
		t.Errorf("artifact.Title = %q, want probe.md", got.Artifact.Title)
	}
	if got.Artifact.Source != coreart.SourceModelOutput {
		t.Errorf("artifact.Source = %q, want %q (D-7 — no new source value)", got.Artifact.Source, coreart.SourceModelOutput)
	}
	if string(got.Bytes) != "hello from the workflow engine" {
		t.Errorf("artifact bytes = %q, want the written content", string(got.Bytes))
	}
}

// TestWfArtifactsAdapter_SessionLessRun_FailsLoudly is D-8: a run with
// no session (the Workflows-view run form's shape today) must fail with
// the existing "no SessionID configured" error rather than inventing a
// synthetic session or silently succeeding.
func TestWfArtifactsAdapter_SessionLessRun_FailsLoudly(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wfArtifactsProbeYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := api.Workflows().RunWithOptions(ctx, workflowsview.RunRequest{ID: saved.ID})
	// RunWithOptions itself returns nil at the top level (per-step errors
	// surface in Steps[].Err / res.Err) — assert on the run outcome.
	if err != nil {
		return // also acceptable: an error surfaced at the top level
	}
	if res.Status == "completed" {
		t.Fatal("session-less run of a write_artifact-only workflow reported completed — " +
			"D-8 requires a loud failure, not silent success")
	}
	if !strings.Contains(res.Err, "no SessionID configured") && !strings.Contains(res.Steps[0].Err, "no SessionID configured") {
		t.Errorf("run.Err = %q, steps[0].Err = %q; want the \"no SessionID configured\" message",
			res.Err, res.Steps[0].Err)
	}
}

// TestWfArtifactsAdapter_ContentRef_StoresNonZeroBytes is spec §5.5c /
// §1.6: a content_ref-only step must store real bytes, not report
// success with an empty artifact.
func TestWfArtifactsAdapter_ContentRef_StoresNonZeroBytes(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	sess, err := api.Sessions().Create(ctx, "wf artifacts content_ref probe")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	const yaml = `
id: zz-wfartifacts-contentref-probe
name: "wf artifacts content_ref probe"
version: 1
steps:
  - name: echo_it
    kind: shell
    cmd: "echo"
    args: ["payload-from-content-ref"]
  - name: save
    kind: write_artifact
    inputs_from: [echo_it]
    title: "probe-ref.txt"
    content_ref: "${step.echo_it.output}"
`
	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := api.Workflows().RunWithOptions(ctx, workflowsview.RunRequest{
		ID:        saved.ID,
		SessionID: sess.ID,
	})
	if err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("run status = %q, want completed (err=%q, steps=%+v)", res.Status, res.Err, res.Steps)
	}

	var payload struct {
		ID string `json:"id"`
	}
	saveStep := res.Steps[len(res.Steps)-1]
	if err := json.Unmarshal([]byte(saveStep.Output), &payload); err != nil {
		t.Fatalf("save step output %q did not decode: %v", saveStep.Output, err)
	}

	got, err := api.Artifacts().Get(ctx, payload.ID)
	if err != nil {
		t.Fatalf("Artifacts().Get: %v", err)
	}
	if len(got.Bytes) == 0 {
		t.Fatal("content_ref-only artifact stored ZERO bytes — the pre-UNIT-5 defect (spec §1.6)")
	}
	if !strings.Contains(string(got.Bytes), "payload-from-content-ref") {
		t.Errorf("artifact bytes = %q, want it to contain the echoed payload", string(got.Bytes))
	}
}
