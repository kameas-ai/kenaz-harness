package serve_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// TestServedPermissionPrompt_ReachesRaisingSessionOnly pins audit finding
// HR-1 end to end: a filesystem prompt raised by a tool call running in
// session S — through the real CedarPrompter and the API's real prompt
// registry, dispatcher, broker and event bus — reaches S's WebSocket and
// no other session's. Before the fix the surface carried no session id,
// frameFor dropped the frame for every socket, the permission modal never
// opened, and the tool call blocked for the full PromptTimeout.
func TestServedPermissionPrompt_ReachesRaisingSessionOnly(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	const (
		sessionS = "sess-hr1-raiser"
		sessionT = "sess-hr1-bystander"
	)
	wsS := dialChatWS(t, baseURL, sessionS)
	defer wsS.Close() //nolint:errcheck
	wsT := dialChatWS(t, baseURL, sessionT)
	defer wsT.Close() //nolint:errcheck

	registry := api.PromptRegistry()
	if registry == nil {
		t.Fatal("api.PromptRegistry() is nil")
	}
	prompter := &corefs.CedarPrompter{Registry: registry}

	// The dispatch ctx is what kernelToolAdapter hands the tool: it
	// carries the session id and nothing else that names the session.
	ctx := toolloop.WithSessionID(context.Background(), sessionS)
	ctx = cedar.WithPromptPosture(ctx, cedar.PostureAlwaysPrompt)
	result := make(chan corefs.PromptResponse, 1)
	go func() {
		resp, _ := prompter.Prompt(ctx, corefs.PromptSurface{
			Op:            corefs.OpRead,
			CanonicalPath: "/workspace",
		})
		result <- resp
	}()

	f := readUntil(t, wsS, rpc.TopicFSPermissionPending, 5*time.Second)
	var flat rpc.FlatPermissionRequest
	if err := json.Unmarshal(f.Data, &flat); err != nil {
		t.Fatalf("decode %s payload: %v", rpc.TopicFSPermissionPending, err)
	}
	if flat.SessionID != sessionS {
		t.Fatalf("frame session_id = %q, want %q", flat.SessionID, sessionS)
	}

	// The bystander session must not see (and so cannot answer) S's prompt.
	assertNoFrame(t, wsT, 500*time.Millisecond, "bystander session, fs:permission-pending")

	if err := registry.Resolve(flat.RequestID, cedar.DecisionAllowOnce); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case resp := <-result:
		if resp != corefs.PromptAllowOnce {
			t.Fatalf("Prompt = %v, want PromptAllowOnce", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return after Resolve")
	}
}
