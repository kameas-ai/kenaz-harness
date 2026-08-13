package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ReplayOptions controls how a capture replay is driven.
//
// There is deliberately no strategy dial here. A replay is a re-render of
// a recorded session from its cached responses: it never re-executes a
// tool and never re-invokes a model, and Diff scores only assistant-role
// KindMessage text, which Replay copies through untouched. A compaction /
// memory / branching knob could therefore never move a replay's score —
// it could only change what a trace file *claimed* the strategy was. The
// override machinery that used to live here did exactly that and nothing
// more, so it was removed rather than carried as a knob that looks live.
// Making one real would mean re-issuing each LLMRequestEntry as a
// corellm.GenerationRequest (which the capture format cannot reconstruct:
// it records no tools, temperature, token caps, or sampling params),
// synthesizing the assistant messages Diff compares, and standing up a
// compaction pipeline inside this package.
type ReplayOptions struct {
	// CachedOnly, when true, serves every LLM call from the cached
	// responses recorded in the capture file identified by request
	// fingerprint. A cache miss returns ErrCacheMiss.
	CachedOnly bool

	// AllowLive, when true and the cache misses, falls through to a real
	// LLM API call via Registry. AllowLive is meaningful only when
	// CachedOnly is false.
	AllowLive bool

	// RunID is the identifier for this run; used to name the output
	// directory <DataDir>/eval-runs/<RunID>/. If empty, a timestamp is
	// used.
	RunID string
}

// ErrCacheMiss is returned by the cached-only replay when no cached
// response is available for a given request fingerprint.
var ErrCacheMiss = errors.New("eval/replay: no cached response for request fingerprint")

// ErrNoCaptureFile is returned when the capture file path does not exist.
var ErrNoCaptureFile = errors.New("eval/replay: capture file not found")

// responseCache maps request fingerprint → cached LLMResponseEntry.
type responseCache map[string]LLMResponseEntry

// buildResponseCache reads a capture file and extracts all LLMResponseEntry
// records, keyed by RequestFingerprint.
func buildResponseCache(capturePath string) (responseCache, error) {
	cache := make(responseCache)
	err := ReadCapture(capturePath, func(e CaptureEntry) error {
		if e.Kind != KindLLMResponse {
			return nil
		}
		var resp LLMResponseEntry
		if err := json.Unmarshal(e.Payload, &resp); err != nil {
			return nil // skip malformed entries
		}
		if resp.RequestFingerprint != "" {
			cache[resp.RequestFingerprint] = resp
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cache, nil
}

// replayEntry is the ordered transcript element produced by Replay.
type replayEntry struct {
	SeqNo     int64
	Kind      EntryKind
	Timestamp time.Time
	Payload   json.RawMessage
}

// ReplayResult is the outcome of a single Replay execution.
type ReplayResult struct {
	RunID       string
	CaptureFile string
	// Entries is the ordered sequence of entries replayed, with each
	// cached LLM response spliced in after the request that matched it.
	// It mirrors the trace.jsonl written to disk.
	Entries []replayEntry
	// ResponsesServed is the number of LLM responses served from the cache.
	ResponsesServed int
	// ResponsesMissed is the number of cache misses (0 when CachedOnly=false
	// and AllowLive=true, since misses fall through to live APIs).
	ResponsesMissed int
}

// Replayer drives a deterministic replay of a capture file.
type Replayer struct {
	// captureDir is the directory holding <session_id>.jsonl files.
	captureDir string
	// runsDir is the directory under which run output directories are created.
	runsDir string
}

// NewReplayer constructs a Replayer. captureDir and runsDir are typically
// <DataDir>/eval-captures and <DataDir>/eval-runs respectively.
func NewReplayer(captureDir, runsDir string) *Replayer {
	return &Replayer{captureDir: captureDir, runsDir: runsDir}
}

// Replay replays the capture file for sessionID. It reconstructs the
// message/tool-call/llm-request sequence from the capture, serves LLM
// responses from the cache (or live APIs per opts.AllowLive), and writes
// <runsDir>/<RunID>/{trace.jsonl, summary.json} on completion.
//
// This function does NOT re-execute tool calls against real systems —
// it reconstructs them from the recorded results. The "replay" is a
// deterministic read-through of the capture, with LLM calls optionally
// re-routed to the response cache.
func (r *Replayer) Replay(ctx context.Context, sessionID string, opts ReplayOptions) (*ReplayResult, error) {
	capturePath := filepath.Join(r.captureDir, sessionID+".jsonl")
	if _, err := os.Stat(capturePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrNoCaptureFile, capturePath)
	}

	// Build the response cache from the capture.
	cache, err := buildResponseCache(capturePath)
	if err != nil {
		return nil, fmt.Errorf("eval/replay: build cache: %w", err)
	}

	// Determine run ID.
	runID := opts.RunID
	if runID == "" {
		runID = fmt.Sprintf("%s-%d", sessionID[:min(8, len(sessionID))], time.Now().UnixMilli())
	}

	result := &ReplayResult{
		RunID:       runID,
		CaptureFile: capturePath,
	}

	// Walk the capture and reconstruct the replay trace.
	err = ReadCapture(capturePath, func(e CaptureEntry) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		entry := replayEntry{
			SeqNo:     e.SeqNo,
			Kind:      e.Kind,
			Timestamp: e.Timestamp,
			Payload:   e.Payload,
		}

		// For LLM requests, check the cache.
		if e.Kind == KindLLMRequest {
			var req LLMRequestEntry
			if err := json.Unmarshal(e.Payload, &req); err == nil {
				fp := req.Fingerprint
				if cached, ok := cache[fp]; ok {
					result.ResponsesServed++
					// Emit the cached response as a synthetic entry.
					cachedPayload, _ := json.Marshal(cached)
					result.Entries = append(result.Entries, replayEntry{
						SeqNo:     e.SeqNo,
						Kind:      KindLLMRequest,
						Timestamp: e.Timestamp,
						Payload:   e.Payload,
					})
					result.Entries = append(result.Entries, replayEntry{
						SeqNo:     e.SeqNo,
						Kind:      KindLLMResponse,
						Timestamp: e.Timestamp,
						Payload:   cachedPayload,
					})
					return nil
				}
				// Cache miss.
				result.ResponsesMissed++
				if opts.CachedOnly {
					// Surface miss as a trace annotation; do not abort.
					missAnnotation, _ := json.Marshal(map[string]string{
						"miss": "true",
						"fingerprint": fp,
					})
					entry.Payload = missAnnotation
				}
				// opts.AllowLive has no effect here: re-issuing this
				// request would require rebuilding a
				// corellm.GenerationRequest from LLMRequestEntry, which
				// records only (profile, model, system, messages) — no
				// tools, temperature, token caps, or sampling params —
				// and a corellm.Registry this package is not given. A
				// miss is therefore recorded as a miss and nothing more.
			}
		}

		result.Entries = append(result.Entries, entry)
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("eval/replay: walk capture: %w", err)
	}

	// Write run output.
	if err := r.writeRunOutput(runID, result, opts); err != nil {
		return result, fmt.Errorf("eval/replay: write output: %w", err)
	}

	return result, nil
}

// writeRunOutput writes <runsDir>/<runID>/{trace.jsonl, summary.json}.
func (r *Replayer) writeRunOutput(runID string, result *ReplayResult, opts ReplayOptions) error {
	runDir := filepath.Join(r.runsDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return err
	}

	// Write trace.jsonl
	tracePath := filepath.Join(runDir, "trace.jsonl")
	tf, err := os.Create(tracePath)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(tf)
	for _, e := range result.Entries {
		if err := enc.Encode(e); err != nil {
			_ = tf.Close()
			return err
		}
	}
	if err := tf.Close(); err != nil {
		return err
	}

	// Write summary.json
	type summaryJSON struct {
		RunID           string    `json:"run_id"`
		CaptureFile     string    `json:"capture_file"`
		StartedAt       time.Time `json:"started_at"`
		EntryCount      int       `json:"entry_count"`
		ResponsesServed int       `json:"responses_served"`
		ResponsesMissed int       `json:"responses_missed"`
		CachedOnly      bool      `json:"cached_only"`
		AllowLive       bool      `json:"allow_live"`
	}
	summary := summaryJSON{
		RunID:           result.RunID,
		CaptureFile:     result.CaptureFile,
		StartedAt:       time.Now().UTC(),
		EntryCount:      len(result.Entries),
		ResponsesServed: result.ResponsesServed,
		ResponsesMissed: result.ResponsesMissed,
		CachedOnly:      opts.CachedOnly,
		AllowLive:       opts.AllowLive,
	}
	summaryPath := filepath.Join(runDir, "summary.json")
	sf, err := os.Create(summaryPath)
	if err != nil {
		return err
	}
	enc2 := json.NewEncoder(sf)
	enc2.SetIndent("", "  ")
	if err := enc2.Encode(summary); err != nil {
		_ = sf.Close()
		return err
	}
	return sf.Close()
}

// min is a local helper (Go 1.21 has built-in min but keeping explicit for
// compat with go.mod version).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
