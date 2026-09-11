package serve

// wsstream_fleet_topic_drift_test.go — drift guard for the fleet topic
// string literals in wsstream.go's const block
// (topicFleetLockdownChanged / topicFleetSessionExpired).
//
// core/serve is not on scripts/ci/check-no-fleet-imports.sh's allowlist:
// core/fleet is the control-plane client and core/serve is the
// served-mode HTTP/WS server, and the gate exists specifically to keep
// them decoupled (an OSS fork that deletes core/fleet/ must still build
// core/serve/). wsstream.go therefore hand-copies the two fleet topic
// string literals instead of importing corefleet.TopicFleetLockdownChanged
// / corefleet.TopicFleetSessionExpired directly.
//
// A hand-copied literal with no drift check is exactly how the
// three-copy topic-list bug (findings #62/#63, served-topic-single-source)
// happened in the first place — this test is that check for the fleet
// pair specifically. It is safe for a _test.go file in this package to
// import core/fleet: scripts/ci/check-no-fleet-imports.sh drives
// `go list -f '{{join .Imports " "}}'`, whose .Imports field covers only
// a package's non-test source files, not its test files' imports
// (verified against the pre-existing wsstream_gap_topics_test.go, an
// external `package serve_test` file that already imports corefleet
// without tripping the gate).
import (
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestFleetTopicLiterals_MatchCorefleetConstants fails loudly if
// wsstream.go's hand-copied fleet topic literals ever drift from
// core/fleet's exported constants — the source of truth every real
// fleet-side producer publishes on.
func TestFleetTopicLiterals_MatchCorefleetConstants(t *testing.T) {
	if topicFleetLockdownChanged != corefleet.TopicFleetLockdownChanged {
		t.Errorf("wsstream.go's topicFleetLockdownChanged = %q, want corefleet.TopicFleetLockdownChanged = %q",
			topicFleetLockdownChanged, corefleet.TopicFleetLockdownChanged)
	}
	if topicFleetSessionExpired != corefleet.TopicFleetSessionExpired {
		t.Errorf("wsstream.go's topicFleetSessionExpired = %q, want corefleet.TopicFleetSessionExpired = %q",
			topicFleetSessionExpired, corefleet.TopicFleetSessionExpired)
	}

	// Belt-and-braces: also confirm the literals are actually wired into
	// passthroughTopics and processWideTopics (a drift-free literal that
	// nobody registered would still leave the gap findings #62/#63 fixed).
	if !containsString(passthroughTopics, corefleet.TopicFleetLockdownChanged) {
		t.Errorf("passthroughTopics does not contain %q", corefleet.TopicFleetLockdownChanged)
	}
	if !containsString(passthroughTopics, corefleet.TopicFleetSessionExpired) {
		t.Errorf("passthroughTopics does not contain %q", corefleet.TopicFleetSessionExpired)
	}
	if !processWideTopics[corefleet.TopicFleetLockdownChanged] {
		t.Errorf("processWideTopics[%q] = false, want true", corefleet.TopicFleetLockdownChanged)
	}
	if !processWideTopics[corefleet.TopicFleetSessionExpired] {
		t.Errorf("processWideTopics[%q] = false, want true", corefleet.TopicFleetSessionExpired)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
