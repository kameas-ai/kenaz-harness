package fleet_test

// useragent_test.go — legal (kameas-ai-11, 2026-10-16, via kenaz-fleet
// #289): every Fleet request carries User-Agent: kenaz-harness/<semver>.
// A fake Fleet records the UA of a regular GET, the ML notice-ack POST, a
// token refresh and an OTLP export; each must start with "kenaz-harness/".

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type uaRecorder struct {
	mu  sync.Mutex
	uas map[string]string // "METHOD path" -> User-Agent
}

func (u *uaRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	u.mu.Lock()
	u.uas[r.Method+" "+r.URL.Path] = r.Header.Get("User-Agent")
	u.mu.Unlock()
	switch r.URL.Path {
	case "/oauth/v2/token":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3600,"token_type":"Bearer"}`)
	case "/v1/traces", "/otlp/v1/traces":
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	default:
		// /me/ml and notice-ack: a status the client reads as an error is
		// fine; only the request's UA matters here.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"not_found"}`)
	}
}

func (u *uaRecorder) get(key string) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	v, ok := u.uas[key]
	return v, ok
}

func TestUserAgent_OnEveryFleetRequestKind(t *testing.T) {
	fleet.SetClientVersion("v1.2.3")
	t.Cleanup(func() { fleet.SetClientVersion("") })
	rec := &uaRecorder{uas: map[string]string{}}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)
	_, _ = c.GetMeML(t.Context())
	_, _ = c.AckMLNotice(t.Context(), fleet.MLNoticeAckActionRead, 1, 0, nil, 0)
	if _, err := fleet.RefreshTokenSet(t.Context(), fleet.EnvProfile{ZitadelIssuer: srv.URL, NativeClientID: "cid"}, "rt"); err != nil {
		t.Fatalf("RefreshTokenSet: %v", err)
	}
	rt := fleet.NewOTLPAckRoundTripper(fleet.NewTokenRoundTripper(func() (string, error) { return "tok", nil }, nil))
	exp := newTraceExporter(t, srv.URL, rt)
	if err := exp.ExportSpans(context.Background(), recordedSpans(t)); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	_ = exp.Shutdown(context.Background())

	for _, key := range []string{
		"GET /api/v1/me/ml",
		"POST /api/v1/me/ml/notice-ack",
		"POST /oauth/v2/token",
		"POST /v1/traces",
	} {
		ua, ok := rec.get(key)
		if !ok {
			t.Errorf("%s: no request recorded", key)
			continue
		}
		if !strings.HasPrefix(ua, "kenaz-harness/") || ua != "kenaz-harness/1.2.3" {
			t.Errorf("%s: User-Agent = %q, want kenaz-harness/1.2.3", key, ua)
		}
	}
}

func TestUserAgent_VersionFormat(t *testing.T) {
	t.Cleanup(func() { fleet.SetClientVersion("") })
	for in, want := range map[string]string{
		"v0.98.1":    "kenaz-harness/0.98.1",
		"0.98.1":     "kenaz-harness/0.98.1",
		"v1.2.0-rc1": "kenaz-harness/1.2.0-rc1",
		"dev":        "kenaz-harness/dev",
		"":           "kenaz-harness/dev",
		"  v2.0.0  ": "kenaz-harness/2.0.0",
	} {
		fleet.SetClientVersion(in)
		if got := fleet.UserAgent(); got != want {
			t.Errorf("SetClientVersion(%q): UserAgent = %q, want %q", in, got, want)
		}
	}
}
