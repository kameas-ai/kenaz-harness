package settings

// fleet_device_name_test.go — Fleet ER-43: Settings › Account › Device name
// reaches the enroll body (`hostname`). Real settings.json FileStore, real
// fleet.Client, fake Fleet. Fake computer names only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// failNext makes the next N enrolls answer 503 (not recorded in bodies;
	// counted in failed).
	failNext int
	failed   int
}

func (f *deviceNameFleet) failNextEnrolls(n int) { f.mu.Lock(); f.failNext = n; f.mu.Unlock() }

func (f *deviceNameFleet) failures() int { f.mu.Lock(); defer f.mu.Unlock(); return f.failed }

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
		if f.failNext > 0 {
			f.failNext--
			f.failed++
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"unavailable","message":"try again"}`))
			return
		}
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

// TestFleetDeviceName_SettingReachesEnrollBody: default on with the
// computer's name; a change re-enrolls at once; an explicit neutral choice
// sends the neutral label; a custom name overrides; off sends nothing.
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

	// Default (never chosen): shared, this computer's name.
	if _, err := a.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	nodeID := fleet.ReadNodeID(dataDir)
	def := fleet.DefaultDeviceName(runtime.GOOS, nodeID)
	if def == "" || f.last(t)["hostname"] != "Example-Workstation" {
		t.Fatalf("default hostname = %v, want the computer name", f.last(t)["hostname"])
	}
	dn, err := a.GetFleetDeviceName(ctx)
	if err != nil || !dn.Share || dn.UseComputerName || dn.UseNeutralName || dn.CustomName != "" ||
		dn.DefaultName != def || dn.EffectiveName != "Example-Workstation" ||
		dn.ComputerName != "Example-Workstation" || dn.MaxLength != 64 {
		t.Fatalf("Get default = %+v, %v", dn, err)
	}

	// The neutral label, chosen: re-enrolls with it, and it is kept.
	n := f.count()
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: true, UseNeutralName: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitEnroll(t, n)["hostname"]; got != def {
		t.Fatalf("neutral hostname = %v, want %q", got, def)
	}
	if dn, _ := a.GetFleetDeviceName(ctx); !dn.UseNeutralName || dn.UseComputerName || dn.EffectiveName != def {
		t.Fatalf("Get neutral = %+v", dn)
	}
	// A whole-settings Set from a stale copy (no neutral flag) must not
	// drop the explicit neutral choice and swap in the computer's name.
	if err := a.Set(ctx, Settings{Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := a.Get(ctx); !s.FleetDeviceNameNeutral || s.Theme != "light" {
		t.Fatalf("stale Set dropped the neutral choice: neutral=%v theme=%q", s.FleetDeviceNameNeutral, s.Theme)
	}
	if got := a.fleetDeviceName(nodeID); got != def {
		t.Fatalf("after stale Set, device name = %q, want neutral %q", got, def)
	}

	// Use this computer's name: re-enrolls with it, clearing neutral.
	n = f.count()
	if err := a.SetFleetDeviceName(ctx, FleetDeviceNameSettings{Share: true, UseComputerName: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitEnroll(t, n)["hostname"]; got != "Example-Workstation" {
		t.Fatalf("computer-name hostname = %v", got)
	}
	if s, _ := a.Get(ctx); s.FleetDeviceNameNeutral || !s.FleetDeviceNameUseComputerName {
		t.Fatalf("explicit computer choice stored as neutral=%v computer=%v", s.FleetDeviceNameNeutral, s.FleetDeviceNameUseComputerName)
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

// TestFleetDeviceName_StoredChoices: what a settings.json written by an
// earlier harness (or by this one) resolves to. Never chosen moves to the
// computer-name default; an explicit neutral, custom or off is kept; an
// empty or generic computer name falls back to the neutral label.
func TestFleetDeviceName_StoredChoices(t *testing.T) {
	const node = "node-1"
	def := fleet.DefaultDeviceName(runtime.GOOS, node)
	cases := []struct {
		name     string
		json     string
		computer string
		want     string
	}{
		{"never chosen (no keys)", `{}`, "Example-Workstation", "Example-Workstation"},
		{"never chosen, other settings", `{"theme":"dark"}`, "Example-Workstation", "Example-Workstation"},
		{"explicit computer (pre-change)", `{"fleetDeviceNameUseComputerName":true}`, "Example-Workstation", "Example-Workstation"},
		{"explicit neutral", `{"fleetDeviceNameNeutral":true}`, "Example-Workstation", def},
		{"custom preserved", `{"fleetDeviceNameCustom":"Test Laptop"}`, "Example-Workstation", "Test Laptop"},
		{"custom over neutral", `{"fleetDeviceNameNeutral":true,"fleetDeviceNameCustom":"Test Laptop"}`, "Example-Workstation", "Test Laptop"},
		{"off preserved", `{"fleetDeviceNameHidden":true}`, "Example-Workstation", ""},
		{"off with custom", `{"fleetDeviceNameHidden":true,"fleetDeviceNameCustom":"Test Laptop"}`, "Example-Workstation", ""},
		{"empty computer name", `{}`, "", def},
		{"generic VM hostname", `{}`, "localhost", def},
		{"workbench image hostname", `{}`, "kameas-workbench", def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubComputerName(t, tc.computer)
			var s Settings
			if err := json.Unmarshal([]byte(tc.json), &s); err != nil {
				t.Fatal(err)
			}
			a := &API{store: fixedLoadStore{s: s}}
			if got := a.fleetDeviceName(node); got != tc.want {
				t.Fatalf("fleetDeviceName = %q, want %q", got, tc.want)
			}
		})
	}
}

type fixedLoadStore struct {
	SettingsStore
	s Settings
}

func (f fixedLoadStore) LoadAll() (Settings, error) { return f.s, nil }

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

// deviceNameRig is an enrolled API over a fake Fleet.
func deviceNameRig(t *testing.T) (*API, *deviceNameFleet) {
	t.Helper()
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
	r := &sessionRig{dataDir: dataDir}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	a := NewAPI(store)
	a.SetFleetClient(fleet.NewClientForTestingWithDataDir(f.srv.URL, dataDir), dataDir)
	t.Cleanup(a.StopFleetBackground)
	t.Cleanup(a.deviceNamePushes.Wait)
	if _, err := a.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	return a, f
}

func shortDeviceNameRetry(t *testing.T) {
	t.Helper()
	base, maxD := deviceNameRetryBase, deviceNameRetryMax
	deviceNameRetryBase, deviceNameRetryMax = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { deviceNameRetryBase, deviceNameRetryMax = base, maxD })
}

// TestFleetDeviceName_UncontendedChangeEnrollsOnce: with nothing in flight,
// a setting change costs exactly ONE extra enroll (the confirm-and-stop
// check after it must hold).
func TestFleetDeviceName_UncontendedChangeEnrollsOnce(t *testing.T) {
	a, f := deviceNameRig(t)
	n := f.count()
	if err := a.SetFleetDeviceName(context.Background(), FleetDeviceNameSettings{Share: true, CustomName: "Test Laptop"}); err != nil {
		t.Fatal(err)
	}
	a.deviceNamePushes.Wait()
	if got := f.count() - n; got != 1 {
		t.Fatalf("uncontended change made %d enrolls, want exactly 1", got)
	}
	if h := f.last(t)["hostname"]; h != "Test Laptop" {
		t.Fatalf("hostname = %v", h)
	}
}

// TestFleetDeviceName_TransientFailureRetried: a failed re-enroll (Fleet
// briefly unavailable) is retried with backoff, so the opt-out still
// reaches Fleet now rather than at the next start.
func TestFleetDeviceName_TransientFailureRetried(t *testing.T) {
	shortDeviceNameRetry(t)
	a, f := deviceNameRig(t)
	n := f.count()
	f.failNextEnrolls(2)
	if err := a.SetFleetDeviceName(context.Background(), FleetDeviceNameSettings{Share: false}); err != nil {
		t.Fatal(err)
	}
	a.deviceNamePushes.Wait()
	if f.failures() < 2 {
		t.Fatalf("failures = %d, want the 2 injected", f.failures())
	}
	if got := f.count() - n; got != 1 {
		t.Fatalf("successful enrolls after retry = %d, want 1", got)
	}
	if body := f.last(t); body["hostname"] != nil {
		t.Fatalf("opt-out not delivered after retry: hostname %v", body["hostname"])
	}
}

// TestFleetDeviceName_PersistentFailureBounded: a Fleet that keeps failing
// gets a bounded number of tries, never a hot loop.
func TestFleetDeviceName_PersistentFailureBounded(t *testing.T) {
	shortDeviceNameRetry(t)
	a, f := deviceNameRig(t)
	n := f.count()
	f.failNextEnrolls(1000)
	if err := a.SetFleetDeviceName(context.Background(), FleetDeviceNameSettings{Share: true, CustomName: "Test Laptop"}); err != nil {
		t.Fatal(err)
	}
	a.deviceNamePushes.Wait()
	if f.count() != n {
		t.Fatalf("unexpected successful enroll")
	}
	// Each RefreshIdentity may itself make more than one HTTP call; the
	// bound is on pushDeviceName's tries (1 + deviceNameMaxRetries).
	if got, maxCalls := f.failures(), (1+deviceNameMaxRetries)*4; got == 0 || got > maxCalls {
		t.Fatalf("failed enrolls = %d, want 1..%d", got, maxCalls)
	}
}

func TestFleetDeviceName_TerminalErrors(t *testing.T) {
	for _, err := range []error{fleet.ErrNotSignedIn, fleet.ErrNodeRemoved, fleet.ErrTokenExpired,
		fmt.Errorf("wrapped: %w", fleet.ErrNodeRemoved), context.DeadlineExceeded} {
		if !deviceNameTerminal(err) {
			t.Errorf("%v: want terminal", err)
		}
	}
	for _, err := range []error{fleet.ErrFleetUnreachable, errors.New("fleet: enroll: status 503")} {
		if deviceNameTerminal(err) {
			t.Errorf("%v: want transient", err)
		}
	}
}
