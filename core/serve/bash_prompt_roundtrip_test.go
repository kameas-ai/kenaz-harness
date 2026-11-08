package serve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
)

// TestServedBash_PromptRoundTrip drives the full served-mode prompt path
// a fleet-enabled bash command with no matching run_bash_command rule
// takes: the shared PromptRegistry raises `bash:permission-pending`, the
// WS pump forwards it to the session's socket, the browser answers with
// Permissions_Resolve over /rpc, and the blocked gate resumes with that
// decision. Without every hop the bash call would hang until
// PromptTimeout inside the workbench.
func TestServedBash_PromptRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		answer string
		want   cedar.PromptDecision
	}{
		{"allow_once", cedar.DecisionAllowOnce},
		{"deny", cedar.DecisionDeny},
	} {
		t.Run(tc.answer, func(t *testing.T) {
			api, baseURL, cancel := newChatHarness(t)
			defer cancel()

			const sessionID = "sess-bash-prompt"
			ws := dialChatWS(t, baseURL, sessionID)
			defer ws.Close() //nolint:errcheck

			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			type result struct {
				res cedar.Resolution
				err error
			}
			done := make(chan result, 1)
			go func() {
				res, err := api.PromptRegistry().RequestInteractive(ctx, cedar.PromptSurface{
					Bash: &cedar.BashPromptSurface{
						Pattern: "uname",
						Argv:    []string{"uname", "-a"},
					},
					SessionID: sessionID,
				})
				done <- result{res, err}
			}()

			f := readUntil(t, ws, rpc.TopicBashPermissionPending, 5*time.Second)
			var pending struct {
				RequestID string `json:"request_id"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(f.Data, &pending); err != nil {
				t.Fatalf("decode pending: %v (%s)", err, f.Data)
			}
			if pending.RequestID == "" || pending.SessionID != sessionID {
				t.Fatalf("pending frame missing request/session id: %s", f.Data)
			}

			resp := authedPost(t, baseURL, "tok", fmt.Sprintf(
				`{"method":"Permissions_Resolve","params":{"requestId":%q,"decision":%q}}`,
				pending.RequestID, tc.answer))
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != 200 {
				t.Fatalf("Permissions_Resolve: %d %s", resp.StatusCode, body)
			}

			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("RequestInteractive: %v", r.err)
				}
				if r.res.Decision != tc.want {
					t.Fatalf("decision = %q, want %q", r.res.Decision, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("gate still blocked after Permissions_Resolve")
			}
		})
	}
}
