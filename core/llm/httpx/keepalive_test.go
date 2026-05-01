package httpx

import (
	"testing"
	"time"
)

// TestDefaultTransport_HasKeepalive asserts the dialer underlying
// DefaultTransport has the 30s OS-level TCP keepalive armed. We use
// the package-internal defaultDialer() seam rather than reflecting
// on the transport's DialContext closure — DialContext is a method
// value bound to a specific *net.Dialer and Go gives us no public
// API to recover the receiver from a bound method. Testing the
// constructor directly is the simplest unambiguous coverage.
func TestDefaultTransport_HasKeepalive(t *testing.T) {
	d := defaultDialer()
	if got, want := d.KeepAlive, 30*time.Second; got != want {
		t.Fatalf("defaultDialer().KeepAlive = %v, want %v", got, want)
	}
	if got, want := d.Timeout, 30*time.Second; got != want {
		t.Fatalf("defaultDialer().Timeout = %v, want %v", got, want)
	}
}

// TestDefaultTransport_Shape pins the Transport-level fields the
// plan calls out. Future tuning should update both this test and
// the package doc-comment so the rationale stays in sync.
func TestDefaultTransport_Shape(t *testing.T) {
	tr := DefaultTransport()
	if tr == nil {
		t.Fatal("DefaultTransport() returned nil")
	}
	if tr.DialContext == nil {
		t.Fatal("DefaultTransport().DialContext is nil; keepalive dialer not wired")
	}
	if got, want := tr.TLSHandshakeTimeout, 10*time.Second; got != want {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", got, want)
	}
	if got, want := tr.ExpectContinueTimeout, 1*time.Second; got != want {
		t.Errorf("ExpectContinueTimeout = %v, want %v", got, want)
	}
	if got, want := tr.ResponseHeaderTimeout, 60*time.Second; got != want {
		t.Errorf("ResponseHeaderTimeout = %v, want %v", got, want)
	}
	if got, want := tr.IdleConnTimeout, 90*time.Second; got != want {
		t.Errorf("IdleConnTimeout = %v, want %v", got, want)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 = false, want true")
	}
}

// TestDefaultTransport_FreshInstances guards against accidental
// mutation between callers — each call should yield an independent
// *http.Transport so adapter A can't poison adapter B's connection
// pool.
func TestDefaultTransport_FreshInstances(t *testing.T) {
	a := DefaultTransport()
	b := DefaultTransport()
	if a == b {
		t.Fatal("DefaultTransport() returned the same pointer twice; want fresh instances")
	}
}
