package sqlite_test

// sessions/0347-tool-exposure adds projects.tool_exposure,
// sessions.tool_exposure and sessions.tool_activations
// (tool-context-budget-01TCBUD01 WP02).
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.93.2, which predates 0347) and its seeded project and
// sessions, opens under HEAD, and asserts the old rows read back with
// no override, that overrides and activations written through the
// production stores survive a close/reopen, and that the resolver
// folds the persisted layers in §2.1 order.
//
// Falsifiable: drop migration0347 from Migrations() and every read
// fails (no such column: tool_exposure).

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/projects"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

type exposureCatalog []toolexposure.CatalogTool

func (c exposureCatalog) Catalog(context.Context, string) ([]toolexposure.CatalogTool, error) {
	return append([]toolexposure.CatalogTool(nil), c...), nil
}

func openExposureManagers(t *testing.T, dir string) (storage.DB, *session.Manager, *projects.Manager) {
	t.Helper()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db, session.NewManager(session.NewSQLStore(session.NewStorageDB(db))),
		projects.NewManager(projects.NewSQLStore(db))
}

func TestMigration0347_ToolExposure_UpgradesPopulatedV0932(t *testing.T) {
	ctx := context.Background()
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.93.2", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.93.2 fixture: %v", err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.93.2 snapshot: %v", err)
	}
	for _, c := range [][2]string{{"projects", "tool_exposure"}, {"sessions", "tool_exposure"}, {"sessions", "tool_activations"}} {
		if columnExists(t, raw, c[0], c[1]) {
			t.Fatalf("v0.93.2 snapshot already has %s.%s — the fixture no longer predates 0347", c[0], c[1])
		}
	}
	var seededSessions, seededProjects int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&seededSessions); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&seededProjects); err != nil {
		t.Fatal(err)
	}
	if seededSessions < 2 || seededProjects < 1 {
		t.Fatalf("snapshot seeds %d sessions / %d projects; the test needs a project session and a loose one", seededSessions, seededProjects)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, sessMgr, projMgr := openExposureManagers(t, dir)
	if pending, perr := db.Migrations().Pending(); perr != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, perr)
	}

	// Pre-0347 rows: no override, nothing activated, row intact.
	st, err := sessMgr.SessionToolExposure(ctx, "seed-session-1")
	if err != nil {
		t.Fatalf("SessionToolExposure on a pre-0347 row: %v", err)
	}
	if st.ProjectID != "seed-project-1" || !st.Override.IsZero() || st.Activations != nil {
		t.Fatalf("pre-0347 session state = %+v, want project seed-project-1, no override, no activations", st)
	}
	if rec, err := sessMgr.Get(ctx, "seed-session-1"); err != nil || rec.Name != "Seed Session One" {
		t.Fatalf("seed-session-1 = %+v (err %v), want it intact", rec, err)
	}
	if pe, err := projMgr.ProjectToolExposure(ctx, "seed-project-1"); err != nil || !pe.IsZero() {
		t.Fatalf("pre-0347 project override = %+v (err %v), want zero", pe, err)
	}

	projectLayer := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"outlook": {Tier: toolexposure.TierFull},
	}}
	sessionLayer := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
		"outlook": {Tools: map[string]toolexposure.Tier{"send-mail": toolexposure.TierOff}},
	}}
	acts := []toolexposure.Activation{
		{Name: "fetch__fetch", Server: "fetch", LastUsedTurn: 4, Sticky: true},
		{Name: "outlook__list-messages", Server: "outlook", LastUsedTurn: 2},
	}
	if err := projMgr.SetToolExposure(ctx, "seed-project-1", projectLayer); err != nil {
		t.Fatalf("project SetToolExposure: %v", err)
	}
	if err := sessMgr.SetToolExposure(ctx, "seed-session-1", sessionLayer); err != nil {
		t.Fatalf("session SetToolExposure: %v", err)
	}
	if err := sessMgr.SetToolActivations(ctx, "seed-session-1", acts); err != nil {
		t.Fatalf("SetToolActivations: %v", err)
	}
	// Refused writes leave the row as it was.
	bad := toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{"outlook": {Tier: "hidden"}}}
	if err := sessMgr.SetToolExposure(ctx, "seed-session-1", bad); err == nil {
		t.Fatal("unknown tier accepted by the session store")
	}
	if err := projMgr.SetToolExposure(ctx, "seed-project-1", bad); err == nil {
		t.Fatal("unknown tier accepted by the project store")
	}
	if err := sessMgr.SetToolExposure(ctx, "no-such-session", sessionLayer); err == nil {
		t.Fatal("SetToolExposure on a missing session succeeded")
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, sessMgr2, projMgr2 := openExposureManagers(t, dir)
	t.Cleanup(func() { _ = db2.Close(ctx) })
	st, err = sessMgr2.SessionToolExposure(ctx, "seed-session-1")
	if err != nil {
		t.Fatalf("SessionToolExposure after reopen: %v", err)
	}
	if !reflect.DeepEqual(st.Override, sessionLayer) || !reflect.DeepEqual(st.Activations, acts) {
		t.Fatalf("after reopen session state = %+v, want override %+v and activations %+v", st, sessionLayer, acts)
	}
	if pe, err := projMgr2.ProjectToolExposure(ctx, "seed-project-1"); err != nil || !reflect.DeepEqual(pe, projectLayer) {
		t.Fatalf("after reopen project override = %+v (err %v), want %+v", pe, err, projectLayer)
	}
	if st2, err := sessMgr2.SessionToolExposure(ctx, "seed-session-2"); err != nil || !st2.Override.IsZero() || st2.Activations != nil {
		t.Fatalf("untouched seed-session-2 = %+v (err %v), want no override / activations", st2, err)
	}

	// The resolver over the persisted layers plus a real settings file.
	setStore, err := settings.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setAPI := settings.NewAPI(setStore)
	if err := setAPI.SetToolExposure(ctx, toolexposure.Settings{
		Exposure: toolexposure.Exposure{Servers: map[string]toolexposure.ServerExposure{
			"outlook": {Tools: map[string]toolexposure.Tier{"list-messages": toolexposure.TierOff}},
		}},
	}); err != nil {
		t.Fatalf("settings SetToolExposure: %v", err)
	}
	deps := toolexposure.Deps{
		Catalog: exposureCatalog{
			{Name: "outlook__send-mail", Server: "outlook", Running: true},
			{Name: "outlook__list-messages", Server: "outlook", Running: true},
			{Name: "fetch__fetch", Server: "fetch", Running: true},
			{Name: toolexposure.LoadToolsName, Server: toolexposure.BuiltinServer, Running: true},
		},
		Settings: setAPI,
		Sessions: sessMgr2,
		Projects: projMgr2,
	}
	want := map[string]map[string]struct {
		tier toolexposure.Tier
		src  toolexposure.Level
	}{
		// Project session: session tool entry > project server tier >
		// user tool entry.
		"seed-session-1": {
			"outlook__send-mail":       {toolexposure.TierOff, toolexposure.LevelSession},
			"outlook__list-messages":   {toolexposure.TierFull, toolexposure.LevelProject},
			"fetch__fetch":             {toolexposure.TierSummary, toolexposure.LevelDefault},
			toolexposure.LoadToolsName: {toolexposure.TierFull, toolexposure.LevelDefault},
		},
		// Loose session: no project layer, so the user setting decides.
		"seed-session-2": {
			"outlook__send-mail":       {toolexposure.TierSummary, toolexposure.LevelDefault},
			"outlook__list-messages":   {toolexposure.TierOff, toolexposure.LevelUser},
			"fetch__fetch":             {toolexposure.TierSummary, toolexposure.LevelDefault},
			toolexposure.LoadToolsName: {toolexposure.TierFull, toolexposure.LevelDefault},
		},
	}
	for sid, tools := range want {
		rc, err := toolexposure.Resolve(ctx, deps, sid)
		if err != nil {
			t.Fatalf("Resolve %s: %v", sid, err)
		}
		for name, w := range tools {
			got, ok := rc.Tool(name)
			if !ok || got.Tier != w.tier || got.Source != w.src {
				t.Errorf("%s %s = %+v, want %s from %s", sid, name, got, w.tier, w.src)
			}
		}
		if rc.SchemaBudgetTokens != toolexposure.DefaultSchemaBudgetTokens || rc.ActivationTTLTurns != toolexposure.DefaultActivationTTLTurns {
			t.Errorf("%s budget/ttl = %d/%d, want defaults", sid, rc.SchemaBudgetTokens, rc.ActivationTTLTurns)
		}
	}

	// A zero layer / empty activation set clears the columns to NULL.
	if err := sessMgr2.SetToolExposure(ctx, "seed-session-1", toolexposure.Exposure{}); err != nil {
		t.Fatal(err)
	}
	if err := sessMgr2.SetToolActivations(ctx, "seed-session-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := projMgr2.SetToolExposure(ctx, "seed-project-1", toolexposure.Exposure{}); err != nil {
		t.Fatal(err)
	}
	var se, sa, pe sql.NullString
	if err := db2.Reader().QueryRow(ctx,
		"SELECT tool_exposure, tool_activations FROM sessions WHERE id = 'seed-session-1'").Scan(&se, &sa); err != nil {
		t.Fatal(err)
	}
	if err := db2.Reader().QueryRow(ctx,
		"SELECT tool_exposure FROM projects WHERE id = 'seed-project-1'").Scan(&pe); err != nil {
		t.Fatal(err)
	}
	if se.Valid || sa.Valid || pe.Valid {
		t.Fatalf("cleared columns = %v / %v / %v, want NULL", se, sa, pe)
	}
}
