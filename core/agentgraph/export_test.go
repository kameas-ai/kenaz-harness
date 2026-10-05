package agentgraph

import "testing"

// SetRunSpecByteCapForTest lowers the stored-spec bound for one test and
// restores it on cleanup. Callers must not be t.Parallel(): the cap is
// package state read by every RecordRunSpec.
func SetRunSpecByteCapForTest(t *testing.T, n int) {
	t.Helper()
	prev := runSpecByteCap
	runSpecByteCap = n
	t.Cleanup(func() { runSpecByteCap = prev })
}
