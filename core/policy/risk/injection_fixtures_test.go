package risk

import (
	"encoding/json"
	"os"
	"testing"
)

// injectionFixture is one committed prompt-injection attempt WP06's
// family-floor test replays. Score/Rationale are what a (hypothetically
// fully successful) injection got the rating model to say; the test
// exercises ApplyFamilyFloor against that WORST CASE rather than
// against a real LLM call, so the property holds deterministically and
// without network access.
type injectionFixture struct {
	Name          string `json:"name"`
	Family        string `json:"family"`
	Tool          string `json:"tool"`
	ArgsPayload   string `json:"args_payload"`
	InjectedScore int    `json:"injected_score"`
}

func loadInjectionFixtures(t *testing.T) []injectionFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/injection_fixtures.json")
	if err != nil {
		t.Fatalf("read injection fixtures: %v", err)
	}
	var fixtures []injectionFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse injection fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("injection_fixtures.json is empty")
	}
	return fixtures
}
