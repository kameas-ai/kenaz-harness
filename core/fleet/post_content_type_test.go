package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Post sends its contentType (ml-producer-01MLPRD01 WP03). Fleet's OTLP
// receiver picks protobuf vs JSON from Content-Type alone; before this the
// parameter was dropped and every Post went out with none.
func TestClientPost_SendsContentType(t *testing.T) {
	var mu sync.Mutex
	var got []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config.json" {
			_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": srv.URL})
			return
		}
		mu.Lock()
		got = append(got, r.Header.Get("Content-Type"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	SetExternalTokenSource(func() string { return "tok" })
	defer SetExternalTokenSource(nil)

	c := NewClientForTesting(srv.URL)
	for _, ct := range []string{"application/x-protobuf", "application/json", ""} {
		resp, err := c.Post(context.Background(), "/otlp/v1/logs", ct, bytes.NewReader([]byte("x")))
		if err != nil {
			t.Fatalf("Post(%q): %v", ct, err)
		}
		_ = resp.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"application/x-protobuf", "application/json", ""}
	if len(got) != len(want) {
		t.Fatalf("requests = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d Content-Type = %q, want %q", i, got[i], want[i])
		}
	}
}
