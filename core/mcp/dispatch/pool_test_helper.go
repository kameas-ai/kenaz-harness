package dispatch

// TestOptions is a test-friendly variant of Options that accepts interface
// types for all sub-pools. It is used by NewForTest so test code can inject
// in-memory fakes without importing the concrete transport packages.
//
// HTTP/SSE are typed as RemoteSubPool (not the narrower coremcp.Pool)
// so a fake injected here must implement CloseOne too — the same
// requirement New()'s concrete-typed Options.HTTP/SSE fields carry.
// Before UNIT-6 these were bare coremcp.Pool, which let a fake sub-pool
// satisfy TestOptions without ever implementing CloseOne — exactly the
// gap that let the dispatch layer's http/sse CloseOne no-op ship
// untested (spec.md test rule R-2: "a fake sub-pool proves nothing
// about http.Pool").
//
// Exported so external test packages (e.g. package dispatch_test) can call
// NewForTest without importing core/mcp/transport/stdio etc.
type TestOptions struct {
	Stdio StdioSubPool
	HTTP  RemoteSubPool
	SSE   RemoteSubPool
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
