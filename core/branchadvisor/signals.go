// Package branchadvisor implements the heuristic detector that fires when
// a user message looks like a contained side-task that would be a good
// candidate for spawning a subagent branch.
//
// All regex are compiled once at package init; the per-call cost is a
// linear scan of the compiled patterns, which stays well under the
// 5ms NFR-001 target for messages up to a few hundred characters.
package branchadvisor

import "regexp"

// positivePatterns contribute +1 to the signal count when any match is
// found in the user message. Each entry is (label, regex) so the
// detector can surface "what fired?" in the BranchSuggestion.Signals
// field (FR-011 / plan §3 "Rationale").
var positivePatterns []signalPattern

// negativePatterns contribute +1 to the noise count and dampen
// confidence to reduce false positives where the user is replacing or
// clarifying the prior request rather than adding a side task.
var negativePatterns []signalPattern

type signalPattern struct {
	label string
	re    *regexp.Regexp
}

func init() {
	positivePatterns = compilePatterns(rawPositive)
	negativePatterns = compilePatterns(rawNegative)
}

// rawPositive is the v1 positive-signal table (plan §arch §1).
var rawPositive = [][2]string{
	{"also_figure_out", `(?i)\balso\s+(figure\s+out|do|check|look\s+at)\b`},
	{"while_youre_at_it", `(?i)\bwhile\s+you'?re\s+at\s+it\b`},
	{"can_you_also", `(?i)\bcan\s+you\s+also\b`},
	{"side_task", `(?i)\bside\s+(question|task|note)\b`},
	{"tangent", `(?i)\btangent\b`},
	{"as_a_one_off", `(?i)\bas\s+a\s+one[- ]off\b`},
	{"in_parallel", `(?i)\bin\s+parallel\b`},
	{"before_that_let_me_know", `(?i)\bbefore\s+that,?\s+let\s+me\s+know\b`},
	{"quick_aside", `(?i)\bquick\s+aside\b`},
	{"unrelated_but", `(?i)\bunrelated\s+but\b`},
}

// rawNegative is the v1 negative-signal table.
var rawNegative = [][2]string{
	{"actually", `(?i)\bactually\b`},
	{"instead", `(?i)\binstead\b`},
	{"let_me_clarify", `(?i)\blet\s+me\s+clarify\b`},
	{"nevermind", `(?i)\bnevermind\b`},
	{"scratch_that", `(?i)\bscratch\s+that\b`},
	{"ignore_that", `(?i)\bignore\s+that\b`},
}

func compilePatterns(raw [][2]string) []signalPattern {
	out := make([]signalPattern, len(raw))
	for i, r := range raw {
		out[i] = signalPattern{
			label: r[0],
			re:    regexp.MustCompile(r[1]),
		}
	}
	return out
}
