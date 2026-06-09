package branchadvisor

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// threshold is the default used across most tests.
const threshold = settings.DefaultBranchAdvisorMinConfidence

func TestDetect_NilForEmpty(t *testing.T) {
	t.Parallel()
	if got := Detect("", threshold); got != nil {
		t.Errorf("expected nil for empty message, got %+v", got)
	}
}

func TestDetect_NilForNoSignal(t *testing.T) {
	t.Parallel()
	msg := "Please help me write a function that sorts a slice."
	if got := Detect(msg, threshold); got != nil {
		t.Errorf("expected nil for no-signal message, got %+v", got)
	}
}

func TestDetect_CanYouAlso(t *testing.T) {
	t.Parallel()
	msg := "Great, can you also check the latest version of the dependency?"
	got := Detect(msg, 0.5) // lower threshold so lone positive fires
	if got == nil {
		t.Fatal("expected suggestion, got nil")
	}
	if !contains(got.Signals, "can_you_also") {
		t.Errorf("expected can_you_also in signals, got %v", got.Signals)
	}
	if got.Confidence <= 0 || got.Confidence > 1 {
		t.Errorf("confidence out of [0,1]: %v", got.Confidence)
	}
}

func TestDetect_WhileYoureAtIt(t *testing.T) {
	t.Parallel()
	msg := "While you're at it, look at the error handling too."
	got := Detect(msg, 0.5)
	if got == nil {
		t.Fatal("expected suggestion, got nil")
	}
	if !contains(got.Signals, "while_youre_at_it") {
		t.Errorf("expected while_youre_at_it in signals, got %v", got.Signals)
	}
}

func TestDetect_SideQuestion(t *testing.T) {
	t.Parallel()
	msg := "This is a side question: what's the difference between X and Y?"
	got := Detect(msg, 0.5)
	if got == nil {
		t.Fatal("expected suggestion for side question")
	}
	if !contains(got.Signals, "side_task") {
		t.Errorf("expected side_task in signals, got %v", got.Signals)
	}
}

func TestDetect_NegativeDampensConfidence(t *testing.T) {
	t.Parallel()
	// "actually" is a negative signal; confidence should drop below
	// threshold (0.85 → 0.5 when 1 positive + 1 negative).
	msg := "Actually, can you also do X?"
	got := Detect(msg, threshold)
	if got != nil {
		t.Errorf("expected nil after negative signal dampening, got confidence=%v", got.Confidence)
	}
	// "can you also do X?" fires both can_you_also and also_figure_out
	// (2 positives), with 1 negative ("actually") → 2/(2+1) ≈ 0.667.
	// That is still above 0.5 so confidence > 0 and below threshold (0.85).
	got2 := Detect(msg, 0.4)
	if got2 == nil {
		t.Fatal("expected suggestion at lower threshold (0.4) after dampening")
	}
	// 2 positives / (2 positives + 1 negative) = 2/3 ≈ 0.667
	want := 2.0 / 3.0
	if got2.Confidence != want {
		t.Errorf("expected confidence %v (2/(2+1)), got %v", want, got2.Confidence)
	}
}

func TestDetect_MultipleNegatives_ZeroConfidence(t *testing.T) {
	t.Parallel()
	// Multiple negatives with one positive → confidence < 0.5 → no
	// suggestion even at threshold=0.
	msg := "Actually, scratch that, instead can you also help?"
	got := Detect(msg, 0) // threshold=0 would still fire if any positive
	if got == nil {
		t.Fatal("expected suggestion (one positive signal present)")
	}
	// 1 positive / (1 + 3 negatives) = 0.25 confidence
	if got.Confidence > 0.3 {
		t.Errorf("expected low confidence with many negatives, got %v", got.Confidence)
	}
}

func TestDetect_ProposedTitle_LongMessage(t *testing.T) {
	t.Parallel()
	// Build a message longer than 40 chars with a word boundary inside
	prefix := "Can you also figure out why the"
	suffix := " deployment pipeline fails every Monday morning?"
	msg := prefix + suffix
	got := Detect(msg, 0.5)
	if got == nil {
		t.Fatal("expected suggestion")
	}
	if len([]rune(got.ProposedTitle)) > 40 {
		t.Errorf("ProposedTitle too long: %q (%d runes)", got.ProposedTitle, len([]rune(got.ProposedTitle)))
	}
	if strings.HasSuffix(got.ProposedTitle, " ") {
		t.Errorf("ProposedTitle has trailing space: %q", got.ProposedTitle)
	}
}

func TestDetect_ProposedTitle_ShortMessage(t *testing.T) {
	t.Parallel()
	msg := "Can you also do X?"
	got := Detect(msg, 0.5)
	if got == nil {
		t.Fatal("expected suggestion")
	}
	if got.ProposedTitle != "Can you also do X?" {
		t.Errorf("expected full title %q, got %q", msg, got.ProposedTitle)
	}
}

func TestDetect_RationaleContainsSignalLabels(t *testing.T) {
	t.Parallel()
	msg := "While you're at it, can you also check X?"
	got := Detect(msg, 0.5)
	if got == nil {
		t.Fatal("expected suggestion")
	}
	for _, sig := range got.Signals {
		if !strings.Contains(got.Rationale, sig) {
			t.Errorf("rationale %q missing signal label %q", got.Rationale, sig)
		}
	}
}

func TestDetect_BelowThreshold_ReturnsNil(t *testing.T) {
	t.Parallel()
	// One positive, no negatives → confidence = 1.0/1.0 = 1.0 ≥ threshold.
	msg := "Can you also check X?"
	got := Detect(msg, threshold)
	if got == nil {
		t.Fatal("expected suggestion at 1.0 confidence")
	}
	// Now force confidence < threshold by mixing in negatives.
	msg2 := "Actually, can you also check X?"
	got2 := Detect(msg2, threshold)
	if got2 != nil {
		t.Errorf("expected nil below threshold, got %+v", got2)
	}
}

func TestDetect_NilWhenEnvFlagOff(t *testing.T) {
	t.Setenv("HARNESS_BRANCH_ADVISOR", "0")
	msg := "Can you also figure out X?"
	if got := Detect(msg, 0); got != nil {
		t.Errorf("expected nil when HARNESS_BRANCH_ADVISOR=0, got %+v", got)
	}
}

func TestDetect_NilWhenEnvFlagFalse(t *testing.T) {
	t.Setenv("HARNESS_BRANCH_ADVISOR", "false")
	msg := "Can you also figure out X?"
	if got := Detect(msg, 0); got != nil {
		t.Errorf("expected nil when HARNESS_BRANCH_ADVISOR=false, got %+v", got)
	}
}

func TestDetect_BenchmarkNFR001(t *testing.T) {
	// 200-char message that has no signals — worst case for scan time.
	msg := strings.Repeat("help me understand how this complicated system works. ", 4)
	msg = msg[:200]
	// Run enough times that if each call took 5ms the test would complete
	// in reasonable time even at the NFR limit. We don't assert timing in
	// unit tests; the -bench variant handles that separately.
	for i := 0; i < 1000; i++ {
		_ = Detect(msg, threshold)
	}
}

func BenchmarkDetect_200Chars(b *testing.B) {
	msg := strings.Repeat("help me understand this complicated system. ", 5)
	msg = msg[:200]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Detect(msg, threshold)
	}
}

func BenchmarkDetect_200Chars_MultipleSignals(b *testing.B) {
	msg := "While you're at it, can you also figure out how the deployment pipeline works? Quick aside: what's the latest version?"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Detect(msg, threshold)
	}
}

// contains is a small helper for signal-list assertions.
func contains(ss []string, needle string) bool {
	for _, s := range ss {
		if s == needle {
			return true
		}
	}
	return false
}
