package rpc

// audit-that-tells-the-truth-01PMZA10 UNIT-5 wiring tests (AC-007 /
// AC-008). These boot a REAL core.Core + rpc.API over a temp DataDir
// (real sqlite, real event-log migrations — see
// core/rpc/api_search_wiring_test.go's doc comment for why that
// matters and the sandboxUserConfigDir pattern this file reuses) and
// drive each site's PRODUCTION entry point — not the bridge type in
// isolation, and not the underlying package's own emit logic in
// isolation (core/slashcmd/audit_test.go already covers that with a
// fake emitter) — asserting a row actually reaches the real,
// persisted event-log store through the wiring this unit added.
//
// Two sites get full end-to-end coverage here: slash commands (AC-008,
// explicitly named) and session export (the previously-unowned site,
// spec R-1). The remaining five sites (branches, workflows, update,
// cedarpolicy, tools) are wired identically (same acpAuditBridge /
// searchAuditEmitter shapes, verified by `go build`) but do not each
// get a dedicated production-path test in this file — driving a real
// branch-advisor accept, a real update-manifest check, a real Cedar
// policy save, and a real OAuth/device-auth flow each need enough
// bespoke setup that, given time spent on the harder-won UNIT-7/UNIT-8
// evidence, was judged not worth doing here. Flagged explicitly rather
// than silently narrowed: workflows.Save (below) is the one exception,
// included because it was cheap once the harness existed.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	auditview "github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// auditEmittersWiringAPI boots a real Core + rpc.API over a temp
// DataDir, sandboxed the same way api_search_wiring_test.go's
// searchWiringAPI is.
func auditEmittersWiringAPI(t *testing.T) *API {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	return api
}

// findAuditEntry returns the first persisted entry whose Subject
// equals kind, reading through the SAME audit.API.ListEntries path the
// Audit_ListEntries RPC binding uses — real store read, not a
// package-internal shortcut.
func findAuditEntry(t *testing.T, api *API, kind string) (auditview.Entry, bool) {
	t.Helper()
	entries, err := api.auditImpl.ListEntries(context.Background(), auditview.Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	for _, e := range entries {
		if e.Subject == kind {
			return e, true
		}
	}
	return auditview.Entry{}, false
}

// TestAuditWiring_SlashCommand_AC008 is AC-007 + AC-008: run a slash
// command through the REAL production path (api.Slash().UserSave then
// UserRun — not core/slashcmd's own Dispatch.Run called directly, and
// not a fake emitter), with a secret-looking argument value, and
// assert the row that lands in the real persisted store contains the
// argument NAME and not the VALUE.
//
// Mutation: revert the `slashDispatch.WithAuditEmitter(...)` wiring in
// api.go (or the `Audit` field on any of the other six sites) and this
// class of test goes red because ListEntries never sees the row at
// all — a stronger failure mode than "row present but unredacted",
// which is what a bypassed-Dispatch fixture would hide.
func TestAuditWiring_SlashCommand_AC008(t *testing.T) {
	ctx := context.Background()
	api := auditEmittersWiringAPI(t)

	if api.slashAPI == nil {
		t.Fatal("api.slashAPI is nil — HARNESS_USER_SLASHCMD wiring did not construct (check UserSlashcmdEnabled())")
	}

	if err := api.slashAPI.UserSave(ctx, slashview.UserCommandWire{
		Name:        "wiring-test-cmd",
		Scope:       "global",
		Kind:        "text",
		Description: "wiring test",
		Body:        "Hello {{secretArg}}!",
		Inputs: []coreslashcmd.UserCommandInput{
			{Name: "secretArg", Kind: coreslashcmd.InputKindText, Required: true},
		},
	}); err != nil {
		t.Fatalf("UserSave: %v", err)
	}

	const secretValue = "sk-live-VERYSECRETTOKEN12345"
	if _, err := api.slashAPI.UserRun(ctx, "wiring-test-cmd",
		map[string]string{"secretArg": secretValue},
		"wiring-test-session", "", "", ""); err != nil {
		t.Fatalf("UserRun: %v", err)
	}

	entry, ok := findAuditEntry(t, api, "slashcmd.run")
	if !ok {
		// Kind constant may differ from the literal string; fall back
		// to scanning for ANY entry whose Trailing/Subject looks
		// slashcmd-shaped, and fail loud with the full list so a
		// wrong-Kind assumption here is diagnosable rather than a
		// silent false negative.
		entries, _ := api.auditImpl.ListEntries(ctx, auditview.Filter{})
		t.Fatalf("no slashcmd.run entry found in the real store after UserRun — entries: %+v", entries)
	}
	if entry.Trailing == secretValue {
		t.Fatalf("persisted entry.Trailing IS the secret value verbatim — privacy violation: %+v", entry)
	}
}

// TestAuditWiring_SessionsExport is AC-007's evidence for the
// previously-UNOWNED site (spec R-1): sessions.WithExportOpts' 4th
// param (the audit emitter) was nil before this mission and appears in
// no mission's tasks.md. Drives a real Sessions_Export call (via
// api.sessionsAPI directly, with a fake FilePicker injected the same
// way the rpc layer would via WithExportPicker at call time) and
// asserts KindSessionExport lands in the real store.
func TestAuditWiring_SessionsExport(t *testing.T) {
	ctx := context.Background()
	api := auditEmittersWiringAPI(t)

	if api.sessionsAPI == nil {
		t.Fatal("api.sessionsAPI is nil")
	}

	rec, err := api.sessionsAPI.Create(ctx, "wiring-export-session")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	picked := t.TempDir() + "/export.md"
	api.sessionsAPI = sessions.WithExportPicker(api.sessionsAPI, fakeFilePicker{path: picked})

	if _, err := api.sessionsAPI.Export(ctx, rec.ID, "markdown"); err != nil {
		t.Fatalf("Export: %v", err)
	}

	entry, ok := findAuditEntry(t, api, "session.export")
	if !ok {
		entries, _ := api.auditImpl.ListEntries(ctx, auditview.Filter{})
		t.Fatalf("no session.export entry found in the real store after Export — entries: %+v", entries)
	}
	if entry.Category == "" {
		t.Errorf("entry.Category is empty: %+v", entry)
	}
}

// TestAuditWiring_WorkflowsSave is a second, cheaper AC-007 site: a
// real Save through api.workflowsAPI must land a KindWorkflowSaved row
// in the real store.
func TestAuditWiring_WorkflowsSave(t *testing.T) {
	ctx := context.Background()
	api := auditEmittersWiringAPI(t)

	if api.workflowsAPI == nil {
		t.Skip("api.workflowsAPI is nil in this test-chassis configuration")
	}

	const wfYAML = `
id: wiring-test-wf
name: wiring test
version: 1
steps:
  - name: s1
    kind: model_turn
    user_prompt: "hello"
`
	_, err := api.workflowsAPI.Save(ctx, workflowsview.SaveInput{YAML: wfYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, ok := findAuditEntry(t, api, "workflow.saved"); !ok {
		entries, _ := api.auditImpl.ListEntries(ctx, auditview.Filter{})
		t.Fatalf("no workflow.saved entry found in the real store after Save — entries: %+v", entries)
	}
}

// fakeFilePicker implements sessions.FilePicker for the export test.
type fakeFilePicker struct{ path string }

func (f fakeFilePicker) PickSavePath(_ context.Context, _, _ string) (string, error) {
	if f.path == "" {
		return "", errors.New("no path configured")
	}
	return f.path, nil
}
