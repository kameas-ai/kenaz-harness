// Package auditsinktest is a fake reporter collector socket for tests of
// code that writes the harness audit line-protocol (core/auditsink).
package auditsinktest

import (
	"bufio"
	"encoding/json"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/auditsink"
)

// Socket listens on loopback TCP (pair it with auditsink.New(addr, "tcp",
// …)) and collects every record written to it. The sink dials one
// connection per record, synchronously, so accept order is emit order;
// Records returns them in that order regardless of handler scheduling.
type Socket struct {
	ln   net.Listener
	mu   sync.Mutex
	recs []seqRecord
}

type seqRecord struct {
	seq int
	rec auditsink.Record
}

// New starts a Socket and closes it when the test ends.
func New(t *testing.T) *Socket {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("auditsinktest: listen: %v", err)
	}
	s := &Socket{ln: ln}
	go func() {
		seq := 0
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			seq++
			go s.handle(conn, seq)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *Socket) handle(conn net.Conn, seq int) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var rec auditsink.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		s.mu.Lock()
		s.recs = append(s.recs, seqRecord{seq: seq, rec: rec})
		s.mu.Unlock()
	}
}

// Addr is the address to hand auditsink.New with network "tcp".
func (s *Socket) Addr() string { return s.ln.Addr().String() }

// Records returns every record received so far, in emit order.
func (s *Socket) Records() []auditsink.Record {
	s.mu.Lock()
	cp := append([]seqRecord(nil), s.recs...)
	s.mu.Unlock()
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].seq < cp[j].seq })
	out := make([]auditsink.Record, len(cp))
	for i, r := range cp {
		out[i] = r.rec
	}
	return out
}

// WaitFor polls until match returns true for the records received so far,
// failing the test after timeout.
func (s *Socket) WaitFor(t *testing.T, timeout time.Duration, match func([]auditsink.Record) bool) []auditsink.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		recs := s.Records()
		if match(recs) {
			return recs
		}
		if time.Now().After(deadline) {
			t.Fatalf("auditsinktest: timed out; have %d records: %+v", len(recs), recs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
