package localruntime

import (
	"regexp"
	"strings"
)

// quantizationPattern matches all known GGUF / float / GPTQ quantization
// labels embedded in a model filename or tag. It uses a submatch group so
// callers get the label without surrounding separators.
//
// Supported families (case-insensitive, separator-normalised):
//   - GGUF K-quants:  Q4_K_M, Q4_K_S, Q5_K_M, Q5_K_S, Q6_K, Q2_K, etc.
//   - GGUF integer:   Q4_0, Q4_1, Q5_0, Q5_1, Q8_0
//   - GGUF I-quants:  IQ2_XXS, IQ2_XS, IQ2_S, IQ3_S, IQ4_NL, IQ4_XS, etc.
//   - Float formats:  F16, F32, BF16, FP16, FP32
//   - GPTQ / AWQ / EXL2
//   - int4 / int8 shorthand
var quantizationPattern = regexp.MustCompile(
	`(?i)(?:^|[_\-\.:/ ])` +
		`(IQ[1-4]_[A-Z0-9]+` + // IQ-quants: IQ2_XXS, IQ4_NL …
		`|Q[2-8]_K(?:_[SM])?` + // K-quants:  Q4_K_M, Q5_K_S, Q6_K …
		`|Q[2-8]_[0-9]` + // int-quants: Q4_0, Q8_0 …
		`|BF16|F(?:P)?16|F(?:P)?32` + // float:     BF16, F16, FP16, F32 …
		`|GPTQ(?:_\d+[Bb]it)?` + // GPTQ
		`|AWQ|EXL2` + // AWQ, EXL2
		`|INT[48]` + // INT4, INT8
		`)` +
		`(?:[_\-\.:/ ]|\.gguf|\.bin|$)`,
)

// ParseQuantLevel extracts the quantization level from a model name or path.
// Separators ("-", ".", ":", "/", " ") are treated as interchangeable so
// that both "Q4-K-M" and "Q4_K_M" are recognised.
// Returns an empty string when no quantization label is detected.
func ParseQuantLevel(s string) string {
	// Normalise common separators to underscores before matching.
	normalised := strings.NewReplacer("-", "_", ".", "_", ":", "_", "/", "_").Replace(s)
	// Re-add the leading boundary character the regex needs.
	if m := quantizationPattern.FindStringSubmatch("_" + normalised); len(m) >= 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}
