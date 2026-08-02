package eval

import (
	"context"
	"fmt"
	"strings"
	"sync"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// ModelProfileSuiteCase is one workflow case in a ModelProfileSuite: the
// per-session (candidate vs. baseline) comparison RunMatrix drives to decide
// whether a candidate ModelProfile version regresses relative to the
// previously-active baseline (versioned-model-profile-01PMDL04 WP03).
type ModelProfileSuiteCase struct {
	// Label identifies this case in the gate's report and the RunMatrix
	// summary table.
	Label string

	// CandidateSessionID is the eval-capture recorded while exercising the
	// workflow under the candidate ModelProfile version. This is the
	// session actually replayed.
	CandidateSessionID string

	// BaselineSessionID is the eval-capture recorded under the
	// known-good / previously-active ModelProfile version, which the
	// candidate's replay is diffed against. Empty means "diff the
	// candidate capture against itself" — i.e. this case can never
	// regress (mirrors the pre-WP03 MatrixCase default). A suite intended
	// to actually gate promotions should set this to a distinct session.
	BaselineSessionID string

	// MinScore is this case's overall-score pass threshold in [0,1]. Zero
	// means "use the suite's MinOverallScore".
	MinScore float64
}

// ModelProfileSuite is the eval-fit regression suite a candidate
// ModelProfile version must pass before a bundle Activate promotes it
// (versioned-model-profile-01PMDL04 WP03). It is looked up by (ID,
// Version) — the same identity ModelProfile.EvalManifest references.
type ModelProfileSuite struct {
	ID      string
	Version string
	Cases   []ModelProfileSuiteCase

	// MinOverallScore is the default per-case score threshold applied when
	// a case leaves MinScore unset (<= 0). Zero means "no threshold" — a
	// suite that only wants to catch replay errors (missing captures)
	// rather than content drift can leave this at its zero value, but then
	// the gate cannot refuse on content regression alone.
	MinOverallScore float64
}

// ModelProfileSuiteStore is a simple (ID, Version) -> ModelProfileSuite
// in-memory registry. It mirrors llm.ModelProfileStore's Load/Resolve shape
// but deliberately has no family-glob inheritance: eval suites are curated
// per-workflow fixtures, not per-model-family behavioral defaults, so there
// is no "family glob resolves to a suite" concept to reproduce.
type ModelProfileSuiteStore struct {
	mu      sync.RWMutex
	entries map[string]ModelProfileSuite
}

// NewModelProfileSuiteStore returns an empty store. Resolving against an
// empty store always returns found=false — a ModelProfile whose
// EvalManifest names a suite nobody has loaded is a configuration error,
// not silently "no gate" (that distinction matters: absent EvalManifest is
// inert by design; a *present* EvalManifest with a store that can't resolve
// it is a broken reference and should surface loudly).
func NewModelProfileSuiteStore() *ModelProfileSuiteStore {
	return &ModelProfileSuiteStore{entries: make(map[string]ModelProfileSuite)}
}

func modelProfileSuiteKey(id, version string) string { return id + "@" + version }

// Load installs suite into the store keyed by (suite.ID, suite.Version).
// Loading a suite under an (ID, Version) pair that already exists replaces
// it — unlike llm.ModelProfileStore.Load's collision-rejection, eval
// suites are curated test fixtures the operator is expected to iterate on,
// not activation-tracked artifacts.
func (s *ModelProfileSuiteStore) Load(suite ModelProfileSuite) error {
	if strings.TrimSpace(suite.ID) == "" {
		return fmt.Errorf("eval: model profile suite: id is required")
	}
	if strings.TrimSpace(suite.Version) == "" {
		return fmt.Errorf("eval: model profile suite %q: version is required", suite.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[modelProfileSuiteKey(suite.ID, suite.Version)] = suite
	return nil
}

// Resolve returns the suite registered for (id, version).
func (s *ModelProfileSuiteStore) Resolve(id, version string) (ModelProfileSuite, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	suite, ok := s.entries[modelProfileSuiteKey(id, version)]
	return suite, ok
}

// ModelProfileGateResult is the outcome of GateModelProfilePromotion.
type ModelProfileGateResult struct {
	// Passed is true only when every case in the suite replayed without
	// error and cleared its score threshold.
	Passed bool
	// Results carries the underlying RunMatrix results, one per suite case,
	// in suite order.
	Results []MatrixResult
	// Table is the RunMatrix Markdown summary table, useful for logging.
	Table string
	// FailingCase names the first case that caused Passed=false. Empty
	// when Passed is true.
	FailingCase string
	// Reason is a human-readable description of the failure. Empty when
	// Passed is true.
	Reason string
}

// GateModelProfilePromotion runs suite's cases through RunMatrix — the same
// capture -> replay -> diff primitives the compaction-strategy matrix
// already uses — and reports whether every case cleared its score
// threshold. It never re-invokes a live model: like the rest of core/eval,
// this is a deterministic replay/diff over already-recorded captures. The
// regression signal comes from diffing the candidate session's replay
// against a *different*, previously-recorded baseline session
// (MatrixCase.BaselineSessionID) rather than a session against itself.
func GateModelProfilePromotion(ctx context.Context, captureDir, runsDir string, suite ModelProfileSuite) (*ModelProfileGateResult, error) {
	cases := make([]MatrixCase, 0, len(suite.Cases))
	for _, c := range suite.Cases {
		cases = append(cases, MatrixCase{
			SessionID:         c.CandidateSessionID,
			BaselineSessionID: c.BaselineSessionID,
			Label:             c.Label,
			CachedOnly:        true,
			ModelProfileID:    suite.ID,
		})
	}

	results, table, err := RunMatrix(ctx, captureDir, runsDir, cases, DiffModeBytes)
	gr := &ModelProfileGateResult{Results: results, Table: table, Passed: true}
	if err != nil {
		gr.Passed = false
		gr.Reason = fmt.Sprintf("eval-fit suite %s/%s: run error: %v", suite.ID, suite.Version, err)
		return gr, err
	}

	for i, r := range results {
		label := r.Label
		if label == "" {
			label = r.SessionID
		}
		if r.Err != nil {
			gr.Passed = false
			gr.FailingCase = label
			gr.Reason = fmt.Sprintf("case %q failed to replay: %v", label, r.Err)
			return gr, nil
		}

		threshold := suite.MinOverallScore
		if i < len(suite.Cases) && suite.Cases[i].MinScore > 0 {
			threshold = suite.Cases[i].MinScore
		}
		if threshold > 0 && r.OverallScore < threshold {
			gr.Passed = false
			gr.FailingCase = label
			gr.Reason = fmt.Sprintf("case %q scored %.3f, below threshold %.3f (changed %d/%d compared entries)",
				label, r.OverallScore, threshold, r.ChangedCount, r.TotalCompared)
			return gr, nil
		}
	}
	return gr, nil
}

// ModelProfileGate adapts a ModelProfileSuiteStore + GateModelProfilePromotion
// into the small Check(ctx, ModelProfile) error contract that
// core/llm/bundleartifact.ModelProfileHandler.Activate consumes (WP03). It
// is the concrete, real implementation of that contract; bundleartifact
// never imports core/eval's full surface, only this adapter's method shape.
type ModelProfileGate struct {
	// Suites resolves a ModelProfile's EvalManifest reference to the actual
	// suite to run. Required for Check to do anything meaningful.
	Suites *ModelProfileSuiteStore
	// CaptureDir / RunsDir are the eval-captures / eval-runs directories
	// passed through to GateModelProfilePromotion.
	CaptureDir string
	RunsDir    string
}

// Check implements the gate contract: nil means "promote", non-nil means
// "refuse" (with the offending case named in the error). A ModelProfile
// with a nil EvalManifest is passed through unconditionally — the gate is
// opt-in by construction, never a mandatory hurdle for profiles that never
// asked for one.
func (g *ModelProfileGate) Check(ctx context.Context, p corellm.ModelProfile) error {
	if g == nil || p.EvalManifest == nil {
		return nil
	}
	if g.Suites == nil {
		return fmt.Errorf("eval: model profile %q references eval_manifest (id=%q) but no suite store is configured", p.ID, p.EvalManifest.ID)
	}
	suite, found := g.Suites.Resolve(p.EvalManifest.ID, p.EvalManifest.Version)
	if !found {
		return fmt.Errorf("eval: model profile %q: eval manifest (id=%q, version=%q) not found in suite store", p.ID, p.EvalManifest.ID, p.EvalManifest.Version)
	}
	result, err := GateModelProfilePromotion(ctx, g.CaptureDir, g.RunsDir, suite)
	if err != nil {
		return fmt.Errorf("eval: model profile %q eval-fit gate error: %w", p.ID, err)
	}
	if !result.Passed {
		return fmt.Errorf("eval: model profile %q refused promotion by eval-fit gate: %s", p.ID, result.Reason)
	}
	return nil
}
