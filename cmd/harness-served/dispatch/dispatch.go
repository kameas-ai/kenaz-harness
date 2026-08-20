// Package dispatch carries NO `//go:build serve` tag deliberately —
// entry-points-and-crash-reporting-01PMZD13 UNIT-8. main.go IS tagged
// `serve`, so nothing in that file ever compiled or ran under the plain
// `go test ./cmd/...` CI already runs (pr.yml's test-go job never passes
// `-tags serve`; only the separate lint-go "go build (serve tag)" step
// does, and that is a BUILD, not a test). A test file carrying the same
// `serve` tag would therefore never execute in CI — the exact "gate that
// cannot fail" class this campaign exists to end.
//
// This is a SEPARATE LIBRARY PACKAGE, not a file dropped directly into
// cmd/harness-served's `package main` — that was tried first and broke
// `go build ./...`: an untagged file inside a `main` package whose only
// other file (main.go) is `//go:build serve`-gated leaves that package
// with real content but no `func main()` under the default (non-serve)
// build, and `go build ./...` requires a linkable main() for every
// `package main` it discovers, not just when explicitly targeted — it
// does not silently skip a main-less main package the way `go vet`/
// `go test` do. A library package under cmd/harness-served/dispatch/ has
// no such requirement; main.go imports it.
package dispatch

// MCPArgs reports whether os.Args (the process's real argv, or a fixture
// in tests) requests MCP stdio dispatch, and if so, the arguments to hand
// to mcpsubcmd.Dispatch. Mirrors main.go's own
// `if len(os.Args) >= 2 && os.Args[1] == "mcp"` condition exactly — kept
// as a pure function, with no side effects, specifically so it is
// testable without the `serve` build tag or any of main()'s core/paths/
// core.New machinery.
func MCPArgs(args []string) (isMCP bool, mcpArgs []string) {
	if len(args) >= 2 && args[1] == "mcp" {
		return true, args[2:]
	}
	return false, nil
}
