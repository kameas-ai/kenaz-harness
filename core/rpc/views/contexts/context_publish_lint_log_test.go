package contexts_test

// context_publish_lint_log_test.go — review R5. A lint_blocked refusal
// carries server-redacted excerpts of the user's body. They are shown to the
// user in the returned error and must NEVER reach a log line. Planted
// excerpt: it must appear in the error and in no captured slog record.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

const plantedExcerpt = "PLANTED-EXCERPT-fake-credential-sentinel"

// lockedBuf is a race-safe sink for a JSON slog handler.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestContextPublish_LintBlocked_ExcerptNeverLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"code":"lint_blocked","message":"blocked","details":{"node_id":"node-1","findings":[{"rule":"stripe_secret_key","severity":"block","excerpt":"`+plantedExcerpt+`"}]}}`)
	}))
	defer srv.Close()
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	logs := &lockedBuf{}
	prev := logging.Handler()
	logging.Replace(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { logging.Replace(prev) })

	client := corefleet.NewClientForTesting(srv.URL)
	poller := corefleet.NewCapabilityPoller(client, t.TempDir())
	poller.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier:      "enterprise",
		Enabled:   map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true},
		FetchedAt: time.Now(),
	})
	api := contextsview.New(nil).WithSyncer(corefleet.NewContextGraphSyncer(client, t.TempDir(), poller))

	_, err := api.Context_Publish(context.Background(), contextsview.ContextPublishRequest{
		NodeID: "node-1", Layer: "org", Kind: "guidance", Title: "t", Body: "b", Version: 1,
	})
	if !errors.Is(err, corefleet.ErrContextLintBlocked) {
		t.Fatalf("err = %v, want ErrContextLintBlocked", err)
	}
	if !strings.Contains(err.Error(), plantedExcerpt) {
		t.Fatalf("the UI error %q should carry the excerpt so the user can find the secret", err.Error())
	}
	out := logs.String()
	if !strings.Contains(out, "contexts.publish.failed") {
		t.Fatalf("expected the publish failure to be logged; captured:\n%s", out)
	}
	if strings.Contains(out, plantedExcerpt) || strings.Contains(out, "PLANTED-EXCERPT") {
		t.Fatalf("lint excerpt leaked into slog output:\n%s", out)
	}
	if !strings.Contains(out, "lint_blocked") {
		t.Errorf("log should still name the code; captured:\n%s", out)
	}
}

func TestContextPushError_LogValue_OmitsExcerpt(t *testing.T) {
	logs := &lockedBuf{}
	l := slog.New(slog.NewJSONHandler(logs, nil))
	pe := &corefleet.ContextPushError{Op: "push", Status: 422, Code: "lint_blocked",
		Findings: []corefleet.LintFinding{{Rule: "r", Severity: "block", Excerpt: plantedExcerpt}}}
	l.Warn("x", "err", pe)
	if strings.Contains(logs.String(), "PLANTED-EXCERPT") {
		t.Fatalf("LogValue leaked the excerpt: %s", logs.String())
	}
}
