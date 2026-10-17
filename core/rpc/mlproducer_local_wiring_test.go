package rpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	if a.mlProducer == nil || a.mlProducer.rec.LocalStore() == nil {
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
	if got, want := w.rec.LocalStore().Path(), activityDBPath(dataDir); got != want {
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
		if _, err := w.rec.LocalStore().Commit(ctx, activity.Write{
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
	want := activity.EnvVar + "=" + a.mlProducer.rec.LocalStore().Path()
	found := false
	for _, kv := range sp.ExtraEnv {
		if kv == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("ExtraEnv = %v, want %q", sp.ExtraEnv, want)
	}
	if !filepath.IsAbs(a.mlProducer.rec.LocalStore().Path()) {
		t.Fatalf("path %q is not absolute", a.mlProducer.rec.LocalStore().Path())
	}
	if got := a.mlProducer.rec.LocalStore().Path(); got != filepath.Join(dataDir, "ml-activity", "activity.db") {
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
	if _, err := w.rec.LocalStore().Commit(ctx, activity.Write{
		Source: "harness.agent",
		Events: []activity.Event{{Kind: "phase_change", TS: ts, Payload: map[string]any{"task": "agent-x", "phase": "coding"}}},
		Task:   &activity.Task{ID: "agent-x", RepoRoot: "r", StartedAt: ts, LastActive: ts},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The last good read (3650, taken by SetMLLocalActivityRetention's
	// status readout) applies: not the 90-day default.
	ev, tk, err := w.pruner.PruneOnce(ctx)
	if err != nil || ev != 0 || tk != 0 {
		t.Fatalf("prune with an unreadable setting = %d/%d, %v; want the last good 3650 days (nothing deleted)", ev, tk, err)
	}
	st, _ := w.rec.LocalStore().Stats(ctx)
	if st.Events != 1 || st.Tasks != 1 {
		t.Fatalf("rows after the pass = %+v; the 200-day-old activity was purged", st)
	}
	// Below the guard's threshold the readout reports the failure rather
	// than a made-up retention.
	if _, err := a.MLLocalActivity(ctx); err == nil {
		t.Fatal("status with an unreadable setting returned no error")
	}
	// (d) Three consecutive failed passes: the readout flags it with the
	// last good window the pruner is still applying.
	for i := 0; i < 2; i++ {
		_, _, _ = w.pruner.PruneOnce(ctx)
	}
	v, err := a.MLLocalActivity(ctx)
	if err != nil {
		t.Fatalf("status once blocked: %v; want the guard, not an error", err)
	}
	if !v.RetentionBlocked || v.LastGoodRetentionDays != 3650 || v.RetentionDays != 3650 || v.EffectiveRetentionDays != 3650 || !v.Available || v.Events != 1 {
		t.Fatalf("status once blocked = %+v; want RetentionBlocked with the last good 3650", v)
	}
	// A good read clears it.
	if err := os.WriteFile(store.Path(), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := a.MLLocalActivity(ctx); err != nil || v.RetentionBlocked || v.LastGoodRetentionDays != 0 {
		t.Fatalf("status once readable = %+v, %v; want not blocked", v, err)
	}
}

// (b) + (c) Settings unreadable from boot: no successful read in this
// process, so the prune pass deletes nothing (not the 90-day default),
// and the engine is spawned WITHOUT KENAZ_ML_ACTIVITY_RETENTION_DAYS —
// the engine contract (docs/telemetry/ml-activity-db.sql) defines no
// "don't prune" sentinel, so the variable is stripped (also from the
// inherited environment) rather than invented.
func TestMLProducerWiring_UnreadableFromBootSkipsPruneAndStripsEnv(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(c, WithSettingsStore(store))
	t.Cleanup(a.Shutdown)
	w := a.mlProducer
	ctx := context.Background()
	ts := time.Now().Add(-200 * 24 * time.Hour).UnixMilli()
	if _, err := w.rec.LocalStore().Commit(ctx, activity.Write{
		Source: "harness.agent",
		Events: []activity.Event{{Kind: "phase_change", TS: ts, Payload: map[string]any{"task": "agent-x", "phase": "coding"}}},
		Task:   &activity.Task{ID: "agent-x", RepoRoot: "r", StartedAt: ts, LastActive: ts},
	}); err != nil {
		t.Fatal(err)
	}
	if ev, tk, err := w.pruner.PruneOnce(ctx); err != activity.ErrRetentionUnreadable || ev != 0 || tk != 0 {
		t.Fatalf("first prune, never readable = %d/%d, %v; want skipped", ev, tk, err)
	}
	if st, _ := w.rec.LocalStore().Stats(ctx); st.Events != 1 || st.Tasks != 1 {
		t.Fatalf("rows after the skipped pass = %+v", st)
	}
	// Blocked with no last good value: the readout says so (0 = nothing
	// is being deleted) and the rows stay.
	for i := 0; i < 2; i++ {
		_, _, _ = w.pruner.PruneOnce(ctx)
	}
	if v, err := a.MLLocalActivity(ctx); err != nil || !v.RetentionBlocked || v.LastGoodRetentionDays != 0 {
		t.Fatalf("status blocked without last good = %+v, %v", v, err)
	}
	if st, _ := w.rec.LocalStore().Stats(ctx); st.Events != 1 || st.Tasks != 1 {
		t.Fatalf("rows while blocked without last good = %+v", st)
	}
	sp, ok := a.sidecarMgr.Spawner.(mlsidecar.ProcessSpawner)
	if !ok || sp.EnvFunc == nil {
		t.Fatalf("Spawner = %T with no EnvFunc", a.sidecarMgr.Spawner)
	}
	for _, kv := range sp.Env([]string{"PATH=/bin", activity.RetentionEnvVar + "=7"}) {
		if strings.HasPrefix(kv, activity.RetentionEnvVar+"=") {
			t.Fatalf("never-readable setting: child env carries %q; want the variable stripped", kv)
		}
	}
}

// kenaz-ml agreement (#429): the engine is spawned with
// KENAZ_ML_ACTIVITY_RETENTION_DAYS = the EFFECTIVE local retention, read
// fresh on each spawn; on a read failure the last good value, and with
// none no such entry (never a fallback number that could shrink a longer
// setting).
func TestMLProducerWiring_SidecarGetsEffectiveRetention(t *testing.T) {
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
	sp, ok := a.sidecarMgr.Spawner.(mlsidecar.ProcessSpawner)
	if !ok || sp.EnvFunc == nil {
		t.Fatalf("Spawner = %T with no EnvFunc", a.sidecarMgr.Spawner)
	}
	retention := func() []string {
		var out []string
		for _, kv := range sp.EnvFunc() {
			if strings.HasPrefix(kv, activity.RetentionEnvVar+"=") {
				out = append(out, kv)
			}
		}
		return out
	}
	if got := retention(); len(got) != 1 || got[0] != activity.RetentionEnvVar+"=90" {
		t.Fatalf("unset retention → %v, want =90 (the effective default)", got)
	}
	ctx := context.Background()
	if _, err := a.SetMLLocalActivityRetention(ctx, 3650); err != nil {
		t.Fatal(err)
	}
	if got := retention(); len(got) != 1 || got[0] != activity.RetentionEnvVar+"=3650" {
		t.Fatalf("3650 → %v", got)
	}
	var inheritedOver []string
	for _, kv := range sp.Env([]string{activity.RetentionEnvVar + "=7"}) {
		if strings.HasPrefix(kv, activity.RetentionEnvVar+"=") {
			inheritedOver = append(inheritedOver, kv)
		}
	}
	if len(inheritedOver) != 1 || inheritedOver[0] != activity.RetentionEnvVar+"=3650" {
		t.Fatalf("readable setting over an inherited value → %v, want only =3650", inheritedOver)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// (a)/(c) A spawn-time read failure after a good read carries the
	// LAST GOOD value (3650), so the engine is neither widened nor
	// shortened to its own 90-day default; an inherited value never
	// stands in for it (review nit 2, #429).
	if got := retention(); len(got) != 1 || got[0] != activity.RetentionEnvVar+"=3650" {
		t.Fatalf("unreadable setting after a good read → %v, want the last good =3650", got)
	}
	var inherited []string
	for _, kv := range sp.Env([]string{"PATH=/bin", activity.RetentionEnvVar + "=7"}) {
		if strings.HasPrefix(kv, activity.RetentionEnvVar+"=") {
			inherited = append(inherited, kv)
		}
	}
	if len(inherited) != 1 || inherited[0] != activity.RetentionEnvVar+"=3650" {
		t.Fatalf("unreadable setting: child env = %v; want only the last good =3650", inherited)
	}
	if got := activityRetentionEnv(nil); got != nil {
		t.Fatalf("no reader → %v", got)
	}
	if got := activityRetentionEnv(func() (int, error) { return 99999, nil }); got[0] != activity.RetentionEnvVar+"=3650" {
		t.Fatalf("clamp → %v", got)
	}
}
