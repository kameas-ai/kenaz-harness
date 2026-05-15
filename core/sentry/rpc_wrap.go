package sentry

// WrapBinding is a helper that defers RecoverBinding with the given name.
// The idiomatic usage is:
//
//	func (b *Bindings) Foo_Bar() (T, error) {
//	    defer sentry.WrapBinding("Foo_Bar")()
//	    // ...
//	}
//
// Note the double call: WrapBinding returns a func() which must be called
// via defer. This pattern is equivalent to calling RecoverBinding directly
// at the top of the method but allows passing the name as a variable when
// generating binding shims.
//
// wire-up point 3 for sentry: core/rpc/bindings.go calls RecoverBinding
// at the top of every binding method (sentry-error-monitoring-01KX5R8G WP03).
func WrapBinding(name string) func() {
	return func() { RecoverBinding(name) }
}
