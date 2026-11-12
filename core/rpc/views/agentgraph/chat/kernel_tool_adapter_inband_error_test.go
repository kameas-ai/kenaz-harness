package chat

import (
	"context"
	"testing"
)

// fixedResultPool returns the same raw result for every call.
type fixedResultPool struct{ out []byte }

func (p fixedResultPool) Tools(context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: "kenaz", Name: "write_file"}}, nil
}

func (p fixedResultPool) Call(context.Context, string, string, []byte) ([]byte, error) {
	return p.out, nil
}

// TestKernelToolAdapter_InBandErrorIsAFailedCall pins fleet e2e
// 2026-11-07 E2E-2: kenaz__write_file reports a failed mkdir in-band
// ({"is_error":true,...}) with a nil Go error, and the adapter used to
// record that as a SUCCESSFUL tool call — the associate saw "OK" for
// files that were never written.
func TestKernelToolAdapter_InBandErrorIsAFailedCall(t *testing.T) {
	t.Parallel()
	failed := fixedResultPool{out: []byte(`{"is_error":true,"error":"write_file: mkdir: mkdir /home/kenaz: permission denied"}`)}
	res, err := newKernelToolAdapter(failed, nil, "sess-1").Call(context.Background(), makeCall("kenaz", "write_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false for an in-band tool failure; content = %q", res.Content)
	}

	ok := fixedResultPool{out: []byte(`{"written":3,"path":"/tmp/x"}`)}
	res, err = newKernelToolAdapter(ok, nil, "sess-1").Call(context.Background(), makeCall("kenaz", "write_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true for a successful write; content = %q", res.Content)
	}
}
