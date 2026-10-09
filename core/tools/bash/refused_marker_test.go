package bash_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// ml-producer-01MLPRD01 WP02 (spec §12 A-10): bash's own gate refusals
// carry an explicit `refused` marker so a consumer can tell "no process
// ran" from a real process that exited -1; a command that ran carries
// neither marker, so its result bytes are unchanged.
func TestResultMarkers_RefusedVersusRan(t *testing.T) {
	root := t.TempDir()
	tool := bash.New(bash.Options{SandboxRoot: root, Allowlist: []string{"sh"}})
	type res struct {
		ExitCode int   `json:"exit_code"`
		Refused  *bool `json:"refused"`
		NotRun   *bool `json:"not_run"`
	}
	call := func(args string) res {
		t.Helper()
		raw, err := tool.Call(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("Call(%s): %v", args, err)
		}
		var r res
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return r
	}

	if r := call(`{"command":"rm -rf /tmp/nothing"}`); r.ExitCode != -1 || r.Refused == nil || !*r.Refused || r.NotRun != nil {
		t.Errorf("allowlist refusal = %+v, want exit -1 with refused:true", r)
	}
	if r := call(`{"command":"sh -c 'exit 3'"}`); r.ExitCode != 3 || r.Refused != nil || r.NotRun != nil {
		t.Errorf("a command that ran = %+v, want exit 3 and no marker", r)
	}
}
