package tokenizer

import (
	"strings"
	"testing"
)

// TestCountRequestTokens_Empty verifies the documented edge case:
// empty system prompt and zero messages still return the framing
// overhead for the system slot (4 tokens). This is intentional —
// providers always reserve a system slot — and the test pins it so a
// future "skip empty system" tweak doesn't silently change trigger
// math.
func TestCountRequestTokens_Empty(t *testing.T) {
	got := CountRequestTokens("", nil)
	if got != messageFramingOverhead {
		t.Fatalf("empty: got %d tokens, want %d (system framing)", got, messageFramingOverhead)
	}
}

// TestCountRequestTokens_ASCIIBudget uses a 1000-rune ASCII string and
// asserts the estimate is within ±25 of the rune/4 expectation. This
// matches the "±25% of a real tokenizer" budget called out in plan
// §R6 — for our test we substitute the rune/4 expectation since we
// don't have tiktoken in the project.
func TestCountRequestTokens_ASCIIBudget(t *testing.T) {
	const runeCount = 1000
	body := strings.Repeat("a", runeCount)

	got := CountRequestTokens("", []Message{{Role: "user", Content: body}})

	// Expectation: rune/4 for the body + framing for system slot +
	// framing for the one user message = 250 + 4 + 4 = 258. The ±25
	// budget here is the test's stand-in for "±25% of real tiktoken".
	const want = runeCount/runesPerToken + 2*messageFramingOverhead
	const tolerance = 25
	if got < want-tolerance || got > want+tolerance {
		t.Fatalf("ASCII 1000 runes: got %d, want %d ±%d", got, want, tolerance)
	}
}

// TestCountRequestTokens_PerMessageFraming asserts each message adds
// the framing overhead exactly once. We compare a single 80-rune
// message against four 20-rune messages: same total content (and
// identical per-piece rune counts since 80 and 20 both divide evenly
// by runesPerToken so the ceil rounding doesn't perturb the test),
// but the multi-message version carries three additional framings.
func TestCountRequestTokens_PerMessageFraming(t *testing.T) {
	single := []Message{{Role: "user", Content: strings.Repeat("a", 80)}}
	multi := []Message{
		{Role: "user", Content: strings.Repeat("a", 20)},
		{Role: "assistant", Content: strings.Repeat("b", 20)},
		{Role: "user", Content: strings.Repeat("c", 20)},
		{Role: "assistant", Content: strings.Repeat("d", 20)},
	}

	gotSingle := CountRequestTokens("", single)
	gotMulti := CountRequestTokens("", multi)

	// Same content → same content-token contribution (80/4 = 20,
	// 4 * (20/4) = 20). Multi has 3 extra framings.
	if want := gotSingle + 3*messageFramingOverhead; gotMulti != want {
		t.Fatalf("multi %d, single %d, diff %d, want diff %d (3 framings)",
			gotMulti, gotSingle, gotMulti-gotSingle, 3*messageFramingOverhead)
	}
}

// TestCountRequestTokens_SystemPromptCounts ensures the system prompt
// contributes its content tokens, not just framing. A 200-rune system
// prompt should add roughly 50 content tokens on top of framing.
func TestCountRequestTokens_SystemPromptCounts(t *testing.T) {
	without := CountRequestTokens("", nil)
	with := CountRequestTokens(strings.Repeat("a", 200), nil)

	const wantContent = 200 / runesPerToken
	if diff := with - without; diff != wantContent {
		t.Fatalf("system prompt diff: got %d tokens, want %d", diff, wantContent)
	}
}

// TestCountRequestTokens_UnicodeIsRunesNotBytes is a sanity check:
// utf8.RuneCountInString must be used so multi-byte characters count
// as one "character" for the heuristic, not 2-4. A naive len()
// implementation would inflate the token count for non-ASCII text.
func TestCountRequestTokens_UnicodeIsRunesNotBytes(t *testing.T) {
	// 100 copies of a 3-byte rune = 100 runes, 300 bytes.
	body := strings.Repeat("界", 100)
	got := CountRequestTokens("", []Message{{Role: "user", Content: body}})

	// Expected = 100/4 + 4 (system) + 4 (user) = 33.
	const want = 100/runesPerToken + 2*messageFramingOverhead
	if got != want {
		t.Fatalf("unicode: got %d, want %d (rune-count, not byte-count)", got, want)
	}
}

// TestCountRequestTokens_EmptyMessageStillFrames pins the rule that an
// empty-content message still contributes framing overhead. This
// matches the "providers reserve a slot per message" reasoning and
// keeps trigger math honest when a tool call returns an empty string.
func TestCountRequestTokens_EmptyMessageStillFrames(t *testing.T) {
	got := CountRequestTokens("", []Message{{Role: "tool", Content: ""}})
	const want = 2 * messageFramingOverhead // system slot + empty tool message
	if got != want {
		t.Fatalf("empty message: got %d, want %d", got, want)
	}
}

// TestCeilDiv guards the rounding-up behavior: 1 rune ought to count
// as 1 token, not 0. The heuristic rounding up keeps us from
// underestimating just enough to cross the trigger threshold.
func TestCeilDiv(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{0, 4, 0},
		{1, 4, 1},
		{3, 4, 1},
		{4, 4, 1},
		{5, 4, 2},
		{1000, 4, 250},
		{1001, 4, 251},
	}
	for _, c := range cases {
		got := ceilDiv(c.a, c.b)
		if got != c.want {
			t.Fatalf("ceilDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestCeilDiv_NonPositiveDivisor is a defensive check: a zero or
// negative divisor returns 0 rather than panicking. The constant
// runesPerToken is positive, but the helper is exposed in this
// package so the contract should be explicit.
func TestCeilDiv_NonPositiveDivisor(t *testing.T) {
	if got := ceilDiv(100, 0); got != 0 {
		t.Fatalf("ceilDiv(100, 0) = %d, want 0", got)
	}
	if got := ceilDiv(100, -1); got != 0 {
		t.Fatalf("ceilDiv(100, -1) = %d, want 0", got)
	}
}
