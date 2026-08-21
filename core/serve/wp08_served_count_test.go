package serve

import "testing"

// wp08_served_count_test.go — served-mode-is-a-real-mode-01PMZ707 WP08,
// SD-13 / AC-719.
//
// docs/served-mode-boundary.md and frontend/src/lib/featureFlags.ts both
// used to hardcode "33" as the size of servedMethods, and both were wrong
// (RAN: len(servedMethods) was 34 by the time this mission started, and
// this WP's own WP04 landing pushed it to 39 — Config_GetFlags,
// Sessions_ResolveAutonomy, Sessions_SuggestTitle). A comment that ages
// silently is the same failure class this mission exists to fix, just in
// prose instead of code — this test is the tripwire the WP04 correction
// promised: change servedMethods' length without touching this constant
// and CI fails here, naming both prose citations to update, instead of the
// count ageing quietly for another release.
//
// *Falsify*: add or remove an entry from servedMethods in methods.go
// without updating wantServedMethodCount below → this goes red.
const wantServedMethodCount = 39

func TestServedMethodsCountMatchesDoc(t *testing.T) {
	got := len(servedMethods)
	if got != wantServedMethodCount {
		t.Fatalf(
			"len(servedMethods) = %d, want %d (wantServedMethodCount in this file). "+
				"servedMethods changed size — update wantServedMethodCount here AND "+
				"the count cited in docs/served-mode-boundary.md (search for "+
				"\"servedMethods allowlist\") in the SAME commit. "+
				"frontend/src/lib/featureFlags.ts's signedIn comment deliberately "+
				"does not restate the number, so it does not need a matching edit.",
			got, wantServedMethodCount,
		)
	}
}
