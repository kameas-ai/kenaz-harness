package recorders

import (
	"context"
	"sync"
	"testing"
)

// PolicyCheckCall records one call to any PolicyGate method.
type PolicyCheckCall struct {
	Method string // "CheckFileRead" | "CheckFileWrite" | "CheckStateRead" | "CheckStateWrite" | "CheckTool"
	Ctx    context.Context
	Arg    string // path, source, or fully-qualified tool name
}

// PolicyGate is a recording fake that satisfies agentgraph.PolicyGate.
// By default all checks pass (return nil); set DenyAll to deny every check
// or use DenyFn for fine-grained control.
type PolicyGate struct {
	mu      sync.Mutex
	calls   []PolicyCheckCall
	// DenyAll causes every check to return DenyErr.
	DenyAll bool
	// DenyErr is the error returned when DenyAll is set. Defaults to a
	// generic "recorder: access denied" error if nil.
	DenyErr error
	// DenyFn overrides DenyAll; if non-nil it is called on every check.
	DenyFn func(method, arg string) error
}

func (r *PolicyGate) check(ctx context.Context, method, arg string) error {
	r.mu.Lock()
	r.calls = append(r.calls, PolicyCheckCall{Method: method, Ctx: ctx, Arg: arg})
	denyAll := r.DenyAll
	denyErr := r.DenyErr
	fn := r.DenyFn
	r.mu.Unlock()
	if fn != nil {
		return fn(method, arg)
	}
	if denyAll {
		if denyErr != nil {
			return denyErr
		}
		return errDenied(method, arg)
	}
	return nil
}

// CheckFileRead satisfies agentgraph.PolicyGate.
func (r *PolicyGate) CheckFileRead(ctx context.Context, path string) error {
	return r.check(ctx, "CheckFileRead", path)
}

// CheckFileWrite satisfies agentgraph.PolicyGate.
func (r *PolicyGate) CheckFileWrite(ctx context.Context, path string) error {
	return r.check(ctx, "CheckFileWrite", path)
}

// CheckStateRead satisfies agentgraph.PolicyGate.
func (r *PolicyGate) CheckStateRead(ctx context.Context, source string) error {
	return r.check(ctx, "CheckStateRead", source)
}

// CheckStateWrite satisfies agentgraph.PolicyGate.
func (r *PolicyGate) CheckStateWrite(ctx context.Context, target string) error {
	return r.check(ctx, "CheckStateWrite", target)
}

// CheckTool satisfies agentgraph.PolicyGate.
func (r *PolicyGate) CheckTool(ctx context.Context, toolName string) error {
	return r.check(ctx, "CheckTool", toolName)
}

// Calls returns a snapshot of all recorded policy check calls.
func (r *PolicyGate) Calls() []PolicyCheckCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]PolicyCheckCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// CallCount returns the total number of policy check calls recorded.
func (r *PolicyGate) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// CallsForMethod returns all calls for the given method name.
func (r *PolicyGate) CallsForMethod(method string) []PolicyCheckCall {
	var out []PolicyCheckCall
	for _, c := range r.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// RequireChecked asserts at least one check call matches the given matcher.
func (r *PolicyGate) RequireChecked(t *testing.T, match func(PolicyCheckCall) bool, desc string) {
	t.Helper()
	for _, c := range r.Calls() {
		if match(c) {
			return
		}
	}
	t.Fatalf("PolicyGate: no check found matching %q", desc)
}

// RequireFileReadChecked asserts CheckFileRead was called for the given path.
func (r *PolicyGate) RequireFileReadChecked(t *testing.T, path string) {
	t.Helper()
	r.RequireChecked(t, func(c PolicyCheckCall) bool {
		return c.Method == "CheckFileRead" && c.Arg == path
	}, "CheckFileRead path="+path)
}

// RequireFileWriteChecked asserts CheckFileWrite was called for the given path.
func (r *PolicyGate) RequireFileWriteChecked(t *testing.T, path string) {
	t.Helper()
	r.RequireChecked(t, func(c PolicyCheckCall) bool {
		return c.Method == "CheckFileWrite" && c.Arg == path
	}, "CheckFileWrite path="+path)
}

// RequireNeverChecked asserts no policy checks were recorded.
func (r *PolicyGate) RequireNeverChecked(t *testing.T) {
	t.Helper()
	if n := r.CallCount(); n != 0 {
		t.Errorf("PolicyGate: want 0 checks, got %d", n)
	}
}

// Reset clears recorded calls.
func (r *PolicyGate) Reset() {
	r.mu.Lock()
	r.calls = nil
	r.mu.Unlock()
}

// policyDenyError is the sentinel returned by DenyAll when DenyErr is nil.
type policyDenyError struct{ method, arg string }

func (e *policyDenyError) Error() string {
	return "recorder: access denied: " + e.method + " " + e.arg
}

func errDenied(method, arg string) error { return &policyDenyError{method, arg} }
