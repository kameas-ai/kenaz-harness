package rpc

// sync_upload_consent_test.go — owner ruling H1 end to end, through REAL
// settings persistence (CLAUDE.md blind spot #2): the gate reads
// Settings.SettingsSyncUploadsConfirmed from a settings.FileStore on disk,
// Sync_ConfirmUploads writes it there, and a fresh store opened on the same
// directory (a restart) still has it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
)

// consentFakeFleet routes only PUT/GET /api/v1/sync/{category}, as Fleet
// does, and records every request.
type consentFakeFleet struct {
	mu   sync.Mutex
	puts []string
}

func (f *consentFakeFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		var req struct {
			LWWTs   string          `json:"lww_ts"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := time.Parse(time.RFC3339Nano, req.LWWTs); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.puts = append(f.puts, r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"category": "ui_theme", "lww_ts": req.LWWTs, "payload": req.Payload,
			"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
	case http.MethodGet:
		w.WriteHeader(http.StatusNotModified)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *consentFakeFleet) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

// newConsentRig wires a syncview.API exactly as rpc.New does: ui_theme from
// the settings store, and the upload gate over the same store.
func newConsentRig(t *testing.T, baseURL string, store settings.SettingsStore) (*syncview.API, *corefleet.Syncer) {
	t.Helper()
	syncer := corefleet.NewSyncer(corefleet.NewClientForTesting(baseURL))
	t.Cleanup(syncer.Stop)
	syncer.RegisterCategory(corefleet.SyncCategoryUITheme, uiThemeKind(store).CategoryConfig())
	api := syncview.NewAPI(syncer, nil)
	api.SetUploadConsent(settingsUploadConsent{store: store})
	return api, syncer
}

func awaitingConfirmation(t *testing.T, api *syncview.API, cat corefleet.SyncCategory) bool {
	t.Helper()
	rows, err := api.Sync_Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Category == string(cat) {
			return r.AwaitingConfirmation
		}
	}
	t.Fatalf("no status row for %s", cat)
	return false
}

func TestSyncUploadConsent_NoPutUntilConfirmed_PersistsAcrossRestart(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	fake := &consentFakeFleet{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "consent-token" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dir := t.TempDir()
	store, err := settings.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s0, _ := store.LoadAll()
	s0.Theme = "dark"
	if err := store.SaveAll(s0); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A user who opts in on this build: nothing leaves until they confirm.
	api, syncer := newConsentRig(t, srv.URL, store)
	if err := api.Sync_Toggle(ctx, string(corefleet.SyncCategoryUITheme), true); err != nil {
		t.Fatalf("Sync_Toggle: %v", err)
	}
	if err := api.Sync_ForcePush(ctx, string(corefleet.SyncCategoryUITheme)); err == nil {
		t.Error("ForcePush before confirmation should report that uploads are paused")
	}
	syncer.NotifyMutation(corefleet.SyncCategoryUITheme) // debounced; must not upload either
	if n := fake.putCount(); n != 0 {
		t.Fatalf("%d PUTs before confirmation, want 0", n)
	}
	if !awaitingConfirmation(t, api, corefleet.SyncCategoryUITheme) {
		t.Error("ui_theme should be awaiting confirmation")
	}

	// A whole-settings save from a copy loaded before confirming must not
	// clear the flag later; check the reverse here first: confirm, then
	// replay the stale copy through settings.API.Set.
	stale, _ := store.LoadAll()
	if err := api.Sync_ConfirmUploads(ctx); err != nil {
		t.Fatalf("Sync_ConfirmUploads: %v", err)
	}
	if n := fake.putCount(); n != 1 {
		t.Fatalf("PUTs after confirmation = %d, want 1 (the held ui_theme)", n)
	}
	if awaitingConfirmation(t, api, corefleet.SyncCategoryUITheme) {
		t.Error("still awaiting after confirmation")
	}
	if err := settings.NewAPI(store).Set(ctx, stale); err != nil {
		t.Fatalf("Set(stale): %v", err)
	}

	// Restart: a new store on the same directory reads the persisted answer.
	reopened, err := settings.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !got.SettingsSyncUploadsConfirmed {
		t.Fatal("SettingsSyncUploadsConfirmed not persisted (or cleared by a stale Set)")
	}

	// After the restart a new opt-in uploads at once, with no prompt.
	api2, _ := newConsentRig(t, srv.URL, reopened)
	if err := api2.Sync_Toggle(ctx, string(corefleet.SyncCategoryUITheme), true); err != nil {
		t.Fatalf("Sync_Toggle after restart: %v", err)
	}
	if n := fake.putCount(); n != 2 {
		t.Errorf("PUTs after restart opt-in = %d, want 2", n)
	}
	if awaitingConfirmation(t, api2, corefleet.SyncCategoryUITheme) {
		t.Error("a confirmed user must not be asked again")
	}
}

// TestSyncUploadConsent_FailsClosedWithoutStore: no settings store, no
// upload, and confirming reports that the answer could not be saved.
func TestSyncUploadConsent_FailsClosedWithoutStore(t *testing.T) {
	c := settingsUploadConsent{}
	if c.UploadsConfirmed() {
		t.Error("nil store must read as not confirmed")
	}
	if err := c.ConfirmUploads(); err == nil {
		t.Error("ConfirmUploads with no store must fail")
	}
}
