package chat

// The confirm-each decision ladder at the adapter seam
// (confirm-each-enforcement-01PMAG05 WP02 / WP03 / WP05).
//
// Six branches can decide a `confirm_each` verdict. Each one is a way a
// tool call can dispatch WITHOUT the user seeing a prompt, which is why
// each one gets a test that proves it decided AND an audit record that
// says which one it was. The gap this mission repaired was a control
// that decided silently; a branch with no record is the same gap.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// recordingAudit captures emitted events. The adapter emits from the
// dispatch goroutine while the test body reads, so access is
// mutex-guarded and reads go through a snapshot (CLAUDE.md).
type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAudit) Emit(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingAudit) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Event, len(r.events))
	copy(out, r.events)
	return out
}

// decisions returns the decoded KindToolConfirmDecision payloads.
func (r *recordingAudit) decisions(t *testing.T) []audit.ToolConfirmDecisionPayload {
	t.Helper()
	var out []audit.ToolConfirmDecisionPayload
	for _, e := range r.snapshot() {
		if e.Kind != audit.KindToolConfirmDecision {
			continue
		}
		var p audit.ToolConfirmDecisionPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode decision payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func (r *recordingAudit) grants(t *testing.T) []audit.ToolConfirmGrantWrittenPayload {
	t.Helper()
	var out []audit.ToolConfirmGrantWrittenPayload
	for _, e := range r.snapshot() {
		if e.Kind != audit.KindToolConfirmGrantWritten {
			continue
		}
		var p audit.ToolConfirmGrantWrittenPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode grant payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// fakeGrantStore is a PersistentGrantStore backed by a map, standing in
// for the chassis's Cedar-file store.
type fakeGrantStore struct {
	mu      sync.Mutex
	granted map[string]bool
	err     error
	writes  int
}

func newFakeGrantStore() *fakeGrantStore {
	return &fakeGrantStore{granted: map[string]bool{}}
}

func (s *fakeGrantStore) HasGrant(server, tool string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.granted[server+"__"+tool]
}

func (s *fakeGrantStore) WriteGrant(server, tool string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if s.err != nil {
		return s.err
	}
	s.granted[server+"__"+tool] = true
	return nil
}

func (s *fakeGrantStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *fakeGrantStore) revoke(server, tool string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.granted, server+"__"+tool)
}

// ladderFixture drives one adapter through the confirm ladder with a
// scripted answer, recording whether the user was prompted.
type ladderFixture struct {
	pool    *countingToolPool
	perms   *syncPermResolver
	bus     *toolloop.ConfirmBus
	audit   *recordingAudit
	grants  *fakeGrantStore
	session *toolloop.SessionGrantCache
	adapter *kernelToolAdapter

	mu       sync.Mutex
	prompted int
	answer   toolloop.ConfirmDecision
}

// newLadder wires an adapter whose bus answers every prompt with the
// supplied decision, synchronously. deps lets a test omit a layer (nil
// bus for the headless path, nil grants, and so on).
func newLadder(t *testing.T, answer toolloop.ConfirmDecision, mutate func(*toolloop.ConfirmBus, *ladderFixture, *ConfirmDeps)) *ladderFixture {
	t.Helper()
	f := &ladderFixture{
		pool:    &countingToolPool{server: "filesystem", tool: "write_file"},
		perms:   &syncPermResolver{verdict: PermVerdict{Policy: string(toolloop.PolicyConfirmEach), Reason: "confirm each use"}},
		audit:   &recordingAudit{},
		grants:  newFakeGrantStore(),
		session: toolloop.NewSessionGrantCache(),
		answer:  answer,
	}
	f.bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		f.mu.Lock()
		f.prompted++
		d := f.answer
		f.mu.Unlock()
		_ = f.bus.Resolve(req.SessionID, req.CallID, d)
	})
	deps := ConfirmDeps{
		SessionGrants: f.session,
		PersistGrants: f.grants,
		Audit:         f.audit,
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	bus := f.bus
	if mutate != nil {
		mutate(bus, f, &deps)
	}
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-ladder").
		withConfirm(f.bus).
		withConfirmDeps(deps)
	return f
}

func (f *ladderFixture) promptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompted
}

func (f *ladderFixture) call(t *testing.T) coreag.ToolResult {
	t.Helper()
	res, err := f.adapter.Call(context.Background(), coreag.ToolCall{
		Name: "filesystem__write_file",
		Args: map[string]any{"path": "/etc/hosts"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return res
}

// onlyDecision asserts exactly one decision record was emitted and
// returns it.
func (f *ladderFixture) onlyDecision(t *testing.T) audit.ToolConfirmDecisionPayload {
	t.Helper()
	got := f.audit.decisions(t)
	if len(got) != 1 {
		t.Fatalf("emitted %d decision records, want exactly 1: %+v", len(got), got)
	}
	return got[0]
}

// ── WP03: session grants ───────────────────────────────────────────────

// "Allow for this session" suppresses the SECOND prompt, not the first.
// The user still answers once; that is the whole bargain.
func TestConfirmLadder_SessionGrantSuppressesTheSecondPrompt(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true, RememberSession: true}, nil)

	if res := f.call(t); res.IsError {
		t.Fatalf("first call errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 1 {
		t.Fatalf("first call prompted %d times, want 1", got)
	}

	// Second call: same session, same tool, no prompt.
	if res := f.call(t); res.IsError {
		t.Fatalf("second call errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompt count = %d after the grant, want 1 — the session grant did not suppress the prompt", got)
	}
	if n := len(f.pool.dispatched()); n != 2 {
		t.Fatalf("dispatched %d calls, want 2", n)
	}

	decisions := f.audit.decisions(t)
	if len(decisions) != 2 {
		t.Fatalf("emitted %d decision records, want 2", len(decisions))
	}
	if decisions[0].Path != audit.ToolConfirmPathPrompted || !decisions[0].RememberSession {
		t.Errorf("first record = %+v, want prompted with RememberSession", decisions[0])
	}
	if decisions[1].Path != audit.ToolConfirmPathSessionGrant {
		t.Errorf("second record path = %q, want %q", decisions[1].Path, audit.ToolConfirmPathSessionGrant)
	}
}

// A grant is per-tool: approving one tool for the session must not
// silently approve its neighbours.
func TestConfirmLadder_SessionGrantIsPerTool(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true, RememberSession: true}, nil)
	f.call(t)
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompt count = %d, want 1", got)
	}

	// A different tool on the same server.
	f.pool.tool = "delete_file"
	res, err := f.adapter.Call(context.Background(), coreag.ToolCall{Name: "filesystem__delete_file"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("second tool errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 2 {
		t.Fatalf("prompt count = %d, want 2 — the session grant leaked to a sibling tool", got)
	}
}

// A denial must never leave a grant behind, even when the answer carries
// the remember flag.
func TestConfirmLadder_DenialNeverGrants(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: false, RememberSession: true, Persist: true}, nil)

	res := f.call(t)
	if !res.IsError {
		t.Fatal("denied call dispatched")
	}
	if f.session.Count() != 0 {
		t.Fatal("a denial wrote a session grant")
	}
	if f.grants.writeCount() != 0 {
		t.Fatal("a denial wrote a durable rule")
	}
}

// ── WP03: durable "always allow" ───────────────────────────────────────

func TestConfirmLadder_PersistWritesExactlyOneRuleAndSuppressesPrompting(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true, Persist: true}, nil)

	if res := f.call(t); res.IsError {
		t.Fatalf("first call errored: %q", res.Content)
	}
	if got := f.grants.writeCount(); got != 1 {
		t.Fatalf("wrote %d durable rules, want exactly 1", got)
	}
	if !f.grants.HasGrant("filesystem", "write_file") {
		t.Fatal("the rule does not cover the tool it was written for")
	}

	// Second call: covered by the persisted rule, no prompt, and no
	// second write.
	if res := f.call(t); res.IsError {
		t.Fatalf("second call errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompt count = %d, want 1 — the persisted rule did not suppress the prompt", got)
	}
	if got := f.grants.writeCount(); got != 1 {
		t.Fatalf("wrote %d rules, want 1 — the second call re-persisted", got)
	}

	decisions := f.audit.decisions(t)
	if decisions[0].Path != audit.ToolConfirmPathPrompted || !decisions[0].Persisted {
		t.Errorf("first record = %+v, want prompted with Persisted", decisions[0])
	}
	if decisions[1].Path != audit.ToolConfirmPathPersistedGrant {
		t.Errorf("second record path = %q, want %q", decisions[1].Path, audit.ToolConfirmPathPersistedGrant)
	}
	if g := f.audit.grants(t); len(g) != 1 || !g[0].Written {
		t.Errorf("grant records = %+v, want one Written=true", g)
	}
}

// Revocation restores prompting. The store is consulted on every call
// rather than cached, so a rule revoked from Settings takes effect on the
// next tool call with no restart.
func TestConfirmLadder_RevokingThePersistedRuleRestoresPrompting(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true, Persist: true}, nil)
	f.call(t)
	f.call(t)
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompt count = %d before revocation, want 1", got)
	}

	f.grants.revoke("filesystem", "write_file")

	f.call(t)
	if got := f.promptCount(); got != 2 {
		t.Fatalf("prompt count = %d after revocation, want 2 — revoking did not restore prompting", got)
	}
}

// A persist that fails is audited as Written=false and the call still
// dispatches (the user DID approve it). What must not happen is a failed
// write masquerading as a grant.
func TestConfirmLadder_FailedPersistIsAuditedAsNotWritten(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true, Persist: true}, func(_ *toolloop.ConfirmBus, f *ladderFixture, _ *ConfirmDeps) {
		f.grants.err = errWriteFailed
	})

	if res := f.call(t); res.IsError {
		t.Fatalf("approved call did not dispatch after a failed persist: %q", res.Content)
	}
	g := f.audit.grants(t)
	if len(g) != 1 || g[0].Written {
		t.Fatalf("grant records = %+v, want one Written=false", g)
	}
	if g[0].Error == "" {
		t.Error("failed grant record carried no error class")
	}
	d := f.onlyDecision(t)
	if d.Persisted {
		t.Error("decision claimed Persisted after the write failed")
	}
	// And the next call prompts again, because nothing was persisted.
	f.call(t)
	if got := f.promptCount(); got != 2 {
		t.Fatalf("prompt count = %d, want 2 — a failed persist silently suppressed the next prompt", got)
	}
}

var errWriteFailed = &writeFailedError{}

type writeFailedError struct{}

func (*writeFailedError) Error() string { return "disk full" }

// ── FR-006: the Settings toggle ────────────────────────────────────────

// Toggle OFF: the prompt is never offered and confirm_each behaves as
// auto-allow — documented, and audited so the widened posture is
// recoverable from the trail.
func TestConfirmLadder_ToggleOffAutoAllowsAndAudits(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: false}, func(_ *toolloop.ConfirmBus, _ *ladderFixture, d *ConfirmDeps) {
		d.Enabled = func() bool { return false }
	})

	res := f.call(t)
	if res.IsError {
		t.Fatalf("toggle-off call errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompt count = %d with confirm-each disabled, want 0", got)
	}
	if n := len(f.pool.dispatched()); n != 1 {
		t.Fatalf("dispatched %d calls, want 1", n)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathToggleOff {
		t.Fatalf("path = %q, want %q", d.Path, audit.ToolConfirmPathToggleOff)
	}
	if !d.Approved {
		t.Error("toggle-off record says the call was not approved")
	}
}

// A nil Enabled is "enabled". A caller that forgot to wire the toggle
// must get the prompt, never the silent path.
func TestConfirmLadder_NilToggleMeansEnabled(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)
	f.call(t)
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompt count = %d with a nil toggle, want 1", got)
	}
}

// ── WP05: headless policy ──────────────────────────────────────────────

// No prompt channel + default policy ⇒ deny with an audit record.
func TestConfirmLadder_HeadlessDefaultDenies(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{}, func(_ *toolloop.ConfirmBus, f *ladderFixture, _ *ConfirmDeps) {
		f.bus = nil // no bus at all: no channel to ask on.
	})
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-headless").
		withConfirm(nil).
		withConfirmDeps(ConfirmDeps{Audit: f.audit})

	res := f.call(t)
	if !res.IsError {
		t.Fatal("headless confirm_each dispatched — the default must be deny")
	}
	if !strings.Contains(res.Content, "deny") {
		t.Errorf("deny reason does not name the policy: %q", res.Content)
	}
	if n := len(f.pool.dispatched()); n != 0 {
		t.Fatalf("dispatched %d calls under a headless deny", n)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathHeadlessPolicy {
		t.Fatalf("path = %q, want %q", d.Path, audit.ToolConfirmPathHeadlessPolicy)
	}
	if d.Approved {
		t.Error("headless deny recorded as approved")
	}
}

// The explicit operator override allows — and is audited. This is the
// only path on which a confirm_each verdict dispatches unasked without a
// user or a posture saying so, which is exactly why it must leave a
// record.
func TestConfirmLadder_HeadlessExplicitAllowIsAudited(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{}, nil)
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-headless-allow").
		withConfirm(nil).
		withConfirmDeps(ConfirmDeps{
			Audit:    f.audit,
			Headless: toolloop.HeadlessAllow,
		})

	res := f.call(t)
	if res.IsError {
		t.Fatalf("explicit headless allow denied: %q", res.Content)
	}
	if n := len(f.pool.dispatched()); n != 1 {
		t.Fatalf("dispatched %d calls, want 1", n)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathHeadlessPolicy || !d.Approved {
		t.Fatalf("record = %+v, want an approved headless_policy record", d)
	}
}

// A bus constructed with no publisher can park but cannot ask. Treating
// that as "there is a channel" would hang the run instead of applying the
// deployment's policy.
func TestConfirmLadder_BusWithoutPublisherIsHeadless(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{}, nil)
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-nopub").
		withConfirm(toolloop.NewConfirmBus(nil)).
		withConfirmDeps(ConfirmDeps{Audit: f.audit})

	res := f.call(t)
	if !res.IsError {
		t.Fatal("a publisher-less bus was treated as a live prompt channel")
	}
	if d := f.onlyDecision(t); d.Path != audit.ToolConfirmPathHeadlessPolicy {
		t.Fatalf("path = %q, want %q", d.Path, audit.ToolConfirmPathHeadlessPolicy)
	}
}

// An explicit operator declaration (HeadlessExplicit) applies the
// headless policy even though a LIVE prompt channel is attached. This is
// the production shape: every shipped binary attaches a broker publisher,
// so HasChannel() is always true and without this leg the env var is an
// inert knob — a served-no-UI deployment would park confirm_each calls
// forever instead of applying its configured default-deny (adversarial
// review 2026-08-13, owner decision 4).
//
// The fixture bus auto-APPROVES any prompt, so if the declaration is
// ignored the call prompts, gets approved, and dispatches — making this
// test fail on exactly that mutant rather than hanging.
func TestConfirmLadder_ExplicitHeadlessDenyBypassesLivePromptChannel(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, func(_ *toolloop.ConfirmBus, _ *ladderFixture, deps *ConfirmDeps) {
		deps.Headless = toolloop.HeadlessDeny
		deps.HeadlessExplicit = true
	})

	res := f.call(t)
	if !res.IsError {
		t.Fatal("explicit headless deny dispatched despite the declaration")
	}
	if !strings.Contains(res.Content, "declared headless by operator") {
		t.Errorf("deny reason does not name the operator declaration: %q", res.Content)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompted %d times under an explicit headless declaration, want 0", got)
	}
	if n := len(f.pool.dispatched()); n != 0 {
		t.Fatalf("dispatched %d calls under an explicit headless deny", n)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathHeadlessPolicy || d.Approved {
		t.Fatalf("record = %+v, want a denied headless_policy record", d)
	}
}

// The allow side of the same declaration: dispatches without prompting,
// with an audit record naming the operator declaration (FR-007).
func TestConfirmLadder_ExplicitHeadlessAllowSkipsLivePromptChannel(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: false}, func(_ *toolloop.ConfirmBus, _ *ladderFixture, deps *ConfirmDeps) {
		deps.Headless = toolloop.HeadlessAllow
		deps.HeadlessExplicit = true
	})

	res := f.call(t)
	if res.IsError {
		t.Fatalf("explicit headless allow denied: %q", res.Content)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompted %d times under an explicit headless declaration, want 0", got)
	}
	if n := len(f.pool.dispatched()); n != 1 {
		t.Fatalf("dispatched %d calls, want 1", n)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathHeadlessPolicy || !d.Approved {
		t.Fatalf("record = %+v, want an approved headless_policy record", d)
	}
	if !strings.Contains(d.Reason, "declared headless by operator") {
		t.Errorf("audit reason does not name the operator declaration: %q", d.Reason)
	}
}

// ── FR-007: coverage ───────────────────────────────────────────────────

// The skip-set path names the classified family, per the review finding:
// an operator needs to know WHICH grant applied, not merely that one did.
func TestConfirmLadder_SkipSetRecordNamesTheFamily(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{}, nil)
	// autoApproveFamilies: [write] covers write_file and nothing else.
	// knobs() is the shared helper from the skip-set tests, so this
	// cannot drift from what the production translation does.
	posture := knobs(autonomy.DestructiveConfirm, autonomy.FamilyWrite)
	f.adapter = newKernelToolAdapter(f.pool, f.perms, "sess-skip").
		withConfirm(f.bus).
		withConfirmDeps(ConfirmDeps{Audit: f.audit}).
		withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return posture })

	res := f.call(t)
	if res.IsError {
		t.Fatalf("skip-set call errored: %q", res.Content)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompt count = %d under an auto-approving posture, want 0", got)
	}
	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathSkipSet {
		t.Fatalf("path = %q, want %q", d.Path, audit.ToolConfirmPathSkipSet)
	}
	if d.Family != toolloop.FamilyWrite {
		t.Fatalf("family = %q, want %q — the record must name which grant applied", d.Family, toolloop.FamilyWrite)
	}
}

// FR-005, the load-bearing one: NEITHER grant layer can override an
// explicit Cedar deny.
//
// Both layers are seeded for exactly the (server, tool) under test, so a
// grant check that ran before the deny switch would find a hit and
// dispatch. The deny verdict must win anyway — and must do so without
// prompting and without emitting a confirm-decision record, because
// there was no confirmation to decide.
//
// This is the ordering the ladder's structure guarantees today (the deny
// case returns from Call before resolveConfirmEach is ever reached), but
// structure is not a test. Verified by mutation: moving the grant checks
// ahead of the permission switch makes this fail on IsError and on the
// dispatch count, while every other test in the suite stays green — which
// is exactly why it needed writing.
func TestConfirmLadder_GrantsNeverOverrideExplicitDeny(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)

	// Seed BOTH remember-layers for this exact tool: the strongest
	// possible "the user already said yes to this".
	f.session.Grant("sess-ladder", "filesystem", "write_file")
	if err := f.grants.WriteGrant("filesystem", "write_file"); err != nil {
		t.Fatalf("seed persistent grant: %v", err)
	}
	if !f.session.Has("sess-ladder", "filesystem", "write_file") || !f.grants.HasGrant("filesystem", "write_file") {
		t.Fatal("precondition: both grant layers must be seeded, or this test proves nothing")
	}

	f.perms.verdict = PermVerdict{Policy: string(toolloop.PolicyDeny), Reason: "denied by policy"}

	res := f.call(t)
	if !res.IsError {
		t.Fatal("a grant overrode an explicit Cedar deny — deny is the floor under every knob and every grant (FR-005)")
	}
	if n := len(f.pool.dispatched()); n != 0 {
		t.Fatalf("dispatched %d calls under an explicit deny", n)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompt count = %d on a deny verdict, want 0", got)
	}
	if got := f.audit.decisions(t); len(got) != 0 {
		t.Fatalf("deny emitted %d confirm-decision records, want 0 — nothing was confirmed: %+v", len(got), got)
	}
}

// Cedar deny short-circuits BEFORE the ladder, so it neither prompts nor
// emits a confirm-decision record: there was no confirmation to decide.
func TestConfirmLadder_CedarDenyNeverReachesTheLadder(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)
	f.perms.verdict = PermVerdict{Policy: string(toolloop.PolicyDeny), Reason: "policy"}

	res := f.call(t)
	if !res.IsError {
		t.Fatal("explicit deny dispatched")
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompt count = %d on a deny verdict, want 0", got)
	}
	if got := f.audit.decisions(t); len(got) != 0 {
		t.Fatalf("deny emitted %d confirm-decision records, want 0: %+v", len(got), got)
	}
}

// Privacy: the audit payload carries names and classification, never a
// rendering of the call's arguments.
func TestConfirmLadder_AuditPayloadCarriesNoArgumentData(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)
	f.call(t)

	for _, e := range f.audit.snapshot() {
		body := string(e.Payload)
		for _, forbidden := range []string{"/etc/hosts", "args_summary", "arguments:"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("audit payload leaked %q: %s", forbidden, body)
			}
		}
	}
}
