package mlsidecar

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Lane fallback (owner ruling A5.2, 2026-10-05; bases per A5.3). A fixed
// per-env port cannot be guaranteed free — dev 7775 collided with sigild's
// plugin-ingest listener — and a fixed renumber only moves the collision.
// So each env owns a LANE of LaneCount candidates, base + k*LaneStride
// (k = 0..LaneCount-1): prod 7774, 7784, …, 7814; dev 7785, 7795, …,
// 7825; test 7786, 7796, …, 7826. Each env keeps its own units column, so
// the three lanes never overlap.
//
// The Manager probes the lane with the engine identity check
// (EvaluateAdoption) as the per-candidate arbiter: a listener that fails
// identity is skipped, a free port is spawned on, our own verified engine
// is adopted. The port it settles on is recorded in engine.port.
const (
	LaneStride = 10
	LaneCount  = 5
)

// CandidatePorts is the lane for base, in scan order.
func CandidatePorts(base int) []int {
	out := make([]int, LaneCount)
	for k := range out {
		out[k] = base + k*LaneStride
	}
	return out
}

// inLane reports whether port is one of base's lane candidates.
func inLane(base, port int) bool {
	for _, p := range CandidatePorts(base) {
		if p == port {
			return true
		}
	}
	return false
}

// EnginePortFilename is the lane-port record's name in the install root,
// next to `current` — a CROSS-REPO contract shared with Kenaz (A5.2):
//
//   - content: the port as ASCII decimal followed by exactly one "\n"
//     ("7795\n"), nothing else;
//   - written atomically (temp file in the root + rename) and ONLY after
//     the engine on that port passed the identity check (adoption verdict
//     AdoptAccept);
//   - a reader re-verifies it: garbage (malformed, port 0, out of range)
//     and a port outside the env's lane are logged and ignored as if
//     absent — never dialed, never trusted; the Manager tries an in-lane
//     record FIRST and re-runs the identity check against it on every
//     Ensure — a dead or foreign listener makes it stale, the lane scan
//     reruns, and the file is rewritten when the scan adopts or spawns.
//     It is never written (nor removed) when every candidate is foreign;
//   - absent (ok=false, no error): scan from the base.
//
// engine.port is cross-client DISCOVERY only (review F2, 2026-10-05): it
// is the scan's first candidate, nothing more. No request is ever routed
// by reading it — a same-user writer could otherwise redirect advice,
// label and shutdown-token traffic to any in-lane port. Dial paths use the
// Manager's in-memory verified port (Manager.DialClient), set only after
// the identity check passed on that port.
const EnginePortFilename = "engine.port"

// EnginePortFile is engine.port's location.
func (l Layout) EnginePortFile() string { return filepath.Join(l.Root, EnginePortFilename) }

// ReadEnginePort parses engine.port strictly ("<decimal>\n", 1..65535).
// An ABSENT file is (0, false, nil) — not an error. An unreadable or
// malformed file (including port 0) is (0, false, err): garbage, which
// callers log and then treat exactly as absent.
func ReadEnginePort(l Layout) (int, bool, error) {
	b, err := os.ReadFile(l.EnginePortFile())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("mlsidecar: engine.port: %w", err)
	}
	if len(b) < 2 || len(b) > 6 || b[len(b)-1] != '\n' {
		return 0, false, fmt.Errorf("mlsidecar: engine.port: malformed content %q (want \"<port>\\n\")", b)
	}
	digits := b[:len(b)-1]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false, fmt.Errorf("mlsidecar: engine.port: malformed content %q (want \"<port>\\n\")", b)
		}
	}
	p, err := strconv.Atoi(string(digits))
	if err != nil || p < 1 || p > 65535 {
		return 0, false, fmt.Errorf("mlsidecar: engine.port: port %q out of range 1..65535", digits)
	}
	return p, true, nil
}

// RecordedEnginePort is ReadEnginePort narrowed to base's lane. A record
// naming a port outside the lane is an error (it cannot be an engine this
// env's clients spawned or adopted): never dialed, never trusted —
// callers log it and treat it as absent.
func RecordedEnginePort(l Layout, base int) (int, bool, error) {
	p, ok, err := ReadEnginePort(l)
	if !ok {
		return 0, false, err
	}
	if !inLane(base, p) {
		return 0, false, fmt.Errorf("mlsidecar: engine.port: port %d is outside the lane %v; ignored", p, CandidatePorts(base))
	}
	return p, true, nil
}

// WriteEnginePort records port atomically: temp file in the root, then
// rename over engine.port. Callers write it only for a port whose engine
// passed the identity check.
func WriteEnginePort(l Layout, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("mlsidecar: engine.port: invalid port %d", port)
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return fmt.Errorf("mlsidecar: engine.port: mkdir root: %w", err)
	}
	tmp, err := os.CreateTemp(l.Root, EnginePortFilename+".tmp-*")
	if err != nil {
		return fmt.Errorf("mlsidecar: engine.port: temp file: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(strconv.Itoa(port) + "\n"); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("mlsidecar: engine.port: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("mlsidecar: engine.port: close: %w", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("mlsidecar: engine.port: chmod: %w", err)
	}
	if err := os.Rename(name, l.EnginePortFile()); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("mlsidecar: engine.port: rename: %w", err)
	}
	return nil
}

// RemoveEnginePort deletes engine.port (absent is not an error).
func RemoveEnginePort(l Layout) error {
	if err := os.Remove(l.EnginePortFile()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
