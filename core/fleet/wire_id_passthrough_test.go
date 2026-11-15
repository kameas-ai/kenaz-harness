package fleet

import (
	"runtime"
	"testing"
)

// Only the exact synthetic shape "<team|org>/_fleet/<uuid>" passes through
// as a wire id; "_fleet/<uuid>" deeper in a member's own path is a file and
// gets its own salted id.
func TestPassThroughWireID_ExactShapeOnly(t *testing.T) {
	const u = "6b2f6f0e-0000-4000-8000-0000000000aa"
	for _, p := range []string{u, "team/_fleet/" + u, "org/_fleet/" + u, "/team/_fleet/" + u, "team/./_fleet/" + u, "team\\_fleet\\" + u} {
		if got, ok := passThroughWireID(p); !ok || got != u {
			t.Errorf("passThroughWireID(%q) = %q, %v; want pass-through", p, got, ok)
		}
	}
	for _, p := range []string{"notes/team/_fleet/" + u, "projects/x/_fleet/" + u, "personal/_fleet/" + u, "team/_fleet/" + u + "/child", "team/_fleet/not-a-uuid"} {
		if _, ok := passThroughWireID(p); ok {
			t.Errorf("passThroughWireID(%q) passed through; want a salted id", p)
		}
	}
	_, ok := passThroughWireID("Team/_FLEET/" + u)
	if want := runtime.GOOS == "darwin" || runtime.GOOS == "windows"; ok != want {
		t.Errorf("Team/_FLEET pass-through = %v on %s, want %v", ok, runtime.GOOS, want)
	}
}
