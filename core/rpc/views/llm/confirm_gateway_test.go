package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

func TestConfirmGateway_RequestEmitsBothTopics(t *testing.T) {
	sink := &recordingSink{}
	gw := NewConfirmGateway(sink)

	doneCh := make(chan struct{})
	var (
		decision toolloop.ConfirmDecision
		err      error
	)
	go func() {
		defer close(doneCh)
		decision, err = gw.RequestConfirm(context.Background(), toolloop.ConfirmRequest{
			RequestID:    "req-1",
			SessionID:    "sess-1",
			ParentSubID:  "sub-1",
			Server:       "fs",
			Tool:         "delete",
			ToolUseID:    "tu-1",
			ArgsRedacted: `{"path":"/etc/hosts"}`,
		})
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sink.topicCount("llm:stream-chunk") >= 1 && sink.topicCount("llm:tool-confirm-request") >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.topicCount("llm:stream-chunk"); got < 1 {
		t.Errorf("llm:stream-chunk emissions = %d, want >= 1", got)
	}
	if got := sink.topicCount("llm:tool-confirm-request"); got < 1 {
		t.Errorf("llm:tool-confirm-request emissions = %d, want >= 1", got)
	}

	if err := gw.ResolveConfirm("req-1", toolloop.ConfirmAlwaysAllow); err != nil {
		t.Fatalf("ResolveConfirm: %v", err)
	}
	<-doneCh
	if err != nil {
		t.Fatalf("RequestConfirm err: %v", err)
	}
	if decision != toolloop.ConfirmAlwaysAllow {
		t.Fatalf("decision = %q, want always_allow", decision)
	}
}

func TestConfirmGateway_ResolveUnknownReqID(t *testing.T) {
	gw := NewConfirmGateway(nil)
	if err := gw.ResolveConfirm("nope", toolloop.ConfirmAllow); err == nil {
		t.Fatal("expected error resolving unknown request id")
	}
}

func TestConfirmGateway_RejectsUnknownDecision(t *testing.T) {
	gw := NewConfirmGateway(nil)
	if err := gw.ResolveConfirm("any", toolloop.ConfirmDecision("yolo")); err == nil {
		t.Fatal("expected error on unknown decision")
	}
}

func TestConfirmGateway_CtxCancelDuringWait(t *testing.T) {
	gw := NewConfirmGateway(nil)
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		_, err := gw.RequestConfirm(ctx, toolloop.ConfirmRequest{RequestID: "r"})
		doneCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestConfirm did not unwind on ctx cancel")
	}
}
