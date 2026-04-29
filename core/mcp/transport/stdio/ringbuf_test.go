package stdio

import (
	"bytes"
	"strings"
	"testing"
)

func TestRingBuffer_BelowCapacity(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(16)
	n, err := rb.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write -> %d, %v", n, err)
	}
	if got := rb.Snapshot(0); got != "hello" {
		t.Fatalf("Snapshot = %q want hello", got)
	}
	if rb.Len() != 5 {
		t.Fatalf("Len = %d want 5", rb.Len())
	}
}

func TestRingBuffer_WrapsExactlyAtCapacity(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(8)
	rb.Write([]byte("12345678"))
	if got := rb.Snapshot(0); got != "12345678" {
		t.Fatalf("Snapshot = %q", got)
	}
	rb.Write([]byte("ABC"))
	if got := rb.Snapshot(0); got != "45678ABC" {
		t.Fatalf("Snapshot after wrap = %q want 45678ABC", got)
	}
}

func TestRingBuffer_LargeWriteRetainsTail(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(64)
	huge := bytes.Repeat([]byte("a"), 10*1024)
	huge[len(huge)-3] = 'X'
	huge[len(huge)-2] = 'Y'
	huge[len(huge)-1] = 'Z'
	rb.Write(huge)
	if rb.Len() != 64 {
		t.Fatalf("Len after huge write = %d want 64", rb.Len())
	}
	snap := rb.Snapshot(0)
	if !strings.HasSuffix(snap, "XYZ") {
		t.Fatalf("Snapshot suffix = %q want ...XYZ", snap[len(snap)-5:])
	}
}

func TestRingBuffer_SnapshotMaxBytes(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(64)
	rb.Write([]byte("abcdefghij"))
	if got := rb.Snapshot(4); got != "ghij" {
		t.Fatalf("Snapshot(4) = %q want ghij", got)
	}
}

func TestRingBuffer_OccupancyAfter64KiBWrite(t *testing.T) {
	t.Parallel()
	rb := NewRingBuffer(RingBufferSize)
	rb.Write(bytes.Repeat([]byte("z"), RingBufferSize+1024))
	if rb.Len() != RingBufferSize {
		t.Fatalf("Len = %d want %d", rb.Len(), RingBufferSize)
	}
}
