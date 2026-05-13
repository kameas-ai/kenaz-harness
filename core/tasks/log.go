package tasks

import (
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	// maxLogBytes is the maximum size of a task log file before rotation.
	maxLogBytes = 100 * 1024 * 1024 // 100 MiB

	// logRetentionDays is how long rotated archives are kept.
	// (Rotation itself happens at harness shutdown — see recovery.go.)
	logRetentionDays = 7
)

// openLogFile opens (creates) a per-task append-only log file at
// <logDir>/<taskID>.log. Returns nil, nil when logDir is empty.
//
// The returned io.WriteCloser is never nil when err is nil.
func openLogFile(logDir, taskID string) (io.WriteCloser, error) {
	if logDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(logDir, taskID+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return newRotatingWriter(f, path, maxLogBytes), nil
}

// rotatingWriter wraps an os.File and rotates when the file hits sizeLimit.
// Rotation: rename <name> → <name>.1, open new <name>. Old .1 is
// overwritten. File deletion for archives older than logRetentionDays is
// handled by the shutdown path in recovery.go.
type rotatingWriter struct {
	mu        sync.Mutex
	f         *os.File
	path      string
	sizeLimit int64
	written   int64
}

func newRotatingWriter(f *os.File, path string, sizeLimit int64) *rotatingWriter {
	// Probe current size.
	var n int64
	if fi, err := f.Stat(); err == nil {
		n = fi.Size()
	}
	return &rotatingWriter{f: f, path: path, sizeLimit: sizeLimit, written: n}
}

func (rw *rotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.written+int64(len(p)) >= rw.sizeLimit {
		rw.rotate()
	}
	n, err := rw.f.Write(p)
	rw.written += int64(n)
	return n, err
}

func (rw *rotatingWriter) rotate() {
	_ = rw.f.Close()
	archive := rw.path + ".1"
	_ = os.Rename(rw.path, archive) // best-effort
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		// Best-effort: keep old file handle open.
		f2, _ := os.OpenFile(rw.path, os.O_APPEND|os.O_WRONLY, 0o600)
		if f2 != nil {
			rw.f = f2
		}
		return
	}
	rw.f = f
	rw.written = 0
}

func (rw *rotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.f.Close()
}
