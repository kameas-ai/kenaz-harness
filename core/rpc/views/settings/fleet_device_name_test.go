package settings

// fleet_device_name_test.go — Fleet ER-43: Settings › Account › Device name
// reaches the enroll body (`hostname`). Real settings.json FileStore, real
// fleet.Client, fake Fleet. Fake computer names only.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type deviceNameFleet struct {
	srv *httptest.Server

	mu     sync.Mutex
	bodies []map[string]any
	// gate, when set, makes the next enroll signal entered and wait for
	// gate to close before answering (one-shot).
	gate    chan struct{}
	entered chan struct{}
}

// holdNext makes the next enroll block until the returned release runs.
func (f *deviceNameFleet) holdNext() (entered <-chan struct{}, release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate, f.entered = make(chan struct{}), make(chan struct{})
	gate := f.gate
	return f.entered, func() { close(gate) }
}

func newDeviceNameFleet(t *testing.T) *deviceNameFleet {
	t.Helper()
	f := &deviceNameFleet{}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("POST /api/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		got := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		f.mu.Lock()
		gate, entered := f.gate, f.entered
		f.gate, f.entered = nil, nil
		f.mu.Unlock()
		if gate != nil {
			close(entered)
			<-gate
		}
		f.mu.Lock()
		f.bodies = append(f.bodies, got)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"org_id": "org-uuid", "team_id": "t1", "org_name": "Kameas", "role": "org_member", "tier": "team",
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *deviceNameFleet) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.bodies) }

func (f *deviceNameFleet) last(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		t.Fatal("no enroll")
	}
	return f.bodies[len(f.bodies)-1]
}

// waitEnroll waits for the background re-enroll a setter starts.
func (f *deviceNameFleet) waitEnroll(t *testing.T, after int) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.count() <= after {
		if time.Now().After(deadline) {
			t.Fatalf("no re-enroll after the setting changed (enrolls: %d)", f.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.last(t)
}

func stubComputerName(t *testing.T, name string) {
	t.Helper()
	fn := func() string { return name }
	prev := computerNameFn.Swap(&fn)
	t.Cleanup(func() { computerNameFn.Store(prev) })
}

// TestFleetDeviceName_SettingReachesEnrollBody: default on with the neutral
// name; a change re-enrolls at once; the computer name is used only when
// chosen; a custom name overrides it; off sends nothing.
func TestFleetDeviceName_SettingReachesEnrollBody(t *testing.T) {
	// NOT parallel: SetExternalTokenSource and computerNameFn are global.
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	stubComputerName(t, "Example-Workstation")
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := newDeviceNameFleet(t)
	dataDir := t.TempDir()
	r := &sessionRig{dataDir: dataDir}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	a := NewAPI(store)
	a.SetFleetClient(fleet.NewClientForTestingWithDataDir(f.srv.URL, dataDir), dataDir)
	t.Cleanup(a.StopFleetBackground)

	// Default: shared, neutral "<OS> device <4 hex>", never the computer name.
	if _, err := a.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	nodeID := fleet.ReadNodeID(dataDir)
	def := fleet.DefaultDeviceName(runtime.GOOS, nodeID)
	if def == "" || f.last(t)["hostname"] != def {
		t.Fatalf("default hostname = %v, want %q", f.last(t)["hostname"], def)
	}
	dn, err := a.GetFleetDeviceName(ctx)
	if err != nil || !dn.Share || dn.UseComputerName || dn.CustomName != "" ||
		dn.DefaultName != def || dn.EffectiveName != def ||
		dn.ComputerName != "Example-Workstation" || dn.MaxLength != 64 {
		t.Fatalf("Get default = %+v, %v", dn, err)
	}

	// Use this computer's name: re-enrolls with it.
	n := f.count()
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: true, UseComputerName: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitEnroll(t, n)["hostname"]; got != "Example-Workstation" {
		t.Fatalf("computer-name hostname = %v", got)
	}

	// Custom overrides it; stored sanitised and capped.
	n = f.count()
	custom := " Test\tLaptop " + strings.Repeat("z", 80)
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: true, UseComputerName: true, CustomName: custom}); err != nil {
		t.Fatal(err)
	}
	want := fleet.SanitizeDeviceName(custom)
	if got := f.waitEnroll(t, n)["hostname"]; got != want || len([]rune(want)) != 64 || !strings.HasPrefix(want, "Test Laptop zz") {
		t.Fatalf("custom hostname = %v, want %q", got, want)
	}
	if dn, _ := a.GetFleetDeviceName(ctx); dn.CustomName != want || dn.EffectiveName != want {
		t.Fatalf("Get custom = %+v", dn)
	}

	// Off: nothing is sent (the omitted key clears Fleet's stored name).
	n = f.count()
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: false, CustomName: "Test Laptop"}); err != nil {
		t.Fatal(err)
	}
	if body := f.waitEnroll(t, n); body["hostname"] != nil {
		t.Fatalf("hostname sent while off: %v", body["hostname"])
	}
	if dn, _ := a.GetFleetDeviceName(ctx); dn.Share || dn.EffectiveName != "" {
		t.Fatalf("Get off = %+v", dn)
	}

	// A whole-settings Set from a stale copy cannot turn sharing back on.
	stale := Settings{Theme: "dark"}
	if err := a.Set(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := a.FleetRefreshIdentity(ctx); err != nil {
		t.Fatal(err)
	}
	if h := f.last(t)["hostname"]; h != nil {
		t.Fatalf("stale Set re-enabled sharing: hostname %v", h)
	}
	if s, _ := a.Get(ctx); !s.FleetDeviceNameHidden || s.FleetDeviceNameCustom != "Test Laptop" || s.Theme != "dark" {
		t.Fatalf("after stale Set: hidden=%v custom=%q theme=%q", s.FleetDeviceNameHidden, s.FleetDeviceNameCustom, s.Theme)
	}
}

// TestFleetDeviceName_NotEnrolledDoesNotEnroll: changing the setting before
// sign-in persists it and makes no network call.
func TestFleetDeviceName_NotEnrolledDoesNotEnroll(t *testing.T) {
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	stubComputerName(t, "Example-Workstation")
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := newDeviceNameFleet(t)
	dataDir := t.TempDir()
	a := NewAPI(store)
	a.SetFleetClient(fleet.NewClientForTestingWithDataDir(f.srv.URL, dataDir), dataDir)
	t.Cleanup(a.StopFleetBackground)
	if err := a.SetFleetDeviceName(context.Background(), FleetDeviceNameSettings{Share: true, CustomName: "Test Laptop"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Fatalf("enrolled %d times while not enrolled", f.count())
	}
	// Before any node id exists there is no default name, and Get does not
	// mint a node id.
	dn, err := a.GetFleetDeviceName(context.Background())
	if err != nil || dn.DefaultName != "" || dn.EffectiveName != "Test Laptop" {
		t.Fatalf("Get = %+v, %v", dn, err)
	}
	if fleet.ReadNodeID(dataDir) != "" {
		t.Fatal("GetFleetDeviceName minted a node id")
	}
}

// A store that cannot be read sends no name (the member may have turned
// sharing off).
func TestFleetDeviceName_UnreadableSettingsSendNothing(t *testing.T) {
	a := &API{store: failingLoadStore{}}
	if got := a.fleetDeviceName("node-1"); got != "" {
		t.Fatalf("got %q", got)
	}
}

type failingLoadStore struct{ SettingsStore }

func (failingLoadStore) LoadAll() (Settings, error) { return Settings{}, errors.New("unreadable") }

// TestFleetDeviceName_ChangeDuringInFlightEnroll: RefreshIdentity is
// singleflight, so the re-enroll after a setting change can join an
// enroll already in flight that read the OLD setting. Turning sharing off
// meanwhile must still end with an enroll that carries no name.
func TestFleetDeviceName_ChangeDuringInFlightEnroll(t *testing.T) {
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	stubComputerName(t, "Example-Workstation")
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := newDeviceNameFleet(t)
	dataDir := t.TempDir()
	r := &sessionRig{dataDir: dataDir}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	a := NewAPI(store)
	a.SetFleetClient(fleet.NewClientForTestingWithDataDir(f.srv.URL, dataDir), dataDir)
	t.Cleanup(a.StopFleetBackground)

	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: true, CustomName: "Test Laptop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.FleetRefreshIdentity(ctx); err != nil { // enrolled, sends "Test Laptop"
		t.Fatal(err)
	}

	// An enroll is in flight with the old setting...
	entered, release := f.holdNext()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = a.FleetRefreshIdentity(ctx) }()
	<-entered
	// ...when the member turns sharing off. Its re-enroll joins the
	// in-flight call (which already read "Test Laptop").
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: false}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // let the re-enroll reach the singleflight
	release()
	<-done

	deadline := time.Now().Add(10 * time.Second)
	for {
		f.mu.Lock()
		last := f.bodies[len(f.bodies)-1]
		n := len(f.bodies)
		f.mu.Unlock()
		if _, has := last["hostname"]; !has && n >= 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after turning sharing off mid-enroll, the last enroll (of %d) still sent hostname %v", n, last["hostname"])
		}
		time.Sleep(10 * time.Millisecond)
	}
}
