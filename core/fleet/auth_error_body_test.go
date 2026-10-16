package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A token planted in a token-endpoint ERROR body must reach neither the log
// nor the returned error, on the code exchange or on refresh (the path that
// runs most often; its error becomes the session-expired event's Reason).
func plantedTokenServer(t *testing.T, status int, stray string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":"invalid_grant","error_description":"token %s expired","refresh_token":"%s"}`, stray, stray)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func logText(c *logCapture) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, r := range c.recs {
		b.WriteString(r.Message)
		r.Attrs(func(a slog.Attr) bool { b.WriteString(" " + a.Key + "=" + a.Value.String()); return true })
	}
	return b.String()
}

func TestTokenEndpointErrors_NeverCarryTheBody(t *testing.T) {
	stray := "eyJ" + strings.Repeat("q", 30) + ".eyJ" + strings.Repeat("r", 30) + ".sig"
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusBadGateway} {
		srv := plantedTokenServer(t, status, stray)
		profile := EnvProfile{ZitadelIssuer: srv.URL, NativeClientID: "test-client"}

		logs := captureLogs(t)
		_, err := exchangeCode(context.Background(), profile, "code", "http://127.0.0.1/cb", "verifier")
		if err == nil || strings.Contains(err.Error(), stray) {
			t.Fatalf("exchange status %d: err = %v (must exist and not carry the token)", status, err)
		}
		if strings.Contains(logText(logs), stray) {
			t.Fatalf("exchange status %d: the token reached the log", status)
		}

		_, err = RefreshTokenSet(context.Background(), profile, "rt")
		if err == nil || strings.Contains(err.Error(), stray) {
			t.Fatalf("refresh status %d: err = %v (must exist and not carry the token)", status, err)
		}
		if strings.Contains(logText(logs), stray) {
			t.Fatalf("refresh status %d: the token reached the log", status)
		}
		if !strings.Contains(err.Error(), "invalid_grant") {
			t.Errorf("refresh status %d: lost the OAuth error code: %v", status, err)
		}
	}
}
