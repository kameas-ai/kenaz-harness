package main

import (
	"reflect"
	"testing"
)

// TestMCPDispatchArgs pins the exact decision main.go's early-dispatch
// branch makes before flag.Parse, core.New or paths.DataDir ever run.
// entry-points-and-crash-reporting-01PMZD13 UNIT-8, AC-19's falsifiable
// form: "a test asserting that mcp arguments never reach
// paths.DataDir()". This is that test, at the boundary that decides it —
// main() calls paths.DataDir() unconditionally on every path that does
// NOT return true here, so proving mcpDispatchArgs correctly identifies
// every `mcp ...` invocation (and only those) proves the DataDir call is
// unreachable on that argv shape, without needing to run main() itself
// (which needs the `serve` build tag and a real data directory — see
// dispatch.go's header for why that would never execute in CI anyway).
func TestMCPDispatchArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantIsMCP  bool
		wantMCPArg []string
	}{
		{
			name:       "mcp with a server name",
			args:       []string{"harness-served", "mcp", "sites"},
			wantIsMCP:  true,
			wantMCPArg: []string{"sites"},
		},
		{
			name:       "mcp with extra arguments",
			args:       []string{"harness-served", "mcp", "sites", "--foo", "bar"},
			wantIsMCP:  true,
			wantMCPArg: []string{"sites", "--foo", "bar"},
		},
		{
			name:       "mcp with no server name — still dispatches, empty arg slice",
			args:       []string{"harness-served", "mcp"},
			wantIsMCP:  true,
			wantMCPArg: []string{},
		},
		{
			name:      "no arguments — falls through to normal boot",
			args:      []string{"harness-served"},
			wantIsMCP: false,
		},
		{
			name:      "unrelated first argument — falls through to normal boot",
			args:      []string{"harness-served", "--listen", "0.0.0.0:7880"},
			wantIsMCP: false,
		},
		{
			name:      "mcp as a later argument, not first — does NOT dispatch",
			args:      []string{"harness-served", "--listen", "mcp"},
			wantIsMCP: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIsMCP, gotArgs := mcpDispatchArgs(tc.args)
			if gotIsMCP != tc.wantIsMCP {
				t.Fatalf("mcpDispatchArgs(%v) isMCP = %v, want %v", tc.args, gotIsMCP, tc.wantIsMCP)
			}
			if tc.wantIsMCP && !reflect.DeepEqual(gotArgs, tc.wantMCPArg) {
				t.Fatalf("mcpDispatchArgs(%v) mcpArgs = %v, want %v", tc.args, gotArgs, tc.wantMCPArg)
			}
		})
	}
}
