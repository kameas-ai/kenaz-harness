// Package lockfile implements the single-writer process lock for the
// harness data directory (FR-011). On Open, storage takes an exclusive
// flock on <DataDir>/data.db.harness-lock and writes its PID and start
// time into the file. A second harness process attempting to Open the
// same data dir gets ErrAlreadyLocked carrying the holder's PID and
// start time when readable.
package lockfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrAlreadyLocked is returned by Acquire when another process holds
// the lock.
var ErrAlreadyLocked = errors.New("lockfile: data dir already locked by another harness process")

// HolderInfo describes the process that currently owns a lock. Returned
// inside the wrapped error from Acquire.
type HolderInfo struct {
	PID       int    `json:"pid"`
	Hostname  string `json:"hostname"`
	StartedAt string `json:"started_at"` // RFC3339Nano UTC
}

// HolderError is wrapped by ErrAlreadyLocked when the holder's metadata
// is readable.
type HolderError struct {
	HolderInfo
}

func (h *HolderError) Error() string {
	return fmt.Sprintf("lockfile: held by pid %d on host %q since %s",
		h.PID, h.Hostname, h.StartedAt)
}

// Handle is an acquired lock. Call Release to drop it.
type Handle struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	released bool
}

// Acquire opens (creating if needed) the file at lockPath and takes an
// exclusive non-blocking flock. On success, the holder metadata is
// written to the file. On contention, ErrAlreadyLocked is returned with
// a wrapped *HolderError when the holder's info can be read.
func Acquire(lockPath string) (*Handle, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("lockfile: mkdir parent: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lockfile: open %s: %w", lockPath, err)
	}
	if err := flockExNB(f); err != nil {
		// Try to read the holder info before failing.
		holder := readHolderInfo(f)
		_ = f.Close()
		if holder != nil {
			return nil, fmt.Errorf("%w: %v", ErrAlreadyLocked, &HolderError{*holder})
		}
		return nil, fmt.Errorf("%w (no holder info)", ErrAlreadyLocked)
	}
	// Write our holder info.
	hostname, _ := os.Hostname()
	info := HolderInfo{
		PID:       os.Getpid(),
		Hostname:  hostname,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeHolderInfo(f, info); err != nil {
		_ = flockUn(f)
		_ = f.Close()
		return nil, fmt.Errorf("lockfile: write holder: %w", err)
	}
	return &Handle{path: lockPath, file: f}, nil
}

// Release drops the lock and closes the file. Idempotent.
func (h *Handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return nil
	}
	h.released = true
	if h.file == nil {
		return nil
	}
	_ = flockUn(h.file)
	return h.file.Close()
}

// Path returns the lockfile path.
func (h *Handle) Path() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.path
}

func writeHolderInfo(f *os.File, info HolderInfo) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(info); err != nil {
		return err
	}
	return f.Sync()
}

func readHolderInfo(f *os.File) *HolderInfo {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	var info HolderInfo
	dec := json.NewDecoder(f)
	if err := dec.Decode(&info); err != nil {
		return nil
	}
	if info.PID == 0 {
		return nil
	}
	return &info
}

// flockExNB / flockUn are implemented in lock_unix.go and lock_windows.go.
