package channels

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCapWriter(t *testing.T) {
	var buf bytes.Buffer
	if _, err := io.Copy(CapWriter(&buf, 10), strings.NewReader("0123456789")); err != nil {
		t.Fatalf("exactly-at-cap copy failed: %v", err)
	}
	buf.Reset()
	_, err := io.Copy(CapWriter(&buf, 10), strings.NewReader("0123456789X"))
	if !errors.Is(err, ErrFetchTooLarge) {
		t.Fatalf("over-cap err = %v", err)
	}
	if buf.Len() != 10 {
		t.Fatalf("over-cap wrote %d bytes past the sink, want 10", buf.Len())
	}
}
