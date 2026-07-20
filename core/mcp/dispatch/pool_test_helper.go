package dispatch

import coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"

// TestOptions is a test-friendly variant of Options that accepts interface
// types for all sub-pools. It is used by NewForTest so test code can inject
// in-memory fakes without importing the concrete transport packages.
//
// Exported so external test packages (e.g. package dispatch_test) can call
// NewForTest without importing core/mcp/transport/stdio etc.
type TestOptions struct {
	Stdio StdioSubPool
	HTTP  coremcp.Pool
	SSE   coremcp.Pool
}

// NewForTest builds a Pool from interface-based options. It bypasses the
// concrete-type requirements of New so tests can inject fakes.
//
// Exported so external test packages (package dispatch_test) can call it.
func NewForTest(opts TestOptions) *Pool {
	p := &Pool{
		stdioPool: opts.Stdio,
		ownership: make(map[string]string),
	}
	if opts.HTTP != nil {
		p.httpPool = opts.HTTP
	}
	if opts.SSE != nil {
		p.ssePool = opts.SSE
	}
	return p
}
