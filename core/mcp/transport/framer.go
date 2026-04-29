package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MaxFrameBytes caps a single inbound JSON-RPC frame at 4 MiB. Per
// research §"Newline-delimited JSON-RPC 2.0 framing on stdio" this
// matches the line ceiling chosen for the MCP stdio transport in
// reference implementations and protects the harness from a
// runaway/stuck server filling memory.
const MaxFrameBytes = 4 * 1024 * 1024

// errSkipped is the sentinel returned by Framer.Read when the
// inbound line is not parseable JSON. The reader loop continues on
// this error rather than terminating the connection — empty lines
// and stderr-style banners that landed on stdout are tolerated.
var errSkipped = errors.New("transport: skipped non-JSON line")

// IsSkipped reports whether err is the framer's "skip this line"
// sentinel. Tests use this to assert the reader survives malformed
// input without bringing the connection down.
func IsSkipped(err error) bool { return errors.Is(err, errSkipped) }

// ErrFrameTooLarge is returned when a single line exceeds
// MaxFrameBytes. Distinct from errSkipped: the reader treats this
// as fatal and exits the loop, matching the spec's expectation that
// frame-size violations are transport errors.
var ErrFrameTooLarge = errors.New("transport: frame exceeds 4 MiB ceiling")

// Framer reads and writes newline-delimited JSON-RPC 2.0 messages
// over an arbitrary io.Reader/io.Writer pair. Read returns one
// envelope per call; the caller decides whether each envelope is a
// request (id != nil + method set), a response (id != nil + method
// empty), or a notification (id nil + method set).
//
// Read and Write are independently safe for concurrent use with
// themselves; concurrent Read+Write is the expected usage. Multiple
// concurrent Reads are NOT safe — exactly one goroutine should own
// the read side per Framer instance.
type Framer struct {
	scanner *bufio.Scanner

	writeMu sync.Mutex
	w       io.Writer
}

// NewFramer wires a Framer onto the given reader/writer. The reader
// is wrapped in a bufio.Scanner with the buffer raised to
// MaxFrameBytes; lines exceeding that ceiling produce
// ErrFrameTooLarge.
func NewFramer(r io.Reader, w io.Writer) *Framer {
	s := bufio.NewScanner(r)
	// Initial buffer is small but the scanner grows it up to
	// MaxFrameBytes as needed. bufio.Scanner allocates the buffer
	// lazily on first Scan, so the cost is paid once per Framer.
	s.Buffer(make([]byte, 0, 64*1024), MaxFrameBytes)
	s.Split(splitNewlinesStripCR)
	return &Framer{scanner: s, w: w}
}

// Read returns the next envelope on the reader. A non-JSON line
// returns errSkipped (use IsSkipped to detect — the caller should
// `continue` rather than terminate). io.EOF surfaces verbatim.
// ErrFrameTooLarge is fatal: the underlying scanner has already
// stopped and Read will keep returning the same error.
func (f *Framer) Read() (RawMessage, error) {
	if !f.scanner.Scan() {
		err := f.scanner.Err()
		if err == nil {
			return RawMessage{}, io.EOF
		}
		// bufio.Scanner returns ErrTooLong when a line exceeds the
		// buffer ceiling. Re-wrap as our public sentinel so callers
		// can errors.Is without depending on bufio internals.
		if errors.Is(err, bufio.ErrTooLong) {
			return RawMessage{}, ErrFrameTooLarge
		}
		return RawMessage{}, err
	}
	line := f.scanner.Bytes()
	if len(line) == 0 {
		return RawMessage{}, errSkipped
	}
	var msg RawMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return RawMessage{}, fmt.Errorf("%w: %v", errSkipped, err)
	}
	return msg, nil
}

// Write encodes v as a single JSON line on the writer. Safe for
// concurrent use. The encoder marshals into a local buffer then
// writes "<json>\n" in a single Write call, which keeps the line
// atomic on POSIX pipes (atomic up to PIPE_BUF). Letting
// json.Encoder write the newline separately would split the line
// into two pipe writes — fine for a single producer, but two
// concurrent Encoders against the same writer could interleave
// JSON+newline pairs, garbling lines on the receiving end.
func (f *Framer) Write(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, err := f.w.Write(buf.Bytes())
	return err
}

// splitNewlinesStripCR is a bufio.SplitFunc that splits on '\n' and
// strips a single trailing '\r' so CRLF-terminated lines parse the
// same as LF-terminated ones. Mirrors bufio.ScanLines but keeps
// returning empty advances at EOF rather than swallowing trailing
// data.
func splitNewlinesStripCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			line := data[:i]
			if n := len(line); n > 0 && line[n-1] == '\r' {
				line = line[:n-1]
			}
			return i + 1, line, nil
		}
	}
	if atEOF {
		// Final line without a trailing newline — return what we have.
		line := data
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		return len(data), line, nil
	}
	return 0, nil, nil
}
