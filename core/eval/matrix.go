package eval

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MatrixCase is one cell in the RunMatrix: one (capture, strategySet) pair.
type MatrixCase struct {
	// SessionID is the capture session to replay.
	SessionID string
	// Label is a human-readable identifier for this case, used in the
	// summary table. If empty it defaults to "<SessionID>/<strategy hash>".
	Label string
	// Overrides are the strategy overrides to apply for this case.
	Overrides []StrategyOverride
	// CachedOnly mirrors ReplayOptions.CachedOnly; default true for matrix runs.
	CachedOnly bool

	// BaselineSessionID is the capture session Diff compares the replay
	// output against. Empty means "diff against SessionID's own capture" —
	// today's compaction-only behavior, unchanged: replaying a session and
	// diffing it against itself trivially scores ~1.0 because Replay never
	// mutates KindMessage payloads, so the compaction-strategy matrix (which
	// only ever set SessionID) never regresses.
	//
	// The model-profile dimension (versioned-model-profile-01PMDL04 WP03)
	// is the reason this field exists: a candidate ModelProfile version is
	// exercised as its own capture session (CandidateSessionID in
	// ModelProfileSuiteCase, threaded through as SessionID here) and diffed
	// against a *different*, previously-recorded known-good capture
	// (BaselineSessionID) — genuinely comparing two distinct transcripts
	// rather than a session against itself, which is what makes the gate
	// capable of actually failing.
	BaselineSessionID string

	// ModelProfileID / ModelProfileVersion optionally label this case with
	// the candidate profile identity under test. Purely informational —
	// they do not change replay behavior (the mechanism to re-render a
	// request under an alternate profile is
	// per-family-message-shaping-01PMDL06's concern; this mission only
	// gives eval a dimension to diff distinct captures against).
	ModelProfileID      string
	ModelProfileVersion string
}

// MatrixResult is the outcome for one MatrixCase.
type MatrixResult struct {
	MatrixCase
	RunID           string
	OverallScore    float64
	TotalCompared   int
	ChangedCount    int
	ResponsesServed int
	ResponsesMissed int
	Err             error
	DurationMs      int64
}

// RunMatrix runs a replay for each (capture, strategySet) pair and returns a
// slice of MatrixResult plus a Markdown summary table.
//
// captureDir and runsDir are the eval-captures and eval-runs directories
// respectively. baselineCaptureDir is the directory of the baseline captures to
// diff against (often the same as captureDir).
//
// This function is the primary test-driver entry point; Go tests call it via:
//
//	results, table, err := eval.RunMatrix(ctx, captureDir, runsDir, cases, eval.DiffModeBytes)
func RunMatrix(
	ctx context.Context,
	captureDir string,
	runsDir string,
	cases []MatrixCase,
	diffMode DiffMode,
) ([]MatrixResult, string, error) {
	replayer := NewReplayer(captureDir, runsDir)
	results := make([]MatrixResult, 0, len(cases))

	for _, mc := range cases {
		if ctx.Err() != nil {
			return results, buildMarkdownTable(results), ctx.Err()
		}
		start := time.Now()
		opts := ReplayOptions{
			StrategyOverrides: mc.Overrides,
			CachedOnly:        mc.CachedOnly,
			AllowLive:         !mc.CachedOnly,
		}
		rr, err := replayer.Replay(ctx, mc.SessionID, opts)
		elapsed := time.Since(start).Milliseconds()

		mr := MatrixResult{
			MatrixCase: mc,
			DurationMs: elapsed,
		}

		if err != nil {
			mr.Err = err
			results = append(results, mr)
			continue
		}

		mr.RunID = rr.RunID
		mr.ResponsesServed = rr.ResponsesServed
		mr.ResponsesMissed = rr.ResponsesMissed

		// Run diff against the baseline capture. Defaults to the replayed
		// session's own capture (today's compaction-only behavior); the
		// model-profile dimension (WP03) sets BaselineSessionID to a
		// distinct known-good session so the diff is a genuine
		// candidate-vs-baseline comparison rather than a session against
		// itself.
		baselineSessionID := mc.SessionID
		if mc.BaselineSessionID != "" {
			baselineSessionID = mc.BaselineSessionID
		}
		baselinePath := fmt.Sprintf("%s/%s.jsonl", captureDir, baselineSessionID)
		tracePath := fmt.Sprintf("%s/%s/trace.jsonl", runsDir, rr.RunID)
		runDir := fmt.Sprintf("%s/%s", runsDir, rr.RunID)

		diff, diffErr := Diff(runDir, baselinePath, tracePath, diffMode)
		if diffErr != nil {
			mr.Err = diffErr
		} else if diff != nil {
			mr.OverallScore = diff.OverallScore
			mr.TotalCompared = diff.TotalCompared
			mr.ChangedCount = diff.ChangedCount
		}

		results = append(results, mr)
	}

	return results, buildMarkdownTable(results), nil
}

// buildMarkdownTable renders a Markdown table from the results slice.
func buildMarkdownTable(results []MatrixResult) string {
	if len(results) == 0 {
		return "| Case | Run ID | Score | Compared | Changed | Served | Missed | Duration | Error |\n" +
			"|------|--------|-------|----------|---------|--------|--------|----------|-------|\n"
	}

	var sb strings.Builder
	sb.WriteString("| Case | Run ID | Score | Compared | Changed | Served | Missed | Duration | Error |\n")
	sb.WriteString("|------|--------|-------|----------|---------|--------|--------|----------|-------|\n")

	for _, r := range results {
		label := r.Label
		if label == "" {
			label = r.SessionID
		}
		errStr := ""
		if r.Err != nil {
			errStr = r.Err.Error()
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %.3f | %d | %d | %d | %d | %dms | %s |\n",
			label,
			r.RunID,
			r.OverallScore,
			r.TotalCompared,
			r.ChangedCount,
			r.ResponsesServed,
			r.ResponsesMissed,
			r.DurationMs,
			errStr,
		))
	}
	return sb.String()
}
