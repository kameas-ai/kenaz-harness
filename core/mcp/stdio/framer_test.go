package stdio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFramer_RoundTrip(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := NewFramer(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out)
	msg, err := f.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Method != "ping" {
		t.Fatalf("method = %q, want ping", msg.Method)
	}
	// EOF after the single line.
	if _, err := f.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("Read after EOF: got %v want io.EOF", err)
	}
	// Write side encodes as a JSON line with newline.
	if err := f.Write(map[string]any{"jsonrpc": "2.0", "id": 2, "result": "ok"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := `{"id":2,"jsonrpc":"2.0","result":"ok"}` + "\n"
	if got := out.String(); got != want {
		t.Fatalf("Write produced %q want %q", got, want)
	}
}

func TestFramer_SkipsMalformedAndEmpty(t *testing.T) {
	t.Parallel()
	body := "" +
		"\n" + // empty line
		"not-json\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	f := NewFramer(strings.NewReader(body), io.Discard)
	// First two reads should be skips.
	for i := 0; i < 2; i++ {
		_, err := f.Read()
		if !IsSkipped(err) {
			t.Fatalf("read %d: got %v want skipped", i, err)
		}
	}
	msg, err := f.Read()
	if err != nil {
		t.Fatalf("Read after skips: %v", err)
	}
	if msg.Method != "notifications/initialized" {
		t.Fatalf("method = %q", msg.Method)
	}
}

func TestFramer_HandlesCRLF(t *testing.T) {
	t.Parallel()
	body := `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\r\n"
	f := NewFramer(strings.NewReader(body), io.Discard)
	msg, err := f.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Method != "ping" {
		t.Fatalf("method = %q", msg.Method)
	}
}

func TestFramer_OversizedLineFails(t *testing.T) {
	t.Parallel()
	// Build a line larger than MaxFrameBytes by padding a JSON
	// string with whitespace inside the value. The total length
	// exceeds the ceiling so bufio.Scanner returns ErrTooLong.
	pad := bytes.Repeat([]byte("a"), MaxFrameBytes+1)
	line := append([]byte(`{"big":"`), pad...)
	line = append(line, []byte(`"}`+"\n")...)
	f := NewFramer(bytes.NewReader(line), io.Discard)
	_, err := f.Read()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Read oversized: got %v want ErrFrameTooLarge", err)
	}
	// Subsequent reads continue to surface the same error rather
	// than blocking — bufio.Scanner is sticky once it errors.
	_, err = f.Read()
	if err == nil {
		t.Fatalf("Read after fatal: got nil; want sticky error")
	}
}

func TestFramer_ConcurrentWritesSerialize(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	f := NewFramer(strings.NewReader(""), &out)
	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func(n int) {
			_ = f.Write(map[string]any{"jsonrpc": "2.0", "id": n})
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 100; i++ {
		<-done
	}
	// Each write produced one newline-terminated line, so we have
	// exactly 100 newlines.
	if got := bytes.Count(out.Bytes(), []byte{'\n'}); got != 100 {
		t.Fatalf("newlines = %d, want 100", got)
	}
}
