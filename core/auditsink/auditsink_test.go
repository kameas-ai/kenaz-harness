package auditsink_test

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/auditsink"
	"github.com/kameas-ai/kenaz-harness/core/auditsink/auditsinktest"
)

func TestTool_NoDefaultOrNoTaskIsNoop(t *testing.T) {
	auditsink.SetDefault(nil)
	auditsink.Tool(context.Background(), "x")(true) // must not panic

	sock := auditsinktest.New(t)
	auditsink.SetDefault(auditsink.New(sock.Addr(), "tcp", nil))
	t.Cleanup(func() { auditsink.SetDefault(nil) })
	auditsink.Tool(context.Background(), "x")(false) // no task on ctx
	time.Sleep(50 * time.Millisecond)
	if n := len(sock.Records()); n != 0 {
		t.Fatalf("records = %d, want 0 without a task id on ctx", n)
	}
}

func TestTool_WritesCallThenResult(t *testing.T) {
	sock := auditsinktest.New(t)
	auditsink.SetDefault(auditsink.New(sock.Addr(), "tcp", nil))
	t.Cleanup(func() { auditsink.SetDefault(nil) })

	ctx := auditsink.WithTask(context.Background(), "run-1")
	auditsink.Tool(ctx, "fs__list_dir")(true)

	recs := sock.WaitFor(t, 2*time.Second, func(r []auditsink.Record) bool { return len(r) >= 2 })
	if recs[0].Kind != auditsink.KindToolCall || recs[1].Kind != auditsink.KindToolResult {
		t.Fatalf("kinds = %s, %s", recs[0].Kind, recs[1].Kind)
	}
	for _, r := range recs {
		if r.TaskID != "run-1" || r.Tool != "fs__list_dir" {
			t.Fatalf("record = %+v", r)
		}
	}
	if recs[1].ExitCode != 1 {
		t.Fatalf("failed tool exit_code = %d, want 1", recs[1].ExitCode)
	}
}
