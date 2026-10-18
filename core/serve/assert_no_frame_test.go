package serve_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// dialFrames serves the given frames to one client and returns the client.
func dialFrames(t *testing.T, frames ...wsTestFrame) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		websocket.Handler(func(c *websocket.Conn) {
			for _, f := range frames {
				_ = websocket.JSON.Send(c, f)
			}
			time.Sleep(time.Second)
		}).ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	ws, err := websocket.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), "", srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func TestAssertNoFrame_IgnoresProcessWideBroadcast(t *testing.T) {
	ws := dialFrames(t, wsTestFrame{Event: "fleet:session-changed", Data: []byte(`{"state":"signed_out"}`)})
	if msg := noFrameViolation(ws, 200*time.Millisecond); msg != "" {
		t.Fatalf("broadcast frame must not violate, got %q", msg)
	}
}

func TestAssertNoFrame_FailsOnSessionScopedFrame(t *testing.T) {
	ws := dialFrames(t,
		wsTestFrame{Event: "fleet:session-changed", Data: []byte(`{}`)},
		wsTestFrame{Event: "tool:confirm-pending", Data: []byte(`{"session_id":"sess-A"}`)})
	if msg := noFrameViolation(ws, 200*time.Millisecond); !strings.Contains(msg, "tool:confirm-pending") {
		t.Fatalf("session-scoped frame must violate, got %q", msg)
	}
}
