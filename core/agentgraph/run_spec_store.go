package agentgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// RunSpecStore persists the exact resolved spec each run executed,
// keyed by run id (feat/graph-resolved-spec WP01 — the follow-up
// agentgraph-settings-linkage-01DOGF0D §5 deferred).
//
// "Resolved" means the Graph the kernel was handed in env.Graph: kind
// aliases already rewritten by the load path, the chat routing gate and
// max-turns dial already applied. That is the only spec that describes
// the run; the library file it came from can be edited afterwards.
//
// It is the optional second half of an EventLog: the kernel and the
// agentgraph view Manager share one EventLog instance, so a log that
// also implements RunSpecStore is how the kernel's write reaches the
// Manager's read without a second seam to wire. Both shipped logs
// (SQLEventLog, NewMemoryEventLog) implement it.
type RunSpecStore interface {
	// RecordRunSpec stores g as the spec runID executed. Insert-once:
	// a second call for the same run id (Kernel.Resume, the chat
	// overflow redrive — both re-enter Run with the same spec) keeps the
	// first row and returns nil. A spec whose encoding exceeds
	// MaxRunSpecBytes is refused with ErrRunSpecTooLarge.
	RecordRunSpec(runID string, g Graph) error
	// LoadRunSpec returns the stored spec. found is false, with a nil
	// error, when no spec was recorded for the run (runs from before
	// the store existed, or a refused oversized spec). A stored spec
	// that no longer decodes to the digest it was written with is an
	// error, never a silent miss.
	LoadRunSpec(runID string) (g Graph, found bool, err error)
}

// MaxRunSpecBytes bounds one stored spec's JSON encoding. Measured on
// the shipped library (2026-10-05): chat_default encodes to ~5.0 KiB
// (5028 bytes; ~2.7 KiB resolved with the routing gate off),
// toolloop_default to ~4.2 KiB (4259 bytes). 1 MiB leaves two orders of
// magnitude of room for large authored graphs while keeping a
// pathological spec (a multi-megabyte inlined prompt) from bloating the
// database once per run. A spec over the bound is not stored and its
// run view says it is a reconstruction — the run itself is unaffected.
const MaxRunSpecBytes = 1 << 20

// runSpecByteCap is the bound encodeRunSpec enforces: MaxRunSpecBytes,
// lowered only by tests (export_test.go) so the oversize degrade can be
// driven end-to-end without a megabyte fixture.
var runSpecByteCap = MaxRunSpecBytes

// ErrRunSpecTooLarge is RecordRunSpec's refusal for a spec over
// MaxRunSpecBytes.
var ErrRunSpecTooLarge = errors.New("agentgraph: resolved spec exceeds MaxRunSpecBytes; not stored")

// recordRunSpec is the kernel's write: when the run's EventLog carries
// the RunSpecStore half, store env.Graph against env.RunID. A refusal or
// store error is logged, never fatal — the stored spec is observability
// (what the run view shows), the run is the product; such a run's view
// is labelled a reconstruction, exactly as a run from before the store.
func (k *Kernel) recordRunSpec(env *Env) {
	store, ok := k.log.(RunSpecStore)
	if !ok || env.RunID == "" {
		return
	}
	if err := store.RecordRunSpec(env.RunID, *env.Graph); err != nil {
		logging.L().Warn("agentgraph.run_spec.record_failed",
			"run_id", env.RunID,
			"graph_id", env.Graph.ID,
			"err", err.Error(),
		)
	}
}

// encodeRunSpec is the one encoding both stores use, so the bound and
// the digest mean the same thing in RAM and on disk.
func encodeRunSpec(runID string, g Graph) (raw []byte, digest string, err error) {
	if runID == "" {
		return nil, "", errors.New("agentgraph: run spec: run id required")
	}
	raw, err = DumpJSON(g)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > runSpecByteCap {
		return nil, "", fmt.Errorf("%w (run %s: %d bytes)", ErrRunSpecTooLarge, runID, len(raw))
	}
	return raw, SpecDigest(g), nil
}

// decodeRunSpec reverses encodeRunSpec and checks the digest recorded
// alongside it.
func decodeRunSpec(runID string, raw []byte, digest string) (Graph, error) {
	g, err := LoadJSON(raw)
	if err != nil {
		return Graph{}, fmt.Errorf("agentgraph: stored spec for run %s: %w", runID, err)
	}
	if got := SpecDigest(g); digest != "" && got != digest {
		return Graph{}, fmt.Errorf("agentgraph: stored spec for run %s does not match its recorded digest (%s, decodes to %s)", runID, digest, got)
	}
	return g, nil
}

// RecordRunSpec implements RunSpecStore over agent_graph_run_specs
// (migration sessions/0343).
func (l *SQLEventLog) RecordRunSpec(runID string, g Graph) error {
	raw, digest, err := encodeRunSpec(runID, g)
	if err != nil {
		return err
	}
	if _, err := l.db.ExecContext(context.Background(),
		`INSERT INTO agent_graph_run_specs
		    (run_id, graph_id, spec_digest, spec_json, created_at_ns)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(run_id) DO NOTHING`,
		runID, g.ID, digest, string(raw), time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("agentgraph: sql run spec: insert: %w", err)
	}
	return nil
}

// LoadRunSpec implements RunSpecStore over agent_graph_run_specs.
func (l *SQLEventLog) LoadRunSpec(runID string) (Graph, bool, error) {
	var raw, digest string
	err := l.db.QueryRowContext(context.Background(),
		`SELECT spec_json, spec_digest FROM agent_graph_run_specs WHERE run_id = ?`,
		runID).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return Graph{}, false, nil
	}
	if err != nil {
		return Graph{}, false, fmt.Errorf("agentgraph: sql run spec: select: %w", err)
	}
	g, err := decodeRunSpec(runID, []byte(raw), digest)
	if err != nil {
		return Graph{}, false, err
	}
	return g, true, nil
}

type memRunSpec struct {
	raw    []byte
	digest string
}

// RecordRunSpec implements RunSpecStore for the in-memory log. Stored
// encoded, like the SQL log, so both return a decoded copy the caller
// cannot alias into the running graph.
func (l *memEventLog) RecordRunSpec(runID string, g Graph) error {
	raw, digest, err := encodeRunSpec(runID, g)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.specs == nil {
		l.specs = make(map[string]memRunSpec)
	}
	if _, exists := l.specs[runID]; !exists {
		l.specs[runID] = memRunSpec{raw: raw, digest: digest}
	}
	return nil
}

// LoadRunSpec implements RunSpecStore for the in-memory log.
func (l *memEventLog) LoadRunSpec(runID string) (Graph, bool, error) {
	l.mu.Lock()
	s, ok := l.specs[runID]
	l.mu.Unlock()
	if !ok {
		return Graph{}, false, nil
	}
	g, err := decodeRunSpec(runID, s.raw, s.digest)
	if err != nil {
		return Graph{}, false, err
	}
	return g, true, nil
}

// Compile-time witnesses: both shipped logs carry the spec half.
var (
	_ RunSpecStore = (*SQLEventLog)(nil)
	_ RunSpecStore = (*memEventLog)(nil)
)
