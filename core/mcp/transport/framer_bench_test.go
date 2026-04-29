package transport

import (
	"bytes"
	"io"
	"testing"
)

// BenchmarkFramer_Write measures the marshalling + single-write
// path, which is the hot path for outbound JSON-RPC messages on
// every transport.
func BenchmarkFramer_Write(b *testing.B) {
	var sink bytes.Buffer
	f := NewFramer(bytes.NewReader(nil), &sink)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "fake_echo",
			"arguments": map[string]any{"hello": "world"},
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.Reset()
		_ = f.Write(payload)
	}
}

// BenchmarkFramer_Read measures the JSON parse + envelope dispatch
// for inbound responses — the hot path on the read side.
func BenchmarkFramer_Read(b *testing.B) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}` + "\n")
	rdr := bytes.NewReader(nil)
	f := NewFramer(rdr, io.Discard)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rdr.Reset(line)
		// Re-construct the framer's scanner against the new buffer.
		f = NewFramer(rdr, io.Discard)
		_, _ = f.Read()
	}
}

// BenchmarkRingBuffer_Write covers the stderr-pump hot path. The
// number reported is per-call (one Write of 256 bytes).
func BenchmarkRingBuffer_Write(b *testing.B) {
	rb := NewRingBuffer(RingBufferSize)
	chunk := bytes.Repeat([]byte{'x'}, 256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rb.Write(chunk)
	}
}

// BenchmarkRouter_RegisterDeliver covers one full request lifecycle
// in the response router — the central hot path for callRaw.
func BenchmarkRouter_RegisterDeliver(b *testing.B) {
	r := NewResponseRouter(nil)
	env := RawMessage{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := r.Register(int64(i))
		_ = r.Deliver(int64(i), env)
		<-ch
		r.Cancel(int64(i))
	}
}
