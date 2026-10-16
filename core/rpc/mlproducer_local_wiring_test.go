package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/activity"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	settingsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// mlproducer_local_wiring_test.go — ml-producer-01MLPRD01 WP11 through
// the wiring New() builds: a real DataDir, a real data.db and a real
// activity.db. No Fleet is signed in, so the ship gate is closed: this is
// the open-source, local-only install.

func newLocalActivityAPI(t *testing.T) (*API, string) {
	t.Helper()
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	a := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(a.Shutdown)
	if a.mlProducer == nil || a.mlProducer.local == nil {
		t.Fatal("New wired no local activity store over a real DataDir")
	}
	return a, dataDir
}

// Signed out: the tool call lands in activity.db and nowhere else; the
// Settings readout counts it; "Delete local activity data" empties it.
func TestMLProducerWiring_LocalActivityRecordedSignedOutAndDeleted(t *testing.T) {
	t.Parallel()
	a, dataDir := newLocalActivityAPI(t)
	ctx := context.Background()
	w := a.mlProducer
	if got, want := w.local.Path(), activityDBPath(dataDir); got != want {
		t.Fatalf("store path = %q, want %q", got, want)
	}
	w.rec.ToolCallCompleted(coreag.ToolCallRecord{
		Ctx: ctx, SessionID: "s1", ToolName: "kenaz__write_file", Outcome: coreag.ToolOutcomeOK,
		Duration: time.Millisecond, RawArgs: `{"path":"a.go","content":"x"}`, ResultContent: `{"ok":true}`,
	})
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.rec.Flush(fctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.store.Pending(ctx); n != 0 {
		t.Fatalf("outbox = %d record(s) while signed out", n)
	}
	v, err := a.MLLocalActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Available || v.Events != 2 || v.Tasks != 1 || v.SizeBytes <= 0 || v.SharingWithFleet || v.EffectiveRetentionDays != 90 {
		t.Fatalf("status = %+v", v)
	}
	v, err = a.DeleteMLLocalActivity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Events != 0 || v.Tasks != 0 {
		t.Fatalf("status after delete = %+v", v)
	}
}

// Dials-to-consumer: MLLocalActivityRetentionDays decides what the wired
// pruner deletes, read on every pass.
func TestMLProducerWiring_LocalActivityRetentionReachesPruner(t *testing.T) {
	t.Parallel()
	a, _ := newLocalActivityAPI(t)
	ctx := context.Background()
	w := a.mlProducer
	now := time.Now()
	for i, age := range []time.Duration{40 * 24 * time.Hour, time.Hour} {
		ts := now.Add(-age).UnixMilli()
		if _, err := w.local.Commit(ctx, activity.Write{
			Source: "harness.agent",
			Events: []activity.Event{{Kind: "phase_change", TS: ts, Payload: map[string]any{"task": "agent-x", "phase": "coding"}}},
			Task:   &activity.Task{ID: "agent-" + string(rune('a'+i)), RepoRoot: "r", StartedAt: ts, LastActive: ts},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if ev, tk, err := w.pruner.PruneOnce(ctx); err != nil || ev != 0 || tk != 0 {
		t.Fatalf("default 90-day prune = %d/%d, %v; want nothing", ev, tk, err)
	}
	v, err := a.SetMLLocalActivityRetention(ctx, 30)
	if err != nil || v.RetentionDays != 30 || v.EffectiveRetentionDays != 30 {
		t.Fatalf("set retention = %+v, %v", v, err)
	}
	if ev, tk, err := w.pruner.PruneOnce(ctx); err != nil || ev != 1 || tk != 1 {
		t.Fatalf("30-day prune = %d/%d, %v; want the 40-day-old event and task", ev, tk, err)
	}
	if _, err := a.SetMLLocalActivityRetention(ctx, -1); err == nil {
		t.Fatal("negative retention accepted")
	}
	if _, err := a.SetMLLocalActivityRetention(ctx, activity.MaxRetentionDays+1); err == nil {
		t.Fatal("retention above the maximum accepted")
	}
}

// The engine is spawned with the store's absolute path, and it is the
// same file the producer writes. A custom data dir keeps it out of the
// isolated engine root (<dataDir>/ml), which boot must not create and an
// engine uninstall removes wholesale.
func TestMLProducerWiring_SidecarGetsActivityDBPath(t *testing.T) {
	t.Parallel()
	a, dataDir := newLocalActivityAPI(t)
	sp, ok := a.sidecarMgr.Spawner.(mlsidecar.ProcessSpawner)
	if !ok {
		t.Fatalf("Spawner = %T", a.sidecarMgr.Spawner)
	}
	want := activity.EnvVar + "=" + a.mlProducer.local.Path()
	found := false
	for _, kv := range sp.ExtraEnv {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("ExtraEnv = %v, want %q", sp.ExtraEnv, want)
	}
	if !filepath.IsAbs(a.mlProducer.local.Path()) {
		t.Fatalf("path %q is not absolute", a.mlProducer.local.Path())
	}
	if got := a.mlProducer.local.Path(); got != filepath.Join(dataDir, "ml-activity", "activity.db") {
		t.Fatalf("custom-data-dir store = %q, want <dataDir>/ml-activity/activity.db", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "ml")); !os.IsNotExist(err) {
		t.Fatalf("boot created the engine install root (stat err=%v)", err)
	}
	// SpawnEnv carries it through to the child environment.
	env := mlsidecar.SpawnEnv([]string{"PATH=/bin"}, a.sidecarMgr.Layout.Root, sp.ExtraEnv...)
	if env[len(env)-1] != want {
		t.Fatalf("spawn env = %v", env)
	}
}

// The standard-profile rule: activity.db is <dataDir>/ml/activity.db
// whenever that directory is not the engine root.
func TestActivityDBPath_StandardShape(t *testing.T) {
	t.Parallel()
	d := t.TempDir()
	if got := activity.Path(d); got != filepath.Join(d, "ml", "activity.db") {
		t.Fatalf("activity.Path = %q", got)
	}
	// d is not the standard profile, so the engine root is d/ml and the
	// store moves aside.
	if got := activityDBPath(d); got != filepath.Join(d, "ml-activity", "activity.db") {
		t.Fatalf("activityDBPath(custom) = %q", got)
	}
}

// Review finding 1 (#429): an unreadable settings file must never be read
// as the 90-day default. A member who chose 3650 days keeps a 200-day-old
// row through a pass that runs while settings.json does not parse; the
// pass deletes nothing. (Before the fix the read error folded into 0 →
// 90 days, and the row was purged.)
func TestMLProducerWiring_UnreadableRetentionSkipsPrune(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store, err := settingsview.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(c, WithSettingsStore(store))
	t.Cleanup(a.Shutdown)
	w := a.mlProducer
	ctx := context.Background()
	if _, err := a.SetMLLocalActivityRetention(ctx, 3650); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-200 * 24 * time.Hour).UnixMilli()
	if _, err := w.local.Commit(ctx, activity.Write{
		Source: "harness.agent",
		Events: []activity.Event{{Kind: "phase_change", TS: ts, Payload: map[string]any{"task": "agent-x", "phase": "coding"}}},
		Task:   &activity.Task{ID: "agent-x", RepoRoot: "r", StartedAt: ts, LastActive: ts},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, tk, err := w.pruner.PruneOnce(ctx)
	if err == nil || ev != 0 || tk != 0 {
		t.Fatalf("prune with an unreadable setting = %d/%d, %v; want the pass skipped with an error", ev, tk, err)
	}
	st, _ := w.local.Stats(ctx)
	if st.Events != 1 || st.Tasks != 1 {
		t.Fatalf("rows after the skipped pass = %+v; the 200-day-old activity was purged", st)
	}
	// The readout reports the failure rather than a made-up retention.
	if _, err := a.MLLocalActivity(ctx); err == nil {
		t.Fatal("status with an unreadable setting returned no error")
	}
}
