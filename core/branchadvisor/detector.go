package branchadvisor

import (
	"os"
	"strings"
	"unicode"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// BranchSuggestion is the non-nil result returned by Detect when the
// detector's heuristic fires above the configured confidence threshold.
type BranchSuggestion struct {
	// ID is a stable opaque token used to correlate suggestion events
	// with subsequent accept / dismiss audit events. Callers may use any
	// monotonic or random ID generator; the detector itself generates a
	// simple sequential id when one is not supplied externally.
	ID string

	// Confidence is the normalized score [0, 1]. Computed as
	// signal_count / (signal_count + noise_count).
	Confidence float64

	// Rationale is a human-readable summary of the positive signals
	// that fired, suitable for the banner tooltip (FR-011).
	Rationale string

	// Signals is the list of positive-signal labels that contributed to
	// the score (stable short strings, e.g. "can_you_also").
	Signals []string

	// ProposedTitle is the first ≤40 characters of the message, trimmed
	// at the last whitespace boundary before the cutoff.
	ProposedTitle string
}

// Signals is the raw positive/negative regex tally for one message,
// computed BEFORE Detect applies the minConfidence threshold gate or the
// HARNESS_BRANCH_ADVISOR kill-switch. Exported so a second consumer that
// needs the tally itself — laya-advisors-01LAYA001 WP04's branch_now
// advice kind, which uses SignalCount/NoiseCount as two of its own
// Features fields — can read it without re-implementing the regex
// tables Detect compiles against (tasks.md WP04: "one detection
// implementation must remain — adapt or delegate, and record which").
// This package's own Detect is the adapted form: it now calls
// ComputeSignals below rather than duplicating the two scan loops.
type Signals struct {
	// SignalCount is how many rawPositive patterns matched.
	SignalCount int
	// NoiseCount is how many rawNegative patterns matched.
	NoiseCount int
	// Labels lists the positive-signal labels that matched, in
	// pattern-table order (the same slice Detect's own
	// BranchSuggestion.Signals field carries when the kind fires).
	Labels []string
}

// Confidence is the normalized [0,1] score Detect itself gates on:
// signal_count / (signal_count + noise_count), or 0 when neither table
// matched a single pattern. A named method (not inlined at each call
// site) so branch_now's heuristic backend maps the IDENTICAL formula to
// its own 0-100 confidence rather than a second, potentially-drifting
// derivation — see core/advice/kinds/branchnow's doc comment for the
// mapping.
func (s Signals) Confidence() float64 {
	total := s.SignalCount + s.NoiseCount
	if total == 0 {
		return 0
	}
	return float64(s.SignalCount) / float64(total)
}

// ComputeSignals runs both compiled pattern tables over message and
// returns the raw tally. Pure (no IO, no goroutines), same per-call cost
// profile NFR-001 requires of Detect (< 5ms for messages up to ~500
// chars on any modern CPU) — it IS the entire cost of Detect below plus
// two comparisons.
func ComputeSignals(message string) Signals {
	var signalCount int
	var matchedLabels []string
	for _, p := range positivePatterns {
		if p.re.MatchString(message) {
			signalCount++
			matchedLabels = append(matchedLabels, p.label)
		}
	}

	var noiseCount int
	for _, p := range negativePatterns {
		if p.re.MatchString(message) {
			noiseCount++
		}
	}

	return Signals{SignalCount: signalCount, NoiseCount: noiseCount, Labels: matchedLabels}
}

// Enabled reports whether the branch advisor's kill-switch is engaged —
// the same HARNESS_BRANCH_ADVISOR check Detect applies before ever
// computing a tally, exported so a second caller (branch_now's heuristic
// backend) honors the IDENTICAL kill-switch rather than growing its own
// copy of the env-var parsing.
func Enabled() bool {
	return !envOff()
}

// Detect runs the heuristic detector on a single user message and
// returns a BranchSuggestion when:
//
//  1. The HARNESS_BRANCH_ADVISOR environment variable is not "0" or "false".
//  2. At least one positive-signal pattern matches.
//  3. confidence ≥ minConfidence.
//
// Returns nil when no suggestion should be shown to the user.
// The function is pure (no IO, no goroutines) and satisfies NFR-001
// (< 5ms for messages up to ~500 chars on any modern CPU).
func Detect(message string, minConfidence float64) *BranchSuggestion {
	// Feature-flag check (DIRECTIVE_001 plan § feature flag section).
	if !Enabled() {
		return nil
	}

	if message == "" {
		return nil
	}

	sig := ComputeSignals(message)
	if sig.SignalCount == 0 {
		return nil
	}

	confidence := sig.Confidence()
	if confidence < minConfidence {
		return nil
	}

	return &BranchSuggestion{
		Confidence:    confidence,
		Rationale:     buildRationale(sig.Labels),
		Signals:       sig.Labels,
		ProposedTitle: proposedTitle(message),
	}
}

// DetectWithDefaults is a convenience wrapper that reads
// BranchAdvisorMinConfidence from the supplied Settings, falling back to
// settings.DefaultBranchAdvisorMinConfidence.
func DetectWithDefaults(message string, s settings.Settings) *BranchSuggestion {
	return Detect(message, s.EffectiveBranchAdvisorMinConfidence())
}

// envOff returns true when the HARNESS_BRANCH_ADVISOR environment
// variable is explicitly set to "0" or "false" (case-insensitive).
func envOff() bool {
	v := os.Getenv("HARNESS_BRANCH_ADVISOR")
	if v == "" {
		return false // default ON
	}
	lower := strings.ToLower(strings.TrimSpace(v))
	return lower == "0" || lower == "false"
}

// buildRationale constructs a human-readable explanation from the
// matched positive-signal labels. The label names are the v1 stable
// keys; the frontend can localize them or display the raw keys as-is in
// the "What signals were detected?" tooltip (FR-011).
func buildRationale(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	sb := strings.Builder{}
	sb.WriteString("Matched signal")
	if len(labels) > 1 {
		sb.WriteString("s")
	}
	sb.WriteString(": ")
	for i, l := range labels {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(l)
	}
	return sb.String()
}

// proposedTitle returns the first ≤40 characters of the message,
// trimmed at the last whitespace boundary before the character cutoff.
// Leading/trailing space is stripped.
func proposedTitle(message string) string {
	const maxRunes = 40
	message = strings.TrimSpace(message)
	runes := []rune(message)
	if len(runes) <= maxRunes {
		return message
	}
	// Find last whitespace at or before maxRunes.
	cut := maxRunes
	for cut > 0 && !unicode.IsSpace(runes[cut]) {
		cut--
	}
	if cut == 0 {
		// No whitespace found; hard-cut.
		cut = maxRunes
	}
	return strings.TrimSpace(string(runes[:cut]))
}
