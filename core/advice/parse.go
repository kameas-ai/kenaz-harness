package advice

import (
	"encoding/json"
	"fmt"
	"strings"
)

// recommendationWire is the strict wire shape ParseRecommendation
// decodes — spec §2's output contract: "strict binary/enum with
// confidence 0-100, parsed defensively — unparseable ⇒ no advice, never
// a guess."
type recommendationWire struct {
	Decision   bool `json:"decision"`
	Confidence int  `json:"confidence"`
}

// ParseRecommendation extracts the JSON object from a model's raw text
// reply. Tolerates the response being wrapped in a markdown code fence
// (some models do this despite instructions) by locating the first '{'
// and the last '}' rather than requiring the whole string to be bare
// JSON — mirrors risk.parseRaterResponse exactly. Does NOT tolerate
// anything else: no JSON object, malformed JSON, or an out-of-range
// confidence is a parse failure, and the caller (LLMAdvisor.Recommend)
// surfaces it as an error wrapping ErrNoAdvice — never a best-guess
// Recommendation.
func ParseRecommendation(text string) (decision bool, confidence int, err error) {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return false, 0, fmt.Errorf("advice: response has no JSON object: %q", truncateForError(text))
	}
	var parsed recommendationWire
	if jerr := json.Unmarshal([]byte(text[start:end+1]), &parsed); jerr != nil {
		return false, 0, fmt.Errorf("advice: response is not valid JSON: %w", jerr)
	}
	if verr := ValidateConfidence(parsed.Confidence); verr != nil {
		return false, 0, verr
	}
	return parsed.Decision, parsed.Confidence, nil
}

// ValidateConfidence rejects an out-of-[0,100] confidence rather than
// clamping it — clamping would silently hide a model bug (or a
// prompt-injection attempt) behind a plausible-looking number, the same
// reasoning risk.ValidateScore's doc comment gives for risk scores.
func ValidateConfidence(c int) error {
	if c < 0 || c > 100 {
		return fmt.Errorf("advice: confidence %d out of range [0,100]", c)
	}
	return nil
}

// truncateForError bounds an untrusted string embedded in an error
// message so a pathological model response cannot blow up log lines.
// Mirrors risk.truncateForError.
func truncateForError(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
