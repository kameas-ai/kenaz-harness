package advice

import "fmt"

// ValidateConfidence rejects an out-of-[0,100] confidence rather than
// clamping it — clamping would silently hide a model bug (or a
// prompt-injection attempt) behind a plausible-looking number, the same
// reasoning risk.ValidateScore's doc comment gives for risk scores.
// Callers: HeuristicAdvisor (a kind's own heuristic) and SidecarAdvisor
// (the engine's answer); FakeAdvisor mirrors it.
//
// (ParseRecommendation, the free-text JSON extractor for LLMAdvisor's
// model replies, was deleted in the WP14/15 review — 2026-09-30: its only
// producer, LLMAdvisor, was deleted in WP15, and design R1 bans an LLM in
// the advice path outright, so it had no caller left but its own tests.)
func ValidateConfidence(c int) error {
	if c < 0 || c > 100 {
		return fmt.Errorf("advice: confidence %d out of range [0,100]", c)
	}
	return nil
}
