package serve

// passthroughTopics ↔ SERVED_STREAM_TOPICS parity
// (adversarial review 2026-08-13, closing a nit both final reviews left
// open).
//
// The Go side (wsstream.go passthroughTopics) decides which bus topics
// are forwarded to the browser; the TS side (harnessClient.ts
// SERVED_STREAM_TOPICS) decides which forwarded frames are re-published
// onto the served event bus. Both files carry a "keep these in sync"
// comment and nothing enforced it. The failure mode is asymmetric and
// nasty in both directions:
//
//   - topic in Go but not TS: the frame is delivered to the browser and
//     dropped on the floor. For the gate topics (permission pending,
//     confirm pending, elicit pending) that is a tool call parked with
//     no deadline and no visible prompt — a turn that never resumes.
//   - topic in TS but not Go: a component subscribes to an event that
//     can never arrive in served mode, i.e. a desktop-only feature that
//     silently degrades in a workbench.
//
// This test parses the TS array literal out of harnessClient.ts and
// asserts set equality with passthroughTopics. It is deliberately a
// text-level parse: the array is a `] as const` literal of plain string
// literals, and the parse fails loudly (not silently-empty) if that
// shape ever changes.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"testing"
)

func TestPassthroughTopics_MatchServedStreamTopicsTS(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	tsPath := filepath.Join(repoRoot, "frontend", "src", "lib", "harnessClient.ts")
	raw, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read %s: %v", tsPath, err)
	}

	// Isolate the SERVED_STREAM_TOPICS literal so string literals
	// elsewhere in the 4k-line client cannot leak into the set.
	arrRe := regexp.MustCompile(`(?s)export const SERVED_STREAM_TOPICS = \[(.*?)\] as const;`)
	m := arrRe.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("SERVED_STREAM_TOPICS array literal not found in %s — if the declaration shape changed, update this test's regex alongside it", tsPath)
	}
	// Strip // comments before extracting literals: the array's inline
	// commentary contains apostrophes, which would desynchronise a
	// naive quote-pair scan.
	commentRe := regexp.MustCompile(`//[^\n]*`)
	body := commentRe.ReplaceAll(m[1], nil)
	litRe := regexp.MustCompile(`'([^'\n]+)'`)
	tsTopics := map[string]bool{}
	for _, lm := range litRe.FindAllSubmatch(body, -1) {
		tsTopics[string(lm[1])] = true
	}
	if len(tsTopics) == 0 {
		t.Fatal("parsed zero topics out of SERVED_STREAM_TOPICS — the literal shape changed; update the parse")
	}

	goTopics := map[string]bool{}
	for _, topic := range passthroughTopics {
		goTopics[topic] = true
	}

	var missingInTS, missingInGo []string
	for topic := range goTopics {
		if !tsTopics[topic] {
			missingInTS = append(missingInTS, topic)
		}
	}
	for topic := range tsTopics {
		if !goTopics[topic] {
			missingInGo = append(missingInGo, topic)
		}
	}
	sort.Strings(missingInTS)
	sort.Strings(missingInGo)

	if len(missingInTS) > 0 {
		t.Errorf("topics forwarded by core/serve but dropped by the browser (add to SERVED_STREAM_TOPICS in harnessClient.ts): %v", missingInTS)
	}
	if len(missingInGo) > 0 {
		t.Errorf("topics the frontend expects but core/serve never forwards (add to passthroughTopics in wsstream.go): %v", missingInGo)
	}
}
