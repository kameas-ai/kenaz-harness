package chat

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/auditsink"
	"github.com/kameas-ai/kenaz-harness/core/auditsink/auditsinktest"
)

// TestChatTurn_WritesAuditLineProtocol pins audit finding WC-6: a served
// chat turn that makes one tool call writes exactly four metadata-only
// records to the harness audit socket — task.start, task.tool_call,
// task.tool_result, task.complete — all keyed by the turn's run id, with
// the tool's NAME and nothing else from the call.
//
// Not t.Parallel: it installs the process-default sink. Records are
// filtered by this turn's run id so a stray run from another test can
// not pollute the count.
func TestChatTurn_WritesAuditLineProtocol(t *testing.T) {
	sock := auditsinktest.New(t)
	auditsink.SetDefault(auditsink.New(sock.Addr(), "tcp", nil))
	t.Cleanup(func() { auditsink.SetDefault(nil) })

	const secretArg = "sk-live-not-a-real-key-123"
	reg := &scriptedRegistry{}
	reg.push(toolTurn("looking", "tu-1", "fs__list_dir", `{"path":"`+secretArg+`"}`))
	reg.push(textTurn("done"))
	pool := &scriptedPool{
		entries: []ToolEntry{{Server: "fs", Name: "list_dir"}},
		results: [][]byte{[]byte(`{"entries":["` + secretArg + `"]}`)},
	}
	runner, broker, _ := buildMoveRunner(t, reg, pool)
	runID, err := runner.StartStream(context.Background(), "p", "s", "", testTurn("list it"))
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	mine := func(all []auditsink.Record) []auditsink.Record {
		var out []auditsink.Record
		for _, r := range all {
			if r.TaskID == runID {
				out = append(out, r)
			}
		}
		return out
	}
	recs := mine(sock.WaitFor(t, 3*time.Second, func(all []auditsink.Record) bool {
		return len(mine(all)) >= 4
	}))
	time.Sleep(50 * time.Millisecond) // let any surplus record land
	recs = mine(sock.Records())

	want := []string{
		auditsink.KindTaskStart,
		auditsink.KindToolCall,
		auditsink.KindToolResult,
		auditsink.KindTaskComplete,
	}
	if len(recs) != len(want) {
		t.Fatalf("got %d records for run %s, want 4: %+v", len(recs), runID, recs)
	}
	for i, r := range recs {
		if r.Kind != want[i] {
			t.Fatalf("record %d kind = %q, want %q (all: %+v)", i, r.Kind, want[i], recs)
		}
	}
	if recs[1].Tool != "fs__list_dir" || recs[2].Tool != "fs__list_dir" {
		t.Fatalf("tool records name %q / %q, want fs__list_dir", recs[1].Tool, recs[2].Tool)
	}
	if recs[2].ExitCode != 0 || recs[3].ExitCode != 0 {
		t.Fatalf("exit codes = %d / %d, want 0 / 0", recs[2].ExitCode, recs[3].ExitCode)
	}
}
