// rerun.go implements the rerun_policy resolver and the small
// workflow_runs_cache helper that backs it.
//
// A workflow can declare `rerun_policy: <name>` to control whether a
// re-invocation with byte-identical inputs reuses the prior run's
// output instead of dispatching all the steps again. WP08 supports
// three policies (with WP01 alias names retained for back-compat):
//
//   - "always" / "fresh"   — never consult cache; always re-run.
//   - "skip"   / "continue"— if cached, return the cached RunResult.
//   - "prompt" / "ask"     — if cached, return ErrRerunPolicyAsk so
//                            the caller can prompt the user.
//
// The cache itself is a single SQLite table (workflow_runs_cache,
// landed by migration 0320). The Cache interface lets tests drop in
// a memory implementation without standing up a DB.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
)

// RerunPolicy is the closed enum of cache-consult behaviours.
type RerunPolicy string

const (
	// RerunPolicyAlways always re-runs (cache is ignored on read; the
	// fresh result still overwrites the cached row on completion).
	RerunPolicyAlways RerunPolicy = "always"
	// RerunPolicySkip returns the cached RunResult when one exists for
	// the same (workflow_id, inputs_hash) tuple.
	RerunPolicySkip RerunPolicy = "skip"
	// RerunPolicyPrompt returns ErrRerunPolicyAsk when a cached row
	// exists so the caller can ask the user before reusing.
	RerunPolicyPrompt RerunPolicy = "prompt"
)

// NormalizeRerunPolicy maps the WP01 alias names ("fresh", "continue",
// "ask") onto the WP08 canonical vocabulary. An empty / unknown input
// returns RerunPolicyAlways — the conservative default that preserves
// pre-WP08 behaviour (every Run dispatches every step).
func NormalizeRerunPolicy(p string) RerunPolicy {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "always", "fresh":
		return RerunPolicyAlways
	case "skip", "continue":
		return RerunPolicySkip
	case "prompt", "ask":
		return RerunPolicyPrompt
	default:
		return RerunPolicyAlways
	}
}

// RunResult is the cacheable summary of a completed workflow run.
// It mirrors the engine's *Run shape but is tagged for the cache:
// CachedAt > 0 means the value was loaded from cache rather than
// freshly computed.
type RunResult struct {
	RunID       string                `json:"runId"`
	WorkflowID  string                `json:"workflowId"`
	Status      string                `json:"status"`
	Steps       []StepResult          `json:"steps"`
	Outputs     map[string]TypedValue `json:"outputs,omitempty"`
	CompletedAt time.Time             `json:"completedAt"`
	// CachedAt is non-zero when this RunResult was loaded from the
	// rerun cache rather than freshly produced. ResolveRerun sets it
	// to the original completed_at timestamp.
	CachedAt time.Time `json:"cachedAt,omitempty"`
}

// Cache is the persistence contract for workflow run reuse. The
// SQLite implementation lives in this file; tests can substitute
// MemoryCache.
type Cache interface {
	// Get returns the cached RunResult for the (workflowID, inputsHash)
	// tuple, or (nil, nil) when no row exists.
	Get(ctx context.Context, workflowID, inputsHash string) (*RunResult, error)
	// Put writes / overwrites the cached RunResult.
	Put(ctx context.Context, workflowID, inputsHash string, result RunResult) error
}

// HashInputs returns a stable sha256 hex digest of the inputs map.
// Keys are sorted before hashing so two callers passing the same map
// in different iteration orders share a cache row.
func HashInputs(inputs map[string]any) string {
	if len(inputs) == 0 {
		sum := sha256.Sum256([]byte("{}"))
		return hex.EncodeToString(sum[:])
	}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonical := make(map[string]json.RawMessage, len(keys))
	for _, k := range keys {
		// json.Marshal is deterministic for primitives + maps with
		// string keys; for typed values (TypedValue) the same applies
		// because every field is JSON-tagged.
		b, err := json.Marshal(inputs[k])
		if err != nil {
			// Fall back to a marker so an unmarshal-able value still
			// produces a stable (but distinct) hash; in practice the
			// caller should be passing JSON-compatible values.
			b = []byte("null")
		}
		canonical[k] = b
	}
	// Re-encode the sorted map to bytes via the keys slice to be
	// defensive across Go versions.
	var buf strings.Builder
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(canonical[k])
	}
	buf.WriteByte('}')
	sum := sha256.Sum256([]byte(buf.String()))
	return hex.EncodeToString(sum[:])
}

// ResolveRerun consults the cache and decides what the engine should
// do for this invocation. Three return shapes:
//
//   - (cached *RunResult, false, nil) — caller should return cached
//     verbatim and skip dispatching.
//   - (nil, true, ErrRerunPolicyAsk) — policy=prompt + cache hit;
//     caller surfaces the typed error so the UI can ask the user.
//   - (nil, false, nil) — no cached row OR policy=always; caller
//     proceeds with a fresh Engine.Run.
//
// A nil cache short-circuits to (nil, false, nil): callers that want
// caching MUST wire one up.
func ResolveRerun(
	ctx context.Context,
	cache Cache,
	workflowID string,
	inputs map[string]any,
	policy RerunPolicy,
) (*RunResult, bool, error) {
	if cache == nil {
		return nil, false, nil
	}
	if policy == RerunPolicyAlways {
		return nil, false, nil
	}
	hash := HashInputs(inputs)
	cached, err := cache.Get(ctx, workflowID, hash)
	if err != nil {
		return nil, false, fmt.Errorf("workflows: rerun cache get: %w", err)
	}
	if cached == nil {
		return nil, false, nil
	}
	switch policy {
	case RerunPolicySkip:
		return cached, false, nil
	case RerunPolicyPrompt:
		return nil, true, ErrRerunPolicyAsk
	default:
		// Defensive: unknown policy treated as always.
		return nil, false, nil
	}
}

// MemoryCache is an in-memory Cache used by tests and short-lived
// runs. It is goroutine-safe.
type MemoryCache struct {
	mu   sync.RWMutex
	rows map[string]RunResult
}

// NewMemoryCache returns an empty MemoryCache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{rows: make(map[string]RunResult)}
}

func (m *MemoryCache) key(workflowID, inputsHash string) string {
	return workflowID + "|" + inputsHash
}

// Get implements Cache.Get.
func (m *MemoryCache) Get(_ context.Context, workflowID, inputsHash string) (*RunResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.rows[m.key(workflowID, inputsHash)]
	if !ok {
		return nil, nil
	}
	out := v
	return &out, nil
}

// Put implements Cache.Put.
func (m *MemoryCache) Put(_ context.Context, workflowID, inputsHash string, result RunResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[m.key(workflowID, inputsHash)] = result
	return nil
}

// SQLiteCache is the production Cache. It writes to the
// workflow_runs_cache table landed by migration 0320.
type SQLiteCache struct {
	db  storage.DB
	now func() time.Time
}

// NewSQLiteCache returns a Cache backed by the supplied storage.DB.
// The DB MUST already be migrated to schema version 320 or later.
func NewSQLiteCache(db storage.DB) *SQLiteCache {
	return &SQLiteCache{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// Get implements Cache.Get.
func (c *SQLiteCache) Get(ctx context.Context, workflowID, inputsHash string) (*RunResult, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	var (
		blob      string
		completed int64
	)
	row := c.db.Reader().QueryRow(ctx,
		`SELECT output_json, completed_at
		   FROM workflow_runs_cache
		  WHERE workflow_id = ? AND inputs_hash = ?`,
		workflowID, inputsHash)
	if err := row.Scan(&blob, &completed); err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("workflows: cache scan: %w", err)
	}
	var rr RunResult
	if err := json.Unmarshal([]byte(blob), &rr); err != nil {
		return nil, fmt.Errorf("workflows: cache decode: %w", err)
	}
	rr.CachedAt = time.Unix(0, completed).UTC()
	return &rr, nil
}

// Put implements Cache.Put. UPSERTs the (workflow_id, inputs_hash)
// row with the latest serialised RunResult.
func (c *SQLiteCache) Put(ctx context.Context, workflowID, inputsHash string, result RunResult) error {
	if c == nil || c.db == nil {
		return errors.New("workflows: nil cache")
	}
	blob, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("workflows: cache encode: %w", err)
	}
	now := c.now().UnixNano()
	return c.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO workflow_runs_cache
			   (workflow_id, inputs_hash, output_json, completed_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(workflow_id, inputs_hash) DO UPDATE
			   SET output_json = excluded.output_json,
			       completed_at = excluded.completed_at`,
			workflowID, inputsHash, string(blob), now)
		if err != nil {
			return fmt.Errorf("workflows: cache upsert: %w", err)
		}
		return nil
	})
}

// runResultFromRun lifts the engine's *Run into a cacheable RunResult.
// The engine surfaces step transcripts on Run.Steps; we copy verbatim.
func runResultFromRun(r *Run) RunResult {
	out := RunResult{
		RunID:       r.ID,
		WorkflowID:  r.WorkflowID,
		Status:      r.Status,
		Steps:       append([]StepResult(nil), r.Steps...),
		Outputs:     r.Outputs,
		CompletedAt: r.EndedAt,
	}
	return out
}

// applyCachedRun copies a cached RunResult onto a freshly-allocated
// *Run with a new RunID + the supplied workflow id. The caller can
// surface this directly to the UI just like a fresh run.
func applyCachedRun(cached *RunResult, wfID string, now time.Time) *Run {
	r := &Run{
		ID:         randomRunID(),
		WorkflowID: wfID,
		Status:     cached.Status,
		StartedAt:  now,
		EndedAt:    now,
		Steps:      append([]StepResult(nil), cached.Steps...),
		Outputs:    cached.Outputs,
	}
	return r
}
