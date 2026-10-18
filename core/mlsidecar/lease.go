package mlsidecar

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HeartbeatInterval and StaleAfter are the file-lease protocol's timing
// constants (design §3.5): "renewed on its existing 30s health cadence"
// and the 90s staleness bound that gives a client two missed polls of
// slack (design §3.7 R4: "every /health poll counts as an implicit 90s
// lease" — the health-poll cadence and this staleness window are the
// same numbers for exactly that reason).
const (
	HeartbeatInterval = 30 * time.Second
	StaleAfter        = 90 * time.Second
)

// LeaseFileBody is the JSON content of one client's lease file. Design:
// "your side writes/refreshes/releases leases"; staleness is judged on
// the file's mtime (the heartbeat itself), not on any field inside Body
// — Body only carries enough to explain *why* a lease exists and to run
// the pid-liveness check design §3.7 R5 requires.
type LeaseFileBody struct {
	Client        string    `json:"client"`
	PID           int       `json:"pid"`
	ClientVersion string    `json:"client_version"`
	AcquiredAt    time.Time `json:"acquired_at"`
}

// AcquireOrRenewLease writes (or rewrites) client's lease file. Every
// write advances the file's mtime, which is the entire heartbeat
// mechanism (design: "mtime heartbeats on ~30s cadence").
func AcquireOrRenewLease(l Layout, client string, pid int, clientVersion string) error {
	if client == "" {
		return fmt.Errorf("mlsidecar: lease client id is required")
	}
	if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
		return fmt.Errorf("mlsidecar: mkdir lease dir: %w", err)
	}
	body := LeaseFileBody{Client: client, PID: pid, ClientVersion: clientVersion, AcquiredAt: time.Now()}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("mlsidecar: marshal lease: %w", err)
	}
	path := l.LeaseFile(client)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("mlsidecar: write lease tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mlsidecar: rename lease: %w", err)
	}
	return nil
}

// RenewLease is AcquireOrRenewLease under a name that reads correctly at
// call sites that already hold the lease (manager.go's health-poll
// loop, which piggybacks the renewal on every successful /health call —
// design §3.5).
func RenewLease(l Layout, client string, pid int, clientVersion string) error {
	return AcquireOrRenewLease(l, client, pid, clientVersion)
}

// ReleaseLease removes client's lease file. Missing is not an error —
// releasing an already-released (or never-acquired) lease is a no-op.
func ReleaseLease(l Layout, client string) error {
	err := os.Remove(l.LeaseFile(client))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mlsidecar: release lease: %w", err)
	}
	return nil
}

// LeaseInfo is one parsed, timestamped lease file.
type LeaseInfo struct {
	LeaseFileBody
	ModTime time.Time
}

// ReadLease reads and parses one client's lease file. ok=false (no
// error) when the file does not exist.
func ReadLease(l Layout, client string) (LeaseInfo, bool, error) {
	path := l.LeaseFile(client)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LeaseInfo{}, false, nil
		}
		return LeaseInfo{}, false, fmt.Errorf("mlsidecar: stat lease: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return LeaseInfo{}, false, fmt.Errorf("mlsidecar: read lease: %w", err)
	}
	var body LeaseFileBody
	if err := json.Unmarshal(b, &body); err != nil {
		return LeaseInfo{}, false, fmt.Errorf("mlsidecar: parse lease: %w", err)
	}
	return LeaseInfo{LeaseFileBody: body, ModTime: info.ModTime()}, true, nil
}

// IsStale reports whether li's mtime is older than StaleAfter, judged
// against now (a parameter so tests never depend on wall-clock timing).
func (li LeaseInfo) IsStale(now time.Time) bool {
	return now.Sub(li.ModTime) > StaleAfter
}

// SweepStaleLeases discards every lease file in l.LeaseDir() whose
// holder is either stale-by-mtime OR dead-by-pid (design §3.5/§3.7 R5:
// "a crashed client never pins the sidecar"; "leases are
// pid-liveness-validated at sweep"). isAlive is injected so tests can
// simulate a dead pid without spawning or killing a real process.
// Returns the client ids that were discarded.
func SweepStaleLeases(l Layout, now time.Time, isAlive func(pid int) bool) ([]string, error) {
	entries, err := os.ReadDir(l.LeaseDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mlsidecar: read lease dir: %w", err)
	}
	var removed []string
	for _, e := range entries {
		if e.IsDir() || !isLeaseFileName(e.Name()) {
			continue
		}
		client := e.Name()[:len(e.Name())-len(".lease")]
		li, ok, err := ReadLease(l, client)
		if err != nil || !ok {
			continue
		}
		dead := isAlive != nil && li.PID != 0 && !isAlive(li.PID)
		if li.IsStale(now) || dead {
			if rerr := ReleaseLease(l, client); rerr == nil {
				removed = append(removed, client)
			}
		}
	}
	return removed, nil
}

func isLeaseFileName(name string) bool {
	const suffix = ".lease"
	return len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix
}

// ErrSpawnInProgress is returned by AcquireSpawnLock when another live
// process already holds the lock.
var ErrSpawnInProgress = errors.New("mlsidecar: another process is already spawning the engine")

// SpawnLock is a held O_EXCL spawn lock (design §3.5: "O_EXCL spawn-lock
// in lease/"). A lock left behind by a process that crashed before
// Release is broken by the next AcquireSpawnLock call once its holder
// pid is dead (design §3.7 R5: "spawn is serialized by an O_EXCL lock
// with stale-lock breaking keyed on lock-holder pid liveness").
type SpawnLock struct {
	path string
}

// AcquireSpawnLock attempts to create the spawn lock file exclusively.
// If an existing lock's holder is dead (per isAlive) or unparsable, the
// stale lock is removed and acquisition is retried once. A live holder
// returns ErrSpawnInProgress.
func AcquireSpawnLock(l Layout, pid int, isAlive func(pid int) bool) (*SpawnLock, error) {
	if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
		return nil, fmt.Errorf("mlsidecar: mkdir lease dir: %w", err)
	}
	path := l.SpawnLockFile()
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := f.Write([]byte(strconv.Itoa(pid)))
			cerr := f.Close()
			if werr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("mlsidecar: write spawn lock: %w", werr)
			}
			if cerr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("mlsidecar: close spawn lock: %w", cerr)
			}
			return &SpawnLock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("mlsidecar: create spawn lock: %w", err)
		}
		holder, rerr := os.ReadFile(path)
		if rerr != nil {
			// M1-review F1: the holder can release between our EEXIST and
			// this read — that is contention, not an error. Report it as
			// in-progress so bounded-retry callers keep retrying.
			if os.IsNotExist(rerr) {
				return nil, ErrSpawnInProgress
			}
			return nil, fmt.Errorf("mlsidecar: read existing spawn lock: %w", rerr)
		}
		holderPID, perr := strconv.Atoi(string(holder))
		if perr != nil || isAlive == nil || !isAlive(holderPID) {
			// M1-review F3: unparsable content can be a holder caught in
			// the microseconds between O_EXCL create and the pid write —
			// breaking that lock lets two clients both believe they hold
			// it. Only a lock OLDER than a write could plausibly take is
			// stale; a fresh one is in progress.
			if perr != nil {
				if fi, serr := os.Stat(path); serr == nil && time.Since(fi.ModTime()) < 5*time.Second {
					return nil, ErrSpawnInProgress
				}
			}
			// Stale lock (unparsable content past the grace window, or a
			// dead holder): break it and retry once.
			_ = os.Remove(path)
			continue
		}
		return nil, ErrSpawnInProgress
	}
	return nil, ErrSpawnInProgress
}

// Release removes the spawn lock file. Safe to call on a nil *SpawnLock
// (a failed Acquire returns nil, and defer Release() is convenient at
// call sites regardless of whether acquisition succeeded).
func (s *SpawnLock) Release() error {
	if s == nil {
		return nil
	}
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mlsidecar: release spawn lock: %w", err)
	}
	return nil
}

// WriteLocalToken generates (or rotates) the local shutdown-
// authorization token, user-read-only (design §3.5: "a local token file
// (user-read-only)" authorizing POST /v1/admin/shutdown). Returns the
// generated token.
func WriteLocalToken(l Layout) (string, error) {
	if err := os.MkdirAll(l.LeaseDir(), 0o755); err != nil {
		return "", fmt.Errorf("mlsidecar: mkdir lease dir: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mlsidecar: generate token: %w", err)
	}
	token := hex.EncodeToString(buf)
	if err := os.WriteFile(l.TokenFile(), []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("mlsidecar: write token: %w", err)
	}
	return token, nil
}

// ensureLocalToken returns the shutdown token, writing a fresh one when
// none exists yet. kenaz-ml reads lease/shutdown.token on EVERY
// /v1/admin/shutdown request and never creates it ("written
// user-read-only by the spawning client" — kenaz_ml/lifecycle/shutdown.py),
// so a token written now authorizes a stop of an engine that is already
// running — the recovery path for an engine spawned by a harness build
// that never wrote one (every build before the v0.86.0 unwired sweep).
// An existing token is returned untouched, never rotated: another client
// may hold it.
func ensureLocalToken(l Layout) (string, error) {
	if tok, ok, err := ReadLocalToken(l); err != nil {
		return "", err
	} else if ok && tok != "" {
		return tok, nil
	}
	return WriteLocalToken(l)
}

// ReadLocalToken reads the local shutdown-authorization token. ok=false
// (no error) when no token has been written yet.
func ReadLocalToken(l Layout) (string, bool, error) {
	b, err := os.ReadFile(l.TokenFile())
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("mlsidecar: read token: %w", err)
	}
	// The real engine may write the token with a trailing newline; a
	// Bearer header carrying it would be rejected.
	return strings.TrimSpace(string(b)), true, nil
}
