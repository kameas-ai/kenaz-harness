// dispatch.go carries NO `//go:build serve` tag deliberately —
// entry-points-and-crash-reporting-01PMZD13 UNIT-8. main.go IS tagged
// `serve`, so nothing in this package ever compiled or ran under the plain
// `go test ./cmd/...` CI already runs (pr.yml's test-go job never passes
// `-tags serve`; only the separate lint-go "go build (serve tag)" step
// does, and that is a BUILD, not a test). A test file carrying the same
// `serve` tag would therefore never execute in CI — the exact "gate that
// cannot fail" class this campaign exists to end. Pulling the one piece of
// argv-dispatch LOGIC this unit needs to prove out of main.go and into an
// untagged file lets dispatch_test.go run under the ordinary, already-CI'd
// `go test ./cmd/...` with no workflow change.
package main

// mcpDispatchArgs reports whether os.Args (the process's real argv, or a
// fixture in tests) requests MCP stdio dispatch, and if so, the arguments
// to hand to mcpsubcmd.Dispatch. Mirrors main.go's own
// `if len(os.Args) >= 2 && os.Args[1] == "mcp"` condition exactly — kept
// as a pure function, with no side effects, specifically so it is
// testable without the `serve` build tag or any of main()'s core/paths/
// core.New machinery.
func mcpDispatchArgs(args []string) (isMCP bool, mcpArgs []string) {
	if len(args) >= 2 && args[1] == "mcp" {
		return true, args[2:]
	}
	return false, nil
}
