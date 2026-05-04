package cedar

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingDispatcher captures every Dispatch call for assertion.
type recordingDispatcher struct {
	mu      sync.Mutex
	emits   []capturedEmit
	emitted chan struct{}
}

type capturedEmit struct {
	topic   string
	payload PendingRequest
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{emitted: make(chan struct{}, 16)}
}

func (r *recordingDispatcher) Dispatch(_ context.Context, topic string, payload PendingRequest) {
	r.mu.Lock()
	r.emits = append(r.emits, capturedEmit{topic: topic, payload: payload})
	r.mu.Unlock()
	select {
	case r.emitted <- struct{}{}:
	default:
	}
}

func (r *recordingDispatcher) snapshot() []capturedEmit {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]capturedEmit, len(r.emits))
	copy(out, r.emits)
	return out
}

// makeBashSurface returns a representative bash surface for tests.
func makeBashSurface(pattern string) PromptSurface {
	return PromptSurface{
		Bash: &BashPromptSurface{
			Pattern: pattern,
			Argv:    []string{pattern},
		},
		SessionID: "sess-1",
	}
}

// TestRequestInteractive_BlocksUntilResolution covers the happy path:
// a goroutine simulates the rpc layer calling Resolve, and
// RequestInteractive returns the resolution.
func TestRequestInteractive_BlocksUntilResolution(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp))

	done := make(chan Resolution, 1)
	go func() {
		res, err := r.RequestInteractive(context.Background(), makeBashSurface("git status"))
		if err != nil {
			t.Errorf("RequestInteractive: %v", err)
		}
		done <- res
	}()

	// Wait until the dispatcher recorded the emit so we know the
	// request id is registered.
	select {
	case <-disp.emitted:
	case <-time.After(time.Second):
		t.Fatal("dispatcher never received the prompt")
	}

	emits := disp.snapshot()
	if len(emits) != 1 {
		t.Fatalf("want 1 emit, got %d", len(emits))
	}
	if emits[0].topic != TopicBashPermissionPending {
		t.Fatalf("topic = %q, want %q", emits[0].topic, TopicBashPermissionPending)
	}
	id := emits[0].payload.RequestID
	if id == "" {
		t.Fatal("request id empty")
	}

	// Simulate the user clicking Allow always.
	if err := r.Resolve(id, DecisionAllowAlways); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-done:
		if res.Decision != DecisionAllowAlways {
			t.Fatalf("decision = %q, want %q", res.Decision, DecisionAllowAlways)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestInteractive did not return after Resolve")
	}

	// Leak check.
	if r.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", r.PendingCount())
	}
}

// TestRequestInteractive_TimeoutFiresDeny — when no Resolve arrives
// within the deadline, the registry resolves as Deny + "timeout".
func TestRequestInteractive_TimeoutFiresDeny(t *testing.T) {
	t.Parallel()
	r := NewRegistry(WithTimeout(50 * time.Millisecond))

	res, err := r.RequestInteractive(context.Background(), makeBashSurface("rm -rf /"))
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want %q", res.Decision, DecisionDeny)
	}
	if res.Reason != "timeout" {
		t.Fatalf("reason = %q, want \"timeout\"", res.Reason)
	}
	if r.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", r.PendingCount())
	}
}

// TestRequestInteractive_QueueOverflow — at capacity, a new request
// is auto-denied with reason "queue overflow" and never enters the
// pending map.
func TestRequestInteractive_QueueOverflow(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithTimeout(2*time.Second))

	// Saturate the queue with PromptQueueCap pending requests.
	var wg sync.WaitGroup
	resolutions := make([]Resolution, PromptQueueCap)
	for i := 0; i < PromptQueueCap; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			res, _ := r.RequestInteractive(context.Background(), makeBashSurface("cmd"+string(rune('a'+idx))))
			resolutions[idx] = res
		}()
	}
	// Wait until all PromptQueueCap requests are emitted.
	deadline := time.Now().Add(time.Second)
	for r.PendingCount() < PromptQueueCap {
		if time.Now().After(deadline) {
			t.Fatalf("queue never saturated; pending=%d", r.PendingCount())
		}
		time.Sleep(time.Millisecond)
	}

	// The next request must overflow.
	res, err := r.RequestInteractive(context.Background(), makeBashSurface("overflow"))
	if err != nil {
		t.Fatalf("RequestInteractive (overflow): %v", err)
	}
	if res.Decision != DecisionDeny || res.Reason != "queue overflow" {
		t.Fatalf("overflow res = %+v, want Deny/queue overflow", res)
	}
	if r.PendingCount() != PromptQueueCap {
		t.Fatalf("pending after overflow = %d, want %d", r.PendingCount(), PromptQueueCap)
	}

	// Drain the saturated requests so the test exits cleanly.
	emits := disp.snapshot()
	for _, em := range emits {
		_ = r.Resolve(em.payload.RequestID, DecisionDeny)
	}
	wg.Wait()

	if r.PendingCount() != 0 {
		t.Fatalf("pending after drain = %d, want 0", r.PendingCount())
	}
}

// TestRequestInteractive_AllowOnceSeedsTransient — a user-driven
// Allow-once seeds the cache so a second request for the same
// resource short-circuits with Allow without re-prompting.
func TestRequestInteractive_AllowOnceSeedsTransient(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp))

	// First request — user clicks Allow once.
	go func() {
		select {
		case <-disp.emitted:
			emits := disp.snapshot()
			_ = r.Resolve(emits[0].payload.RequestID, DecisionAllowOnce)
		case <-time.After(time.Second):
		}
	}()

	res1, err := r.RequestInteractive(context.Background(), makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("first RequestInteractive: %v", err)
	}
	if res1.Decision != DecisionAllowOnce {
		t.Fatalf("res1 = %q, want %q", res1.Decision, DecisionAllowOnce)
	}

	// Second request — must short-circuit; no new emit.
	emitsBefore := len(disp.snapshot())
	res2, err := r.RequestInteractive(context.Background(), makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("second RequestInteractive: %v", err)
	}
	if res2.Decision != DecisionAllowOnce {
		t.Fatalf("res2 = %q, want %q (transient cache)", res2.Decision, DecisionAllowOnce)
	}
	emitsAfter := len(disp.snapshot())
	if emitsAfter != emitsBefore {
		t.Fatalf("transient hit emitted: before=%d after=%d", emitsBefore, emitsAfter)
	}

	// Different resource still prompts.
	go func() {
		// Wait for the new emit then Deny it.
		var prev = emitsAfter
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			cur := len(disp.snapshot())
			if cur > prev {
				emits := disp.snapshot()
				_ = r.Resolve(emits[len(emits)-1].payload.RequestID, DecisionDeny)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	res3, err := r.RequestInteractive(context.Background(), makeBashSurface("rm"))
	if err != nil {
		t.Fatalf("third RequestInteractive: %v", err)
	}
	if res3.Decision != DecisionDeny {
		t.Fatalf("res3 (different resource) = %q, want Deny", res3.Decision)
	}

	// Leak check.
	if r.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", r.PendingCount())
	}
}

// TestRequestInteractive_ContextCancel — caller's ctx cancels;
// pending entry cleaned up; Resolve later returns ErrUnknownRequest.
func TestRequestInteractive_ContextCancel(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithTimeout(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Resolution, 1)
	go func() {
		res, _ := r.RequestInteractive(ctx, makeBashSurface("sleep"))
		done <- res
	}()

	select {
	case <-disp.emitted:
	case <-time.After(time.Second):
		t.Fatal("dispatcher never received emit")
	}

	id := disp.snapshot()[0].payload.RequestID
	cancel()

	select {
	case res := <-done:
		if res.Decision != DecisionDeny || res.Reason != "ctx canceled" {
			t.Fatalf("res = %+v, want Deny/ctx canceled", res)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestInteractive did not return after cancel")
	}

	if r.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", r.PendingCount())
	}

	// Late Resolve from a stale modal must return ErrUnknownRequest.
	if err := r.Resolve(id, DecisionAllowAlways); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("Resolve after cancel: %v, want ErrUnknownRequest", err)
	}
}

// TestRequestInteractive_InvalidSurface — zero or multi-variant
// surface returns ErrInvalidSurface synchronously.
func TestRequestInteractive_InvalidSurface(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	// Zero variants.
	res, err := r.RequestInteractive(context.Background(), PromptSurface{})
	if !errors.Is(err, ErrInvalidSurface) {
		t.Fatalf("err = %v, want ErrInvalidSurface", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("res = %+v, want Deny", res)
	}

	// Two variants.
	res, err = r.RequestInteractive(context.Background(), PromptSurface{
		Bash: &BashPromptSurface{Pattern: "ls"},
		Tool: &ToolPromptSurface{ToolName: "fs"},
	})
	if !errors.Is(err, ErrInvalidSurface) {
		t.Fatalf("err = %v, want ErrInvalidSurface", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("res = %+v, want Deny", res)
	}
}

// TestResolve_Validation — invalid decisions are rejected.
func TestResolve_Validation(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Resolve("rid-nope", "bogus"); err == nil {
		t.Fatal("Resolve with bogus decision: want error")
	}
	if err := r.Resolve("rid-nope", DecisionAllowAlways); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("Resolve unknown id: %v, want ErrUnknownRequest", err)
	}
}

// TestRegistry_ConcurrentSafety — many goroutines firing prompts and
// resolutions concurrently. Asserts no leaks and no race (run with
// -race).
func TestRegistry_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithTimeout(2*time.Second))

	const N = 32
	var wg sync.WaitGroup
	var resolved atomic.Int64

	for i := 0; i < N; i++ {
		wg.Add(2)
		idx := i

		// One goroutine fires; one resolves. Each prompt has a unique
		// pattern so the transient cache doesn't short-circuit.
		go func() {
			defer wg.Done()
			pattern := "cmd-" + itoa(idx)
			res, _ := r.RequestInteractive(context.Background(), makeBashSurface(pattern))
			if res.Decision == DecisionDeny && res.Reason == "queue overflow" {
				// overflow is a valid outcome under contention; still
				// counted as resolved.
			}
			resolved.Add(1)
		}()
		go func() {
			defer wg.Done()
			// Spin until SOMETHING is in the queue, then resolve the
			// oldest. We don't care about ordering; the point is the
			// race detector has to be clean.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				emits := disp.snapshot()
				if len(emits) > 0 {
					for _, em := range emits {
						_ = r.Resolve(em.payload.RequestID, DecisionAllowOnce)
					}
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Drain anything still pending (timeouts will clean them up
	// eventually; the leak check here just confirms no eternal entries).
	deadline := time.Now().Add(3 * time.Second)
	for r.PendingCount() > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("registry still has %d pending after 3s", r.PendingCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resolved.Load() == 0 {
		t.Fatal("no goroutines resolved")
	}
}

// itoa is the tiny int-to-string helper used by the contention test;
// avoids pulling strconv into the surface area.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestClearTransientGrants — verifies the engine-reload seam.
func TestClearTransientGrants(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	// Seed a transient via direct Resolve simulation.
	disp := newRecordingDispatcher()
	r.dispatcher = disp
	go func() {
		<-disp.emitted
		emits := disp.snapshot()
		_ = r.Resolve(emits[0].payload.RequestID, DecisionAllowOnce)
	}()
	if _, err := r.RequestInteractive(context.Background(), makeBashSurface("ls")); err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if r.TransientGrantCount() != 1 {
		t.Fatalf("transient = %d, want 1", r.TransientGrantCount())
	}
	r.ClearTransientGrants()
	if r.TransientGrantCount() != 0 {
		t.Fatalf("transient after clear = %d, want 0", r.TransientGrantCount())
	}
}

// TestPromptSurface_Family — the discriminator returns the right
// family for each variant and empty for malformed surfaces.
func TestPromptSurface_Family(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		surface PromptSurface
		want    Family
	}{
		{"bash", PromptSurface{Bash: &BashPromptSurface{Pattern: "ls"}}, FamilyBash},
		{"fs", PromptSurface{FS: &FSPromptSurface{Op: "read", CanonicalPath: "/tmp"}}, FamilyFilesystem},
		{"cred", PromptSurface{Cred: &CredPromptSurface{ProviderID: "openai", Purpose: "provider_call"}}, FamilyCredential},
		{"tool", PromptSurface{Tool: &ToolPromptSurface{ToolName: "fs"}}, FamilyTool},
		{"empty", PromptSurface{}, ""},
		{"two", PromptSurface{Bash: &BashPromptSurface{}, Tool: &ToolPromptSurface{}}, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.surface.Family(); got != tc.want {
				t.Fatalf("Family() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAllTopics — verifies the registered topic list matches the
// spec's four user-visible names exactly.
func TestAllTopics(t *testing.T) {
	t.Parallel()
	got := AllTopics()
	want := []string{
		"bash:permission-pending",
		"fs:permission-pending",
		"cred:permission-pending",
		"tool:permission-pending",
	}
	if len(got) != len(want) {
		t.Fatalf("len(AllTopics()) = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("topic[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// ── WP05: autonomy-dial posture wiring tests ─────────────────────────────────

// TestPostureAutoAllow_SkipsDispatchAndReturnsAllow confirms that a
// registry constructed with PostureAutoAllow resolves every
// RequestInteractive call immediately as Allow without dispatching any
// broker event and without touching the pending-request map.
// This covers the "autonomous" / "bold" tier fast-path (WP05).
func TestPostureAutoAllow_SkipsDispatchAndReturnsAllow(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithPosture(PostureAutoAllow))

	res, err := r.RequestInteractive(context.Background(), makeBashSurface("git status"))
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q", res.Decision, DecisionAllowOnce)
	}
	// No broker event must have been dispatched.
	if got := len(disp.snapshot()); got != 0 {
		t.Fatalf("dispatch count = %d, want 0 (auto-allow should skip dispatch)", got)
	}
	// No pending entries (the auto-allow path never enqueues).
	if r.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", r.PendingCount())
	}
}

// TestPostureAutoAllow_MultipleCallsAllAutoResolve confirms that repeated
// calls all return Allow without interaction, simulating the autonomous
// tier across many tool calls in one turn.
func TestPostureAutoAllow_MultipleCallsAllAutoResolve(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithPosture(PostureAutoAllow))

	patterns := []string{"ls", "cat /etc/hosts", "git log", "npm install", "curl https://example.com"}
	for _, p := range patterns {
		res, err := r.RequestInteractive(context.Background(), makeBashSurface(p))
		if err != nil {
			t.Fatalf("pattern %q: RequestInteractive: %v", p, err)
		}
		if res.Decision != DecisionAllowOnce {
			t.Fatalf("pattern %q: decision = %q, want Allow", p, res.Decision)
		}
	}
	if got := len(disp.snapshot()); got != 0 {
		t.Fatalf("dispatch count = %d, want 0", got)
	}
}

// TestPostureAlwaysPrompt_SkipsTransientCache confirms that a registry
// with PostureAlwaysPrompt does NOT short-circuit on a cached Allow-once
// grant — every call surfaces to the UI regardless. This covers the
// "strict" / "cautious" tier (WP05).
func TestPostureAlwaysPrompt_SkipsTransientCache(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithPosture(PostureAlwaysPrompt))

	// First request — resolve as Allow-once (seeds transient cache).
	go func() {
		select {
		case <-disp.emitted:
			emits := disp.snapshot()
			_ = r.Resolve(emits[0].payload.RequestID, DecisionAllowOnce)
		case <-time.After(2 * time.Second):
		}
	}()
	res1, err := r.RequestInteractive(context.Background(), makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("first RequestInteractive: %v", err)
	}
	if res1.Decision != DecisionAllowOnce {
		t.Fatalf("res1 = %q, want Allow-once", res1.Decision)
	}
	// Transient cache has an entry — but PostureAlwaysPrompt must ignore it.
	if r.TransientGrantCount() != 1 {
		t.Fatalf("transient count = %d, want 1", r.TransientGrantCount())
	}

	// Second request for the SAME resource: must surface again (not cache-hit).
	emitsBefore := len(disp.snapshot())
	go func() {
		// Wait until a new emit appears, then deny it.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(disp.snapshot()) > emitsBefore {
				emits := disp.snapshot()
				_ = r.Resolve(emits[len(emits)-1].payload.RequestID, DecisionDeny)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	res2, err := r.RequestInteractive(context.Background(), makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("second RequestInteractive: %v", err)
	}
	// Deny proves the cache was skipped — if it had hit the cache it would
	// have returned DecisionAllowOnce immediately.
	if res2.Decision != DecisionDeny {
		t.Fatalf("res2 = %q, want Deny (cache must have been skipped)", res2.Decision)
	}
	if got := len(disp.snapshot()); got <= emitsBefore {
		t.Fatalf("dispatch count = %d, want > %d (second call must have dispatched)", got, emitsBefore)
	}

	// Leak check.
	if r.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", r.PendingCount())
	}
}

// TestSetPosture_DynamicUpdate confirms that SetPosture on a live
// registry takes effect on the next RequestInteractive call.
func TestSetPosture_DynamicUpdate(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithTimeout(50*time.Millisecond))

	// Default posture: prompts surface (will timeout).
	if r.Posture() != PostureDefault {
		t.Fatalf("initial posture = %q, want %q", r.Posture(), PostureDefault)
	}
	res1, _ := r.RequestInteractive(context.Background(), makeBashSurface("cmd"))
	if res1.Decision != DecisionDeny || res1.Reason != "timeout" {
		t.Fatalf("res1 = %+v, want Deny/timeout under default posture", res1)
	}

	// Switch to auto-allow.
	r.SetPosture(PostureAutoAllow)
	if r.Posture() != PostureAutoAllow {
		t.Fatalf("posture after Set = %q, want %q", r.Posture(), PostureAutoAllow)
	}
	res2, err := r.RequestInteractive(context.Background(), makeBashSurface("cmd"))
	if err != nil {
		t.Fatalf("RequestInteractive after SetPosture: %v", err)
	}
	if res2.Decision != DecisionAllowOnce {
		t.Fatalf("res2 = %q, want Allow after auto-allow posture set", res2.Decision)
	}
}
