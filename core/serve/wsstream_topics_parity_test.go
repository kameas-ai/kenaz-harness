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
//
// # Known gap: this test cannot catch a topic missing from BOTH lists
//
// PR #336 review MUST FIX 1 found exactly that shape: mcp:health-changed
// had a real Go publisher (mcp.API.PublishHealthChange, reaching the
// process-wide EventBus) and a real frontend subscriber
// (useHarnessAPI.ts's useEventStream('mcp:health-changed', ...)), but was
// absent from passthroughTopics AND SERVED_STREAM_TOPICS. Set-equality
// between two lists that both omit the same entry is still equality —
// this test passed the whole time the bug was live.
//
// The fix for THAT topic is wsstream_mcp_health_topic_test.go: an
// end-to-end test that drives a real EventBus.Publish through a real
// served.Server + WebSocket connection and asserts the frame is actually
// delivered. It fails red without either list entry (see that file's
// header for the mutation results) — a completeness check for one
// topic, proven the hard way rather than by static parsing.
//
// A GENERAL completeness gate — "every topic with a real frontend
// useEventStream subscriber must appear in passthroughTopics" — was
// considered and deliberately NOT added here, because it is not
// tractable to add safely in this PR's scope. Cross-referencing every
// `Topic* = "..."` Go const's declaring file against
// `useEventStream(...)` call sites in frontend/src (the same two-pass
// technique scripts/ci/check-broker-topic-consumers.sh already uses,
// see its pass 1) surfaces FIVE Go-published, frontend-subscribed
// topics currently missing from passthroughTopics, of which
// mcp:health-changed was only one:
//
//   - contextbootstrap:progress (core/rpc/contextbootstrap_wiring.go)
//   - elicit:deferred, elicit:deferred:answered (core/rpc/views/elicit/api.go)
//   - fleet:lockdown:changed (core/fleet/lockdown.go)
//   - fleet:session:expired (core/fleet/http.go)
//
// Each of these needs the SAME two-part disposition MUST FIX 1 required
// for mcp:health-changed — a passthroughTopics/SERVED_STREAM_TOPICS
// entry, AND a session-scoping decision (does the payload carry a
// session id D-705's filter can key on, or does it need a
// processWideTopics exemption like TopicMigrationDriftDetected and
// mcpview.TopicMCPHealthChanged do?). That is real per-topic research
// this PR's three MUST FIX findings did not ask for and this fix does
// not attempt — a blanket gate that fails on them today would either
// force a rushed, unresearched disposition of four unrelated topics
// (exactly the "allowlist without a real reason" CLAUDE.md's unwired-
// sweep doctrine warns against) or need those four allowlisted with no
// better justification than "out of scope," which is not a dated
// justification naming a blocker and an owner. Recorded here instead,
// as a candidate for its own follow-up mission — see
// docs/unwired-ledger.md's convention for this class of finding.

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
