package mlsidecar

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Lane-mode tests (owner ruling A5.2/A5.3, kenaz parity (a)-(f)). They
// run REAL loopback listeners on a lane whose base is chosen at random
// with every candidate verified free, so the scheme (base + k*LaneStride)
// is exercised exactly as production runs it, without touching any
// production port. No real engine, no Python: "our engine" is an
// httptest server answering the installed layout's verified identity.

// freeLaneBase returns a base port whose whole lane is currently free on
// 127.0.0.1.
func freeLaneBase(t *testing.T) int {
	t.Helper()
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for attempt := 0; attempt < 200; attempt++ {
		base := 20000 + r.Intn(40000)
		ok := true
		for _, p := range CandidatePorts(base) {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if err != nil {
				ok = false
				break
			}
			_ = ln.Close()
		}
		if ok {
			return base
		}
	}
	t.Fatal("no free lane found")
	return 0
}

// serveAt starts h on 127.0.0.1:port, closed at test cleanup.
func serveAt(t *testing.T, port int, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen on lane port %d: %v", port, err)
	}
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// sigildLike answers /health exactly like sigild's plugin-ingest
// listener: {"status":"ok"} — no engine identity at all.
func sigildLike() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// engineAnswering answers /health with h.
func engineAnswering(h HealthPayload) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h)
	})
}

// laneSpawner is a race-safe fake Spawner that starts "the engine" (an
// httptest server answering health) on exactly the port it is told.
type laneSpawner struct {
	t      *testing.T
	health HealthPayload
	forbid bool // fail the test if Spawn is ever called

	mu    sync.Mutex
	ports []int
}

func (s *laneSpawner) Spawn(_ context.Context, _ string, port int) (int, error) {
	s.mu.Lock()
	s.ports = append(s.ports, port)
	s.mu.Unlock()
	if s.forbid {
		s.t.Errorf("Spawn(port %d) called; this client must adopt, not spawn", port)
		return 0, fmt.Errorf("spawn forbidden")
	}
	serveAt(s.t, port, engineAnswering(s.health))
	return 4242, nil
}

func (s *laneSpawner) spawned() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.ports...)
}

func newLaneManager(l Layout, base int, sp Spawner, clientID string) *Manager {
	m := NewManager(l, NewClient(LoopbackURL(base), nil), sp, clientID, "0.89.1")
	m.BasePort = base
	return m
}

func readPortFile(t *testing.T, l Layout) string {
	t.Helper()
	b, err := os.ReadFile(l.EnginePortFile())
	if err != nil {
		return ""
	}
	return string(b)
}

// TestLanes_ForeignOnBase_SpawnsOnNextCandidate_SecondClientAdoptsFromFile
// is the sigild case end to end: a {"status":"ok"} listener holds the base
// port -> Ensure skips it, spawns on base+LaneStride, records exactly
// "<port>\n" in engine.port; a second client on the same root then adopts
// from the file without spawning.
func TestLanes_ForeignOnBase_SpawnsOnNextCandidate_SecondClientAdoptsFromFile(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	serveAt(t, lanes[0], sigildLike())

	sp := &laneSpawner{t: t, health: health}
	m := newLaneManager(l, base, sp, "harness")
	got := m.Ensure(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q/%q (%s), want healthy on the fallback lane", got.State, got.Reason, got.Detail)
	}
	if ports := sp.spawned(); len(ports) != 1 || ports[0] != lanes[1] {
		t.Fatalf("spawned on %v, want exactly [%d] (base+LaneStride)", ports, lanes[1])
	}
	if f := readPortFile(t, l); f != strconv.Itoa(lanes[1])+"\n" {
		t.Fatalf("engine.port = %q, want %q", f, strconv.Itoa(lanes[1])+"\n")
	}
	if _, err := os.Stat(l.LeaseFile("harness")); err != nil {
		t.Fatalf("healthy spawn must renew this client's lease: %v", err)
	}

	second := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "kenaz")
	if st := second.Ensure(context.Background()); st.State != StateHealthy {
		t.Fatalf("second client State = %q (%s), want healthy by adopting from engine.port", st.State, st.Detail)
	}
}

// TestLanes_AllCandidatesForeign_PortConflictNamesScannedList: every
// candidate holds a foreign listener -> installed_unhealthy/port_conflict
// whose Detail names every scanned port; nothing spawned; engine.port is
// never written.
func TestLanes_AllCandidatesForeign_PortConflictNamesScannedList(t *testing.T) {
	l, _ := installedLayout(t)
	base := freeLaneBase(t)
	for _, p := range CandidatePorts(base) {
		serveAt(t, p, sigildLike())
	}
	sp := &laneSpawner{t: t, forbid: true}
	got := newLaneManager(l, base, sp, "harness").Ensure(context.Background())
	if got.State != StateInstalledUnhealthy || got.Reason != ReasonPortConflict {
		t.Fatalf("got %q/%q (%s), want installed_unhealthy/port_conflict", got.State, got.Reason, got.Detail)
	}
	for _, p := range CandidatePorts(base) {
		if !strings.Contains(got.Detail, strconv.Itoa(p)+" "+string(StateLegacyUnverified)) {
			t.Errorf("Detail %q does not name scanned candidate %d with its verdict", got.Detail, p)
		}
	}
	if _, err := os.Stat(l.EnginePortFile()); !os.IsNotExist(err) {
		t.Fatalf("engine.port must never be written when every candidate is foreign (stat err=%v)", err)
	}
}

// TestLanes_StaleEnginePortAtForeign_IgnoredRescannedRewritten: the file
// points at a port now held by a foreign listener -> it is ignored, the
// lane is rescanned, the engine is spawned on the first free candidate
// and the file is rewritten to it.
func TestLanes_StaleEnginePortAtForeign_IgnoredRescannedRewritten(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	serveAt(t, lanes[2], sigildLike())
	if err := WriteEnginePort(l, lanes[2]); err != nil {
		t.Fatal(err)
	}
	sp := &laneSpawner{t: t, health: health}
	got := newLaneManager(l, base, sp, "harness").Ensure(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if ports := sp.spawned(); len(ports) != 1 || ports[0] != lanes[0] {
		t.Fatalf("spawned on %v, want [%d] (first free candidate)", ports, lanes[0])
	}
	if f := readPortFile(t, l); f != strconv.Itoa(lanes[0])+"\n" {
		t.Fatalf("engine.port = %q, want rewritten to %d", f, lanes[0])
	}
}

// TestLanes_StaleEnginePortAtDeadPort_RescanFindsOursElsewhere is kenaz
// parity (e): the recorded port went dead because the other client
// respawned the engine on a different lane port -> the rescan finds and
// adopts it (file corrected) instead of spawning a second engine, even
// though the base is free.
func TestLanes_StaleEnginePortAtDeadPort_RescanFindsOursElsewhere(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	if err := WriteEnginePort(l, lanes[3]); err != nil { // nothing listens there
		t.Fatal(err)
	}
	serveAt(t, lanes[1], engineAnswering(health))
	got := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness").Ensure(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if f := readPortFile(t, l); f != strconv.Itoa(lanes[1])+"\n" {
		t.Fatalf("engine.port = %q, want corrected to %d", f, lanes[1])
	}
}

// TestLanes_OutOfLaneEnginePort_IgnoredNeverDialed is kenaz parity (a): a
// record outside the env's lane is ignored — even with our own engine
// answering there — and the lane scan decides.
func TestLanes_OutOfLaneEnginePort_IgnoredNeverDialed(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	outside := httptest.NewServer(engineAnswering(health))
	defer outside.Close()
	outPort := outside.Listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(l.EnginePortFile(), []byte(strconv.Itoa(outPort)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := &laneSpawner{t: t, health: health}
	got := newLaneManager(l, base, sp, "harness").Ensure(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if ports := sp.spawned(); len(ports) != 1 || ports[0] != base {
		t.Fatalf("spawned on %v, want [%d]: the out-of-lane record must not be adopted", ports, base)
	}
	if f := readPortFile(t, l); f != strconv.Itoa(base)+"\n" {
		t.Fatalf("engine.port = %q, want rewritten to the base %d", f, base)
	}
}

// TestLanes_RefuseUnverifiedStopsScan is kenaz parity (b): a candidate
// serving this root's install with bytes that fail verification is a
// safety stop — the scan never steps around it to a free candidate.
func TestLanes_RefuseUnverifiedStopsScan(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	health.EngineSHA256 = strings.Repeat("ab", 32) // lies about its digest
	serveAt(t, base, engineAnswering(health))
	got := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness").Ensure(context.Background())
	if got.State != StateUnverified || got.Reason != ReasonDigestMismatch {
		t.Fatalf("got %q/%q (%s), want unverified/digest_mismatch", got.State, got.Reason, got.Detail)
	}
	if _, err := os.Stat(l.EnginePortFile()); !os.IsNotExist(err) {
		t.Fatalf("engine.port written for a refused engine (stat err=%v)", err)
	}
}

// TestLanes_UpdatePendingIsAdopted is kenaz parity (c): an engine from
// this root's versions/ that is not `current` (the old version still
// serving after an update flip) is adopted — engine.port recorded, no
// second engine spawned — but never reported healthy and never leased.
func TestLanes_UpdatePendingIsAdopted(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	health.ExePath = filepath.Join(l.VersionDir("1.1.0"), "kameas-ml", EngineExecutableName(""))
	serveAt(t, base, engineAnswering(health))
	got := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness").Ensure(context.Background())
	if got.State != StateInstalledUnhealthy || got.Reason != ReasonUpdatePending {
		t.Fatalf("got %q/%q (%s), want installed_unhealthy/update_pending", got.State, got.Reason, got.Detail)
	}
	if f := readPortFile(t, l); f != strconv.Itoa(base)+"\n" {
		t.Fatalf("engine.port = %q, want %d", f, base)
	}
	if _, err := os.Stat(l.LeaseFile("harness")); !os.IsNotExist(err) {
		t.Fatalf("an update-pending engine must not be leased (stat err=%v)", err)
	}
}

// TestLanes_ObserveForeignBaseWithFreeLane_IsIdleNotLegacy: the Settings
// read sees sigild on the base and nothing of ours — that is "not
// running", not "legacy engine"; Observe never spawns or writes.
func TestLanes_ObserveForeignBaseWithFreeLane_IsIdleNotLegacy(t *testing.T) {
	l, _ := installedLayout(t)
	base := freeLaneBase(t)
	serveAt(t, base, sigildLike())
	m := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness")
	st, running := m.Observe(context.Background())
	if running || st.State == StateLegacyUnverified {
		t.Fatalf("Observe = %+v running=%v, want idle (not running, not legacy)", st, running)
	}
	if _, err := os.Stat(l.EnginePortFile()); !os.IsNotExist(err) {
		t.Fatalf("Observe wrote engine.port (stat err=%v)", err)
	}
}

// TestDialClient_FollowsVerifiedPortNotFile is review F2: engine.port is
// discovery, not routing. Healthy on lane A; a same-user writer rewrites
// the file to a foreign listener on B -> the next advice-path request
// still dials A; the next Ensure corrects the file back to A. Before any
// verification the dial client fails closed (never reaches the base).
func TestDialClient_FollowsVerifiedPortNotFile(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	serveAt(t, lanes[0], sigildLike())
	serveAt(t, lanes[3], sigildLike())
	sp := &laneSpawner{t: t, health: health}
	m := newLaneManager(l, base, sp, "harness")
	dial := m.DialClient()
	if _, err := dial.Health(context.Background()); err == nil {
		t.Fatal("unverified DialClient reached a listener; it must fail closed")
	}
	if got := m.Ensure(context.Background()); got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	a := lanes[1]
	if m.VerifiedPort() != a {
		t.Fatalf("VerifiedPort = %d, want %d", m.VerifiedPort(), a)
	}
	if err := WriteEnginePort(l, lanes[3]); err != nil { // the redirect attempt
		t.Fatal(err)
	}
	if dial.URL() != LoopbackURL(a) {
		t.Fatalf("DialClient URL = %s after the file was rewritten, want the verified %s", dial.URL(), LoopbackURL(a))
	}
	if got, err := dial.Health(context.Background()); err != nil || got.ExePath != health.ExePath {
		t.Fatalf("Health = %+v, %v; want our engine on the verified port", got, err)
	}
	if got := m.Ensure(context.Background()); got.State != StateHealthy {
		t.Fatalf("second Ensure State = %q (%s)", got.State, got.Detail)
	}
	if f := readPortFile(t, l); f != strconv.Itoa(a)+"\n" {
		t.Fatalf("engine.port = %q, want corrected to %d", f, a)
	}
	if n := len(sp.spawned()); n != 1 {
		t.Fatalf("spawned %d times, want 1", n)
	}
	// Leaving healthy clears the pin.
	m.setStatus(Status{State: StateInstalledUnhealthy})
	if m.VerifiedPort() != 0 || dial.URL() != unpinnedURL {
		t.Fatalf("pin survived a non-healthy status: port %d url %s", m.VerifiedPort(), dial.URL())
	}
}

// TestLanes_UpdatePendingNeverPinned is ruling F4: an update-pending
// adoption records engine.port (cross-repo contract) but is never the
// verified port — no advice or label request can reach it.
func TestLanes_UpdatePendingNeverPinned(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	health.ExePath = filepath.Join(l.VersionDir("1.1.0"), "kameas-ml", EngineExecutableName(""))
	serveAt(t, base, engineAnswering(health))
	m := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness")
	if got := m.Ensure(context.Background()); got.Reason != ReasonUpdatePending {
		t.Fatalf("got %q/%q", got.State, got.Reason)
	}
	if m.VerifiedPort() != 0 || m.DialClient().URL() != unpinnedURL {
		t.Fatalf("update-pending engine pinned: port %d", m.VerifiedPort())
	}
	if m.shutdownClient(context.Background()) != nil {
		t.Fatal("shutdown token would be sent to an update-pending (lexically claimed) engine")
	}
}

// TestEnginePortFile_Contract pins engine.port's cross-repo format (kenaz
// parity (f)): ASCII decimal + exactly one "\n"; absent is ok=false with
// no error; garbage (port 0, no newline, extra bytes, non-digits, out of
// range) is an error and never a port.
func TestEnginePortFile_Contract(t *testing.T) {
	l := NewLayout(t.TempDir())
	if p, ok, err := ReadEnginePort(l); ok || err != nil || p != 0 {
		t.Fatalf("absent: (%d, %v, %v), want (0, false, nil)", p, ok, err)
	}
	if err := WriteEnginePort(l, 7795); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.EnginePortFile()); string(b) != "7795\n" {
		t.Fatalf("written bytes = %q, want \"7795\\n\"", b)
	}
	if p, ok, err := ReadEnginePort(l); !ok || err != nil || p != 7795 {
		t.Fatalf("valid: (%d, %v, %v)", p, ok, err)
	}
	entries, _ := os.ReadDir(l.Root)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	for _, bad := range []string{"0\n", "7795", "7795\n\n", " 7795\n", "77a5\n", "65536\n", "-1\n", "\n"} {
		if err := os.WriteFile(l.EnginePortFile(), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if p, ok, err := ReadEnginePort(l); ok || err == nil {
			t.Errorf("garbage %q: (%d, %v, %v), want an error and no port", bad, p, ok, err)
		}
	}
	if err := WriteEnginePort(l, 0); err == nil {
		t.Error("WriteEnginePort(0) must refuse")
	}
	// In-lane narrowing: a valid port outside the lane is an error.
	base := EnginePort(EngineEnvDev)
	if err := WriteEnginePort(l, base-10); err != nil { // 7775: sigild's port, never a dev lane port
		t.Fatal(err)
	}
	if _, ok, err := RecordedEnginePort(l, base); ok || err == nil {
		t.Errorf("out-of-lane record accepted (ok=%v err=%v)", ok, err)
	}
	for _, p := range CandidatePorts(base) {
		if p == base-10 {
			t.Fatalf("the dev lane contains %d", p)
		}
	}
}

// slowHealth accepts the connection but never completes /health within
// any sane client timeout (until the client gives up).
func slowHealth(d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(d):
		}
	})
}

// TestLanes_SlowEngineOnRecordedPort_NoSpawn is review F1: a listener on
// the RECORDED port that accepts the connection but answers /health only
// after the client timeout is occupied/unknown — never "free". Ensure
// must not spawn a second engine (the base is free) and reports
// installed_unhealthy "not responding".
func TestLanes_SlowEngineOnRecordedPort_NoSpawn(t *testing.T) {
	l, _ := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	serveAt(t, lanes[1], slowHealth(5*time.Second))
	if err := WriteEnginePort(l, lanes[1]); err != nil {
		t.Fatal(err)
	}
	m := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness")
	m.Client.HTTP = &http.Client{Timeout: 150 * time.Millisecond}
	got := m.Ensure(context.Background())
	if got.State != StateInstalledUnhealthy || !strings.Contains(got.Detail, "not responding") {
		t.Fatalf("got %q/%q (%s), want installed_unhealthy \"not responding\"", got.State, got.Reason, got.Detail)
	}
	if f := readPortFile(t, l); f != strconv.Itoa(lanes[1])+"\n" {
		t.Fatalf("engine.port = %q, want left at the recorded %d", f, lanes[1])
	}
}

// TestLanes_SlowListenerUnrecorded_NeverASpawnTarget: a connected-but-
// silent listener on the base with no record is stepped past, never
// spawned onto; the engine goes to the next truly free candidate.
func TestLanes_SlowListenerUnrecorded_NeverASpawnTarget(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	serveAt(t, lanes[0], slowHealth(5*time.Second))
	sp := &laneSpawner{t: t, health: health}
	m := newLaneManager(l, base, sp, "harness")
	m.Client.HTTP = &http.Client{Timeout: 150 * time.Millisecond}
	if got := m.Ensure(context.Background()); got.State != StateHealthy {
		t.Fatalf("State = %q (%s)", got.State, got.Detail)
	}
	if ports := sp.spawned(); len(ports) != 1 || ports[0] != lanes[1] {
		t.Fatalf("spawned on %v, want [%d] — never on the busy base", ports, lanes[1])
	}
}

// onProbeRT is a RoundTripper that runs hook once, just before forwarding
// the first request to triggerPort.
type onProbeRT struct {
	triggerPort int
	hook        func()
	once        sync.Once
}

func (rt *onProbeRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Port() == strconv.Itoa(rt.triggerPort) {
		rt.once.Do(rt.hook)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// TestLanes_PostLockRecheck_AdoptsConcurrentSpawn is review F3(1): the
// other client spawns our engine on another lane port and records it
// AFTER this client's scan saw that port free but BEFORE this client
// spawns. The post-lock engine.port recheck in spawnLocked must adopt it
// with zero spawns.
func TestLanes_PostLockRecheck_AdoptsConcurrentSpawn(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	lanes := CandidatePorts(base)
	sp := &laneSpawner{t: t, forbid: true}
	m := newLaneManager(l, base, sp, "harness")
	m.Client.HTTP = &http.Client{Timeout: 5 * time.Second, Transport: &onProbeRT{
		triggerPort: lanes[LaneCount-1], // the scan's last probe
		hook: func() {
			serveAt(t, lanes[2], engineAnswering(health)) // already probed: free
			if err := WriteEnginePort(l, lanes[2]); err != nil {
				t.Error(err)
			}
		},
	}}
	got := m.Ensure(context.Background())
	if got.State != StateHealthy {
		t.Fatalf("State = %q (%s), want healthy by adopting the concurrent spawn", got.State, got.Detail)
	}
	if n := len(sp.spawned()); n != 0 {
		t.Fatalf("spawned %d times, want 0", n)
	}
	if m.VerifiedPort() != lanes[2] {
		t.Fatalf("VerifiedPort = %d, want %d", m.VerifiedPort(), lanes[2])
	}
}

// TestLanes_ContractUnsupportedIsTerminal is review F3(2): a lease-aware
// engine speaking a lifecycle protocol this build cannot is a stop, not a
// foreign listener — the scan never steps around it into a second spawn.
func TestLanes_ContractUnsupportedIsTerminal(t *testing.T) {
	l, health := installedLayout(t)
	base := freeLaneBase(t)
	health.LifecycleProtocol = SupportedContractMajor + 7
	serveAt(t, base, engineAnswering(health))
	got := newLaneManager(l, base, &laneSpawner{t: t, forbid: true}, "harness").Ensure(context.Background())
	if got.State != StateContractUnsupported {
		t.Fatalf("got %q/%q (%s), want contract_unsupported", got.State, got.Reason, got.Detail)
	}
	if _, err := os.Stat(l.EnginePortFile()); !os.IsNotExist(err) {
		t.Fatalf("engine.port written for a contract-unsupported engine (stat err=%v)", err)
	}
}
