// Package telemetry hosts the OpenTelemetry SDK wiring for the
// harness — Init builds the TracerProvider, MeterProvider, and
// LoggerProvider, plugs in the local SQLite-backed exporters under
// migration 0305, and exposes the persistent service.instance.id ULID
// that every span / metric / log resource carries.
//
// WP01 lands the SDK init + local exporters + instance.id surface.
// WP02 plugs the slog → OTel log bridge and adds the OTLP HTTP
// exporters; WP03 lands retention sweep + per-span attribute
// allowlist; WP04 instruments the chat / tool / hook / mcp hot paths.
//
// Telemetry init failure must NEVER block harness boot. core.New /
// core.Start log the error at warn and continue without telemetry —
// the chat surface is untouched. The exporters themselves use
// bounded buffers + drop-on-full + a counter (NFR-003) so a slow DB
// can't backpressure the producer either.
package telemetry

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// instanceFileName is the leaf used under <DataDir>/telemetry/. The
// ULID stored at this path is the durable service.instance.id every
// resource attaches to its emitted spans / metrics / logs (FR-002).
// The file lives next to data.db so it shares the data-dir lifecycle:
// wipe the dir → fresh ULID; preserve the dir → stable ULID across
// process restarts.
const instanceFileName = "instance.id"

// instanceDirName is the parent dir under DataDir.
const instanceDirName = "telemetry"

// ulidPattern is the Crockford-base32 ULID shape: 26 chars, the
// alphabet excludes I, L, O, U.
var ulidPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// EnsureInstanceID returns the persistent service.instance.id ULID
// stored at <dataDir>/telemetry/instance.id. The parent dir is
// created on first call. A corrupt or unreadable file is replaced
// with a freshly minted ULID; this keeps the harness alive when an
// out-of-band actor truncates the file and avoids failing telemetry
// init mid-flight (NFR-003 spirit).
//
// Returns the ULID + nil on success. Returns "" + err only when the
// filesystem itself fails — i.e. the parent dir cannot be created or
// the atomic-write rename can't land.
func EnsureInstanceID(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("telemetry: EnsureInstanceID: dataDir empty")
	}
	dir := filepath.Join(dataDir, instanceDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("telemetry: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, instanceFileName)

	if data, err := os.ReadFile(path); err == nil {
		// Trim any trailing whitespace from manual edits and validate
		// the shape. Anything else triggers a regeneration.
		got := trimASCIISpace(data)
		if ulidPattern.MatchString(got) {
			return got, nil
		}
		// Fall through to regenerate.
	}

	id, err := newULID()
	if err != nil {
		return "", fmt.Errorf("telemetry: mint ULID: %w", err)
	}
	if err := atomicWrite(path, []byte(id)); err != nil {
		return "", fmt.Errorf("telemetry: write %s: %w", path, err)
	}
	return id, nil
}

// atomicWrite writes data to path through a tmp file + rename. The
// parent dir is fsynced so the rename survives a crash on most
// filesystems. tmp lives next to the target so the rename stays on
// the same filesystem.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".instance-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// trimASCIISpace trims leading/trailing ASCII whitespace.
func trimASCIISpace(b []byte) string {
	start := 0
	for start < len(b) {
		c := b[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	end := len(b)
	for end > start {
		c := b[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return string(b[start:end])
}

// ── ULID generator ─────────────────────────────────────────────────

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu     sync.Mutex
	ulidLastMs uint64
	ulidTail   [10]byte
)

// newULID returns a fresh 26-char Crockford-base32 ULID. Same shape
// as the artifacts package's generator so any future consumer can
// validate an ID without caring which subsystem minted it.
func newULID() (string, error) {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	ms := uint64(time.Now().UnixMilli())
	if ms < ulidLastMs {
		ms = ulidLastMs
	}
	if ms == ulidLastMs {
		// Increment the tail to preserve monotonicity within the same
		// millisecond. This rarely matters for instance.id (one mint
		// per install) but keeps the helper reusable.
		for i := len(ulidTail) - 1; i >= 0; i-- {
			ulidTail[i]++
			if ulidTail[i] != 0 {
				break
			}
			if i == 0 {
				return "", errors.New("telemetry: ULID tail overflow")
			}
		}
	} else {
		var t [10]byte
		if _, err := rand.Read(t[:]); err != nil {
			return "", err
		}
		ulidTail = t
		ulidLastMs = ms
	}
	return encodeULID(ulidLastMs, ulidTail), nil
}

func encodeULID(ms uint64, tail [10]byte) string {
	var raw [16]byte
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	copy(raw[6:], tail[:])

	out := make([]byte, 26)
	out[0] = crockfordAlphabet[(raw[0]>>5)&0x07]
	out[1] = crockfordAlphabet[raw[0]&0x1f]
	out[2] = crockfordAlphabet[(raw[1]>>3)&0x1f]
	out[3] = crockfordAlphabet[((raw[1]&0x07)<<2)|((raw[2]>>6)&0x03)]
	out[4] = crockfordAlphabet[(raw[2]>>1)&0x1f]
	out[5] = crockfordAlphabet[((raw[2]&0x01)<<4)|((raw[3]>>4)&0x0f)]
	out[6] = crockfordAlphabet[((raw[3]&0x0f)<<1)|((raw[4]>>7)&0x01)]
	out[7] = crockfordAlphabet[(raw[4]>>2)&0x1f]
	out[8] = crockfordAlphabet[((raw[4]&0x03)<<3)|((raw[5]>>5)&0x07)]
	out[9] = crockfordAlphabet[raw[5]&0x1f]
	out[10] = crockfordAlphabet[(raw[6]>>3)&0x1f]
	out[11] = crockfordAlphabet[((raw[6]&0x07)<<2)|((raw[7]>>6)&0x03)]
	out[12] = crockfordAlphabet[(raw[7]>>1)&0x1f]
	out[13] = crockfordAlphabet[((raw[7]&0x01)<<4)|((raw[8]>>4)&0x0f)]
	out[14] = crockfordAlphabet[((raw[8]&0x0f)<<1)|((raw[9]>>7)&0x01)]
	out[15] = crockfordAlphabet[(raw[9]>>2)&0x1f]
	out[16] = crockfordAlphabet[((raw[9]&0x03)<<3)|((raw[10]>>5)&0x07)]
	out[17] = crockfordAlphabet[raw[10]&0x1f]
	out[18] = crockfordAlphabet[(raw[11]>>3)&0x1f]
	out[19] = crockfordAlphabet[((raw[11]&0x07)<<2)|((raw[12]>>6)&0x03)]
	out[20] = crockfordAlphabet[(raw[12]>>1)&0x1f]
	out[21] = crockfordAlphabet[((raw[12]&0x01)<<4)|((raw[13]>>4)&0x0f)]
	out[22] = crockfordAlphabet[((raw[13]&0x0f)<<1)|((raw[14]>>7)&0x01)]
	out[23] = crockfordAlphabet[(raw[14]>>2)&0x1f]
	out[24] = crockfordAlphabet[((raw[14]&0x03)<<3)|((raw[15]>>5)&0x07)]
	out[25] = crockfordAlphabet[raw[15]&0x1f]
	return string(out)
}
