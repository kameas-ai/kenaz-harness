package recipes

// ResolveHarnessExeForTest swaps the resolveHarnessExe function var for
// the supplied stub and returns the previous value so callers can restore
// it with defer. Used only in unit tests.
func ResolveHarnessExeForTest(stub func() (string, error)) func() (string, error) {
	prev := resolveHarnessExe
	resolveHarnessExe = stub
	return prev
}
