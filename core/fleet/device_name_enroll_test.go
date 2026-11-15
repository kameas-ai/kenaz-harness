package fleet_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// enrollBodies is a fake Fleet that records every enroll body.
type enrollBodies struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (e *enrollBodies) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		got := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		e.mu.Lock()
		e.bodies = append(e.bodies, got)
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"org_id": "o1", "role": "org_member"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (e *enrollBodies) last(t *testing.T) map[string]any {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.bodies) == 0 {
		t.Fatal("no enroll")
	}
	return e.bodies[len(e.bodies)-1]
}

// TestEnroll_DeviceNameOnTheWire: the device-name source's answer rides as
// Fleet's `hostname` field, sanitised and capped; "" and no source omit it.
func TestEnroll_DeviceNameOnTheWire(t *testing.T) {
	var rec enrollBodies
	srv := rec.server(t)
	c := newTestClient(t, srv)

	// No source wired: no hostname key (the pre-ER-43 body).
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.last(t)["hostname"]; ok {
		t.Fatalf("hostname sent with no source: %v", rec.last(t))
	}

	var gotNode string
	name := "Test Laptop"
	c.SetDeviceNameSource(func(nodeID string) string { gotNode = nodeID; return name })
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := rec.last(t)["hostname"]; got != "Test Laptop" {
		t.Fatalf("hostname = %v", got)
	}
	if gotNode != "node-1" {
		t.Fatalf("source got node %q", gotNode)
	}

	// Sanitised and capped before it leaves the device.
	name = "\u202eTest\x00Laptop " + strings.Repeat("x", 100)
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	h, _ := rec.last(t)["hostname"].(string)
	if !strings.HasPrefix(h, "Test Laptop xxx") || utf8.RuneCountInString(h) != fleet.MaxDeviceNameLen {
		t.Fatalf("hostname = %q", h)
	}

	// Empty (sharing off): the key is omitted, which clears Fleet's copy.
	name = ""
	if _, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.last(t)["hostname"]; ok {
		t.Fatalf("hostname sent when empty: %v", rec.last(t))
	}
}
