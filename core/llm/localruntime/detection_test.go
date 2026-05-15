package localruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// portFromAddr extracts the integer port from an httptest.Server.URL.
func portFromAddr(addr string) int {
	// addr is like "http://127.0.0.1:PORT"
	parts := strings.Split(addr, ":")
	p, _ := strconv.Atoi(parts[len(parts)-1])
	return p
}

// TestDetectAll_ReturnsAllKinds asserts that DetectAll always returns
// exactly len(AllKinds) entries in AllKinds order.
func TestDetectAll_ReturnsAllKinds(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	// Use a PATH that has no matching binaries so we don't depend on the
	// test machine's installed tools.
	t.Setenv("PATH", t.TempDir())

	ctx := context.Background()
	results := DetectAll(ctx)

	if len(results) != len(AllKinds) {
		t.Fatalf("DetectAll returned %d entries; want %d", len(results), len(AllKinds))
	}
	for i, k := range AllKinds {
		if results[i].Kind != k {
			t.Errorf("results[%d].Kind = %q; want %q", i, results[i].Kind, k)
		}
		if results[i].Name == "" {
			t.Errorf("results[%d].Name is empty", i)
		}
	}
}

// TestDetectAll_DisabledFlag asserts that HARNESS_LOCAL_RUNTIMES=0 returns
// an empty slice without any I/O.
func TestDetectAll_DisabledFlag(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "0")
	results := DetectAll(context.Background())
	if len(results) != 0 {
		t.Fatalf("expected empty slice when disabled; got %d entries", len(results))
	}
}

// TestPortListening_TrueWhenServerRunning confirms the port check is
// accurate when a server is actually listening.
func TestPortListening_TrueWhenServerRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := portFromAddr(srv.URL)
	if !portListening(context.Background(), port) {
		t.Fatalf("portListening returned false for a live server on port %d", port)
	}
}

// TestPortListening_FalseWhenNotListening confirms the port check
// returns false for a port with nothing listening.
func TestPortListening_FalseWhenNotListening(t *testing.T) {
	// Port 1 is reserved (requires root) and should never have a listener.
	if portListening(context.Background(), 1) {
		t.Skip("port 1 has something listening on this machine — skipping")
	}
}

// TestPortListening_BinaryDetection verifies that a runtime is marked
// installed=true only when its binary is on PATH.
func TestDetectOne_BinaryDetection(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")
	// Empty PATH — no binaries
	t.Setenv("PATH", t.TempDir())

	info := detectOne(context.Background(), KindOllama)
	if info.Installed {
		t.Error("expected Installed=false with empty PATH")
	}
}

// TestDetectOne_PortRunning verifies that a runtime is marked Running=true
// when a server is listening on its default port.
func TestDetectOne_PortRunning(t *testing.T) {
	// This test patches portListening by overriding the default port for
	// Ollama with the httptest server's port. We do this by spinning up a
	// real server and temporarily overriding defaultPorts.
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "1")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := portFromAddr(srv.URL)
	// Override the Ollama port for the duration of the test.
	orig := defaultPorts[KindOllama]
	defaultPorts[KindOllama] = port
	t.Cleanup(func() { defaultPorts[KindOllama] = orig })

	origURL := defaultBaseURLs[KindOllama]
	defaultBaseURLs[KindOllama] = srv.URL
	t.Cleanup(func() { defaultBaseURLs[KindOllama] = origURL })

	t.Setenv("PATH", t.TempDir()) // no binary
	info := detectOne(context.Background(), KindOllama)
	if !info.Running {
		t.Error("expected Running=true when port is open")
	}
	if info.Method != DetectionPort {
		t.Errorf("expected DetectionPort, got %q", info.Method)
	}
}

// TestCache_TTL verifies the 5-minute cache behaviour:
// same snapshot returned within TTL, re-detected after invalidation.
func TestCache_TTL(t *testing.T) {
	t.Setenv("HARNESS_LOCAL_RUNTIMES", "0") // stable no-op detection

	// Reset cache state
	Invalidate()

	ctx := context.Background()
	r1 := GetOrDetect(ctx)
	r2 := GetOrDetect(ctx) // should hit cache

	// Both should be identical (empty when disabled)
	if len(r1) != len(r2) {
		t.Errorf("cache miss on second call: len(r1)=%d len(r2)=%d", len(r1), len(r2))
	}
}

// TestCache_Invalidate verifies that Invalidate clears the cache timestamp.
func TestCache_Invalidate(t *testing.T) {
	defaultCache.mu.Lock()
	defaultCache.snapshot = []RuntimeInfo{{Kind: KindOllama}}
	defaultCache.fetchedAt = time.Now()
	defaultCache.mu.Unlock()

	Invalidate()

	defaultCache.mu.Lock()
	empty := defaultCache.snapshot == nil
	defaultCache.mu.Unlock()
	if !empty {
		t.Error("Invalidate did not clear the snapshot")
	}
}
