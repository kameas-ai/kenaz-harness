package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// retargetFixture: work/pl -> work/harmless, with work/other beside it.
func retargetFixture(t *testing.T) (pl, harmless, other string) {
	t.Helper()
	work := t.TempDir()
	harmless, other = filepath.Join(work, "harmless"), filepath.Join(work, "other")
	for _, d := range []string{harmless, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pl = filepath.Join(work, "pl")
	symlinkOrSkip(t, harmless, pl)
	return pl, harmless, other
}

func relink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// A grant binds the location the gate checked when it raised the prompt,
// not wherever the link points when the person answers. Before, grantsFor
// re-resolved the link at answer time: retargeting pl while the prompt
// was open made "Allow always (this directory)" persist a grant on the
// NEW target — a directory the person was never shown.
func TestGrant_BindsTheLocationCheckedAtPromptTime(t *testing.T) {
	t.Parallel()
	pl, _, other := retargetFixture(t)
	rc := newRealChain(t, GateOptions{})
	ch := rc.evalAsync(OpWrite, filepath.Join(pl, "a.txt"))
	p := rc.waitPending(t, 1)[0]
	relink(t, other, pl) // retarget while the prompt is open
	if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatal(err)
	}
	mustAllow(t, ch, "the approved write")

	// pl now points at other; the grant must not cover it.
	ch = rc.evalAsync(OpWrite, filepath.Join(pl, "b.txt"))
	pending := rc.waitPending(t, 1)
	_ = rc.registry.Resolve(pending[0].RequestID, cedar.DecisionDeny)
	if r := <-ch; r.d.Outcome == cedar.Allow {
		t.Fatalf("the grant followed a link retargeted while the prompt was open: %s", r.d.Reason)
	}
	_ = os.Remove(filepath.Join(other, "b.txt"))
}

// Releasing queued prompts after an Allow always judges both sides by
// their check-time locations. Before, GrantCoverage recomputed the grant
// at release time: a queued write behind the retargeted link was released
// (allowed) although the persisted grant did not cover it — the CI flake
// in TestWriteFile_RememberedGrantBindsTheRealDirectory, where the test's
// own next write raced ResolveScoped's releaseCovered.
func TestGrantCoverage_UsesCheckTimeLocations(t *testing.T) {
	t.Parallel()
	pl, _, other := retargetFixture(t)
	rc := newRealChain(t, GateOptions{})
	chA := rc.evalAsync(OpWrite, filepath.Join(pl, "a.txt"))
	a := rc.waitPending(t, 1)[0]
	relink(t, other, pl)
	chB := rc.evalAsync(OpWrite, filepath.Join(pl, "x.txt")) // checked behind the new target
	pending := rc.waitPending(t, 2)
	var b cedar.PendingRequest
	for _, q := range pending {
		if q.RequestID != a.RequestID {
			b = q
		}
	}
	if err := rc.registry.ResolveScoped(a.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatal(err)
	}
	mustAllow(t, chA, "the approved write")
	still := false
	for _, q := range rc.registry.ListPending() {
		if q.RequestID == b.RequestID {
			still = true
		}
	}
	if !still {
		r := <-chB
		t.Fatalf("queued write behind the retargeted link was released by the grant (outcome %s: %s)", r.d.Outcome, r.d.Reason)
	}
	_ = rc.registry.Resolve(b.RequestID, cedar.DecisionDeny)
	<-chB
}

// The queued side is judged by its check-time location too: a write
// checked behind the other target, with the link swung back before the
// release, would do its I/O on the other target (the location it was
// checked on), so the grant on harmless must not release it.
func TestGrantCoverage_QueuedRequestJudgedWhereItWasChecked(t *testing.T) {
	t.Parallel()
	pl, harmless, other := retargetFixture(t)
	rc := newRealChain(t, GateOptions{})
	chA := rc.evalAsync(OpWrite, filepath.Join(pl, "a.txt"))
	a := rc.waitPending(t, 1)[0]
	relink(t, other, pl)
	chB := rc.evalAsync(OpWrite, filepath.Join(pl, "x.txt"))
	pending := rc.waitPending(t, 2)
	relink(t, harmless, pl) // swing back before the release
	var b cedar.PendingRequest
	for _, q := range pending {
		if q.RequestID != a.RequestID {
			b = q
		}
	}
	if err := rc.registry.ResolveScoped(a.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
		t.Fatal(err)
	}
	mustAllow(t, chA, "the approved write")
	still := false
	for _, q := range rc.registry.ListPending() {
		if q.RequestID == b.RequestID {
			still = true
		}
	}
	if !still {
		r := <-chB
		t.Fatalf("queued write checked behind the other target was released (outcome %s: %s)", r.d.Outcome, r.d.Reason)
	}
	_ = rc.registry.Resolve(b.RequestID, cedar.DecisionDeny)
	<-chB
}

// The directory a "this directory" grant binds is taken with the checked
// path, before the Cedar passes. A parent link retargeted after the check
// (afterCheck, the window between the policy passes and the prompt) must
// not move the grant onto the new, unshown target.
func TestGrant_DirectoryIsTheCheckedDirectory(t *testing.T) {
	t.Parallel()
	for _, op := range []Op{OpWrite, OpRead} {
		pl, harmless, other := retargetFixture(t)
		if err := os.WriteFile(filepath.Join(harmless, "a.txt"), []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("b"), 0o600); err != nil {
			t.Fatal(err)
		}
		rc := newRealChain(t, GateOptions{})
		var once bool
		rc.gate.afterCheck = func() {
			if !once {
				once = true
				relink(t, other, pl)
			}
		}
		ch := rc.evalAsync(op, filepath.Join(pl, "a.txt"))
		p := rc.waitPending(t, 1)[0]
		if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeDirectory); err != nil {
			t.Fatal(err)
		}
		mustAllow(t, ch, string(op)+" approved")
		rc.gate.afterCheck = nil
		if entries, _ := os.ReadDir(rc.policyDir); len(entries) != 1 {
			t.Fatalf("%s: policy files = %d, want the one directory grant", op, len(entries))
		}

		// pl -> other now; the grant (on harmless) must not cover it.
		ch = rc.evalAsync(op, filepath.Join(pl, "b.txt"))
		pending := rc.waitPending(t, 1)
		_ = rc.registry.Resolve(pending[0].RequestID, cedar.DecisionDeny)
		if r := <-ch; r.d.Outcome == cedar.Allow {
			t.Fatalf("%s: the directory grant bound the parent retargeted after the check: %s", op, r.d.Reason)
		}
	}
}

// grantsFor judges the checked locations, not only the name: a link into
// the state dir retargeted to an ordinary dir before the answer still
// ends as allow-once, and nothing is remembered.
func TestGrant_CheckedLocationInStateIsNotRemembered(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	policy := filepath.Join(dataDir, "policy")
	harmless := filepath.Join(t.TempDir(), "harmless")
	for _, d := range []string{policy, harmless} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pl := filepath.Join(t.TempDir(), "pl")
	symlinkOrSkip(t, policy, pl)
	rc := newRealChain(t, GateOptions{StateDirs: HarnessStateDirs(dataDir)})
	ch := rc.evalAsync(OpWrite, filepath.Join(pl, "evil.cedar"))
	p := rc.waitPending(t, 1)[0]
	relink(t, harmless, pl) // looks harmless by the time it is answered
	if err := rc.registry.ResolveScoped(p.RequestID, cedar.DecisionAllowAlways, cedar.SourceGuest, cedar.ScopeExact); err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil || r.d.Outcome != cedar.Allow {
		t.Fatalf("outcome = %v %v, want the approved allow-once", r.d.Outcome, r.err)
	}
	if !strings.Contains(r.d.Reason, "allow-once") || !strings.Contains(r.d.Reason, "cannot be remembered") {
		t.Fatalf("reason = %q, want allow-once / cannot be remembered", r.d.Reason)
	}
	if entries, _ := os.ReadDir(rc.policyDir); len(entries) != 0 {
		t.Fatalf("a grant was persisted for a checked location inside state: %v", entries)
	}
}
