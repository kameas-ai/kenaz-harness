package forkconversation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/tools/forkconversation"
)

// fakeForker records requests. Race-safe: reads go through snapshot().
type fakeForker struct {
	mu   sync.Mutex
	reqs []forkconversation.ForkRequest
	res  forkconversation.ForkResult
	err  error
}

func (f *fakeForker) Fork(_ context.Context, req forkconversation.ForkRequest) (forkconversation.ForkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.res, f.err
}

func (f *fakeForker) snapshot() []forkconversation.ForkRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]forkconversation.ForkRequest, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func call(t *testing.T, tool *forkconversation.Tool, ctx context.Context, args string) map[string]any {
	t.Helper()
	raw, err := tool.Call(ctx, json.RawMessage(args))
	if err != nil {
		t.Fatalf("Call returned Go error: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, raw)
	}
	return out
}

func TestForkConversation_Success_UsesContextSession(t *testing.T) {
	t.Parallel()
	f := &fakeForker{res: forkconversation.ForkResult{
		BranchID: "br-1", BranchSessionID: "child-1", Title: "Tangent", FromMessageID: "m-9", HandoffSeeded: true,
	}}
	tool := forkconversation.New(forkconversation.Options{Forker: f})
	ctx := toolloop.WithSessionID(context.Background(), "sess-A")

	out := call(t, tool, ctx, `{"title":"  Tangent ","handoff":"explore X","from_message_id":"m-9"}`)

	if out["branch_id"] != "br-1" || out["branch_session_id"] != "child-1" || out["title"] != "Tangent" {
		t.Fatalf("unexpected success envelope: %v", out)
	}
	if out["handoff_seeded"] != true {
		t.Errorf("handoff_seeded = %v, want true", out["handoff_seeded"])
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "branches sidebar") || !strings.Contains(msg, "does not run on its own") {
		t.Errorf("result message must tell the model where the user opens the fork and that it is dormant; got %q", msg)
	}
	reqs := f.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("forker calls = %d, want 1", len(reqs))
	}
	want := forkconversation.ForkRequest{ParentSessionID: "sess-A", Title: "Tangent", Handoff: "explore X", FromMessageID: "m-9"}
	if reqs[0] != want {
		t.Errorf("forker request = %+v, want %+v", reqs[0], want)
	}
}

// TestForkConversation_ForgedSessionArgumentRefused: the parent session
// is the dispatch context's, never an argument. A smuggled session id is
// refused outright and the forker is never reached.
func TestForkConversation_ForgedSessionArgumentRefused(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"session_id", "parent_session_id", "sessionId"} {
		f := &fakeForker{}
		tool := forkconversation.New(forkconversation.Options{Forker: f})
		ctx := toolloop.WithSessionID(context.Background(), "sess-A")
		out := call(t, tool, ctx, `{"title":"x","`+field+`":"sess-OTHER"}`)
		if out["error"] != "invalid_args" {
			t.Errorf("%s: error = %v, want invalid_args", field, out["error"])
		}
		if n := len(f.snapshot()); n != 0 {
			t.Errorf("%s: forker called %d times for a forged-session call; want 0", field, n)
		}
	}
}

func TestForkConversation_SchemaHasNoSessionArgument(t *testing.T) {
	t.Parallel()
	tool := forkconversation.New(forkconversation.Options{Forker: &fakeForker{}})
	var schema struct {
		Properties           map[string]any `json:"properties"`
		Required             []string       `json:"required"`
		AdditionalProperties *bool          `json:"additionalProperties"`
	}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	got := map[string]bool{}
	for k := range schema.Properties {
		got[k] = true
	}
	if len(got) != 3 || !got["title"] || !got["handoff"] || !got["from_message_id"] {
		t.Errorf("schema properties = %v, want exactly title/handoff/from_message_id", got)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "title" {
		t.Errorf("required = %v, want [title]", schema.Required)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Error("additionalProperties must be false")
	}
	if tool.Name() != "kenaz__fork_conversation" {
		t.Errorf("Name = %q", tool.Name())
	}
}

func TestForkConversation_NoSessionInContext(t *testing.T) {
	t.Parallel()
	f := &fakeForker{}
	tool := forkconversation.New(forkconversation.Options{Forker: f})
	out := call(t, tool, context.Background(), `{"title":"x"}`)
	if out["error"] != "no_session" {
		t.Errorf("error = %v, want no_session", out["error"])
	}
	if len(f.snapshot()) != 0 {
		t.Error("forker must not be called without a dispatch session")
	}
}

func TestForkConversation_InvalidArgs(t *testing.T) {
	t.Parallel()
	ctx := toolloop.WithSessionID(context.Background(), "s")
	cases := map[string]string{
		"empty title":     `{"title":"   "}`,
		"missing":         `{}`,
		"not json":        `nope`,
		"title too long":  `{"title":"` + strings.Repeat("a", forkconversation.MaxTitleRunes+1) + `"}`,
		"handoff too big": `{"title":"x","handoff":"` + strings.Repeat("a", forkconversation.MaxHandoffBytes+1) + `"}`,
	}
	for name, args := range cases {
		f := &fakeForker{}
		tool := forkconversation.New(forkconversation.Options{Forker: f})
		out := call(t, tool, ctx, args)
		if out["error"] != "invalid_args" {
			t.Errorf("%s: error = %v, want invalid_args", name, out["error"])
		}
		if len(f.snapshot()) != 0 {
			t.Errorf("%s: forker called on invalid args", name)
		}
	}
	tool := forkconversation.New(forkconversation.Options{Forker: &fakeForker{}})
	if raw, err := tool.Call(ctx, nil); err != nil || !strings.Contains(string(raw), "invalid_args") {
		t.Errorf("nil args: raw=%s err=%v", raw, err)
	}
}

// TestForkConversation_SeedFailureIsReportedNotClaimed: the branch exists
// but the handoff did not land — the model must be told, with the ids.
func TestForkConversation_SeedFailureIsReportedNotClaimed(t *testing.T) {
	t.Parallel()
	f := &fakeForker{
		res: forkconversation.ForkResult{BranchID: "br-2", BranchSessionID: "child-2", Title: "t"},
		err: errors.Join(forkconversation.ErrSeedFailed, errors.New("disk full")),
	}
	tool := forkconversation.New(forkconversation.Options{Forker: f})
	out := call(t, tool, toolloop.WithSessionID(context.Background(), "s"), `{"title":"t","handoff":"h"}`)
	if out["error"] != "handoff_not_seeded" {
		t.Fatalf("error = %v, want handoff_not_seeded", out["error"])
	}
	if out["branch_session_id"] != "child-2" || out["branch_id"] != "br-2" {
		t.Errorf("partial-success ids missing: %v", out)
	}
}

func TestForkConversation_NothingToForkAndGenericFailure(t *testing.T) {
	t.Parallel()
	ctx := toolloop.WithSessionID(context.Background(), "s")
	tool := forkconversation.New(forkconversation.Options{Forker: &fakeForker{err: forkconversation.ErrNothingToFork}})
	if out := call(t, tool, ctx, `{"title":"t"}`); out["error"] != "nothing_to_fork" {
		t.Errorf("error = %v, want nothing_to_fork", out["error"])
	}
	tool = forkconversation.New(forkconversation.Options{Forker: &fakeForker{err: errors.New("boom")}})
	out := call(t, tool, ctx, `{"title":"t"}`)
	if out["error"] != "fork_failed" {
		t.Errorf("error = %v, want fork_failed", out["error"])
	}
	if _, ok := out["branch_session_id"]; ok {
		t.Error("a failed fork must not report a branch_session_id")
	}
}

func TestForkConversation_NilForkerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("New with nil Forker must panic (a tool that can only fail is never registered)")
		}
	}()
	forkconversation.New(forkconversation.Options{})
}

// TestForkConversation_TurnSpanComesFromContextOnly (review M1): the live
// turn's span reaches the Forker from the dispatch context, never from a
// model argument.
func TestForkConversation_TurnSpanComesFromContextOnly(t *testing.T) {
	t.Parallel()
	f := &fakeForker{res: forkconversation.ForkResult{BranchID: "b", BranchSessionID: "c", Title: "t"}}
	tool := forkconversation.New(forkconversation.Options{Forker: f})
	ctx := toolloop.WithTurnSpanID(toolloop.WithSessionID(context.Background(), "sess-A"), "span-1")
	_ = call(t, tool, ctx, `{"title":"t"}`)
	reqs := f.snapshot()
	if len(reqs) != 1 || reqs[0].TurnSpanID != "span-1" {
		t.Fatalf("forker requests = %+v, want TurnSpanID span-1", reqs)
	}
	out := call(t, tool, ctx, `{"title":"t","turn_span_id":"forged"}`)
	if out["error"] != "invalid_args" || len(f.snapshot()) != 1 {
		t.Fatalf("a turn_span_id argument must be refused; got %v (calls=%d)", out, len(f.snapshot()))
	}
}

// TestForkConversation_TrailingJSONRefused (review L3).
func TestForkConversation_TrailingJSONRefused(t *testing.T) {
	t.Parallel()
	ctx := toolloop.WithSessionID(context.Background(), "s")
	for _, args := range []string{
		`{"title":"x"} {"title":"y"}`,
		`{"title":"x"}}`,
		`{"title":"x"} garbage`,
	} {
		f := &fakeForker{}
		tool := forkconversation.New(forkconversation.Options{Forker: f})
		out := call(t, tool, ctx, args)
		if out["error"] != "invalid_args" {
			t.Errorf("%s: error = %v, want invalid_args", args, out["error"])
		}
		if len(f.snapshot()) != 0 {
			t.Errorf("%s: forker called", args)
		}
	}
	// Trailing whitespace is not content.
	f := &fakeForker{res: forkconversation.ForkResult{BranchID: "b", BranchSessionID: "c", Title: "x"}}
	if out := call(t, forkconversation.New(forkconversation.Options{Forker: f}), ctx, "{\"title\":\"x\"}\n  "); out["error"] != nil {
		t.Errorf("trailing whitespace refused: %v", out)
	}
}

// TestForkConversation_DepthLimitIsHonest (review L4): the depth cap is
// reported as a depth limit, not as a "cycle".
func TestForkConversation_DepthLimitIsHonest(t *testing.T) {
	t.Parallel()
	f := &fakeForker{err: errors.Join(forkconversation.ErrDepthLimit, errors.New("conversation: branch cycle detected"))}
	out := call(t, forkconversation.New(forkconversation.Options{Forker: f}),
		toolloop.WithSessionID(context.Background(), "s"), `{"title":"t"}`)
	if out["error"] != "branch_depth_limit" {
		t.Fatalf("error = %v, want branch_depth_limit", out["error"])
	}
	if msg, _ := out["message"].(string); strings.Contains(msg, "cycle") || !strings.Contains(msg, "depth") {
		t.Errorf("message must name the depth limit, not a cycle: %q", msg)
	}
}

// TestForkConversation_DescriptionNamesTheDefaultAnchor (review L5).
func TestForkConversation_DescriptionNamesTheDefaultAnchor(t *testing.T) {
	t.Parallel()
	tool := forkconversation.New(forkconversation.Options{Forker: &fakeForker{}})
	for _, s := range []string{tool.Description(), string(tool.InputSchema())} {
		if !strings.Contains(s, "user or assistant message before the current turn") {
			t.Errorf("does not state the real default anchor: %s", s)
		}
	}
}
