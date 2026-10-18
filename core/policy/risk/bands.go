package risk

import "fmt"

// Band is a named range over the 0-100 score space, per spec FR-002.
// The score needs arithmetic (threshold comparison, WP06's family-floor
// max); bands are for humans reviewing a policy, a prompt, or a
// Settings panel — owner decision 3 (spec.md, 2026-09-12): "keep both."
type Band string

const (
	BandLow      Band = "low"      // 0-19: read-only, reversible, local.
	BandModerate Band = "moderate" // 20-39: local write, reversible.
	BandElevated Band = "elevated" // 40-59: external read, or a write with blast radius.
	BandHigh     Band = "high"     // 60-79: outbound side effect, or hard-to-reverse local change.
	BandCritical Band = "critical" // 80-100: destructive, exfiltrating, or irreversible.
)

// BandAnchor pins one row of the spec FR-002 table as committed data,
// so the rater's behaviour is checked against fixtures rather than
// prose. Min/Max are inclusive.
type BandAnchor struct {
	Band     Band
	Min, Max int
	// Meaning is the one-line description from the spec table.
	Meaning string
	// Examples mirrors the spec table's example column — used by the
	// rater's own prompt fixture (WP05/WP06) as few-shot anchors, and by
	// TestBandAnchorsMatchFixture to cross-check the committed JSON
	// fixture (testdata/bands.json) hasn't drifted from this table.
	Examples []string
}

// BandAnchors is the canonical, ordered table from spec FR-002. Kept as
// a Go slice (not just the JSON fixture) so BandFor and any future
// runtime consumer (the UI's numeric-to-band rendering, owner decision
// 3) has a zero-I/O source of truth; testdata/bands.json exists so the
// anchors are also reviewable/diffable as data, and
// TestBandAnchorsMatchFixture pins the two against each other.
var BandAnchors = []BandAnchor{
	{
		Band: BandLow, Min: 0, Max: 19,
		Meaning:  "read-only, reversible, local",
		Examples: []string{"read a file", "list a directory", "search"},
	},
	{
		Band: BandModerate, Min: 20, Max: 39,
		Meaning:  "local write, reversible",
		Examples: []string{"edit a file in the workspace", "write a scratch file"},
	},
	{
		Band: BandElevated, Min: 40, Max: 59,
		Meaning:  "external read, or a write with blast radius",
		Examples: []string{"HTTP GET", "install a dependency"},
	},
	{
		Band: BandHigh, Min: 60, Max: 79,
		Meaning:  "outbound side effect, or hard-to-reverse local change",
		Examples: []string{"send a request that mutates", "rewrite many files", "git push to a branch"},
	},
	{
		Band: BandCritical, Min: 80, Max: 100,
		Meaning:  "destructive, exfiltrating, or irreversible",
		Examples: []string{"delete data", "force-push", "rotate a credential", "post to a third party", "spend money"},
	},
}

// ValidateScore reports whether score is a well-formed rating value
// (0-100 inclusive). RiskRater implementations MUST call this (or
// equivalent logic) and return an error rather than clamping — see
// Rating.Score's doc comment.
func ValidateScore(score int) error {
	if score < 0 || score > 100 {
		return fmt.Errorf("risk: score %d out of range [0,100]", score)
	}
	return nil
}

// BandFor maps a validated 0-100 score to its named band. Scores
// outside the documented range fall back to the nearest edge band
// (Low for negative, Critical for >100) rather than panicking — callers
// needing strict validation should call ValidateScore first; BandFor is
// a display helper (the UI's numeric-to-band rendering, owner decision
// 3), not a second validation gate, and a display helper that panics on
// bad input turns a rendering bug into a crash.
func BandFor(score int) Band {
	for _, a := range BandAnchors {
		if score >= a.Min && score <= a.Max {
			return a.Band
		}
	}
	if score < BandAnchors[0].Min {
		return BandAnchors[0].Band
	}
	return BandAnchors[len(BandAnchors)-1].Band
}
