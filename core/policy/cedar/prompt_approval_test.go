package cedar

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// --- helpers ---------------------------------------------------------------

// recordingObserver collects ResolvedEvents. Writes arrive on whichever
// goroutine resolved the approval (a decider, the timeout timer, or the
// cancelling task), so every read goes through snapshot().
type recordingObserver struct {
	mu     sync.Mutex
	events []ResolvedEvent
}

func (o *recordingObserver) Resolved(ev ResolvedEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, ev)
}

func (o *recordingObserver) snapshot() []ResolvedEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]ResolvedEvent, len(o.events))
	copy(out, o.events)
	return out
}

// idCapturingDispatcher publishes each dispatched request id on a channel so a
// test can act on an approval the moment it is raised.
type idCapturingDispatcher struct {
	ids chan PendingRequest
}

func newIDCapturingDispatcher() *idCapturingDispatcher {
	return &idCapturingDispatcher{ids: make(chan PendingRequest, 16)}
}

func (d *idCapturingDispatcher) Dispatch(_ context.Context, _ string, p PendingRequest) {
	select {
	case d.ids <- p:
	default:
	}
}

func (d *idCapturingDispatcher) await(t *testing.T) PendingRequest {
	t.Helper()
	select {
	case p := <-d.ids:
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("await: no request dispatched within 3s")
		return PendingRequest{}
	}
}

func toolSurface() PromptSurface {
	return PromptSurface{
		Tool:      &ToolPromptSurface{ServerName: "filesystem", ToolName: "read_file"},
		SessionID: "task-1",
	}
}

// --- ResolveFrom source validation -----------------------------------------

// A source class is provenance the host ledger records as fact. Only the three
// inbound classes may be injected; the registry-synthesised ones must not be
// forgeable by a caller, or "denied by timeout" becomes something a buggy (or
// hostile) host can assert about a decision a human actually made.
func TestResolveFrom_RejectsSynthesisedSources(t *testing.T) {
	t.Parallel()
	for _, src := range []ResolutionSource{SourceTimeout, SourceCancelled, SourceOverflow, "", "nonsense"} {
		reg := NewRegistry()
		err := reg.ResolveFrom("rid-whatever", DecisionAllowOnce, src)
		if err == nil {
			t.Fatalf("source %q: expected rejection, got nil", src)
		}
		if err == ErrUnknownRequest {
			t.Fatalf("source %q: rejected as unknown-request; want an invalid-source error", src)
		}
	}
}

func TestResolveFrom_AcceptsInboundSources(t *testing.T) {
	t.Parallel()
	for _, src := range []ResolutionSource{SourceHost, SourceGuest, SourceRemote} {
		disp := newIDCapturingDispatcher()
		obs := &recordingObserver{}
		reg := NewRegistry(WithDispatcher(disp), WithTimeout(time.Hour))
		reg.AddResolutionObserver(obs)

		done := make(chan Resolution, 1)
		go func() {
			res, _ := reg.RequestInteractive(context.Background(), toolSurface())
			done <- res
		}()
		req := disp.await(t)
		if err := reg.ResolveFrom(req.RequestID, DecisionAllowOnce, src); err != nil {
			t.Fatalf("source %q: ResolveFrom: %v", src, err)
		}
		<-done

		evs := obs.snapshot()
		if len(evs) != 1 {
			t.Fatalf("source %q: want 1 resolution event, got %d", src, len(evs))
		}
		if evs[0].Source != src {
			t.Fatalf("source %q: event carried %q", src, evs[0].Source)
		}
		if evs[0].Decision != DecisionAllowOnce {
			t.Fatalf("source %q: event decision = %q", src, evs[0].Decision)
		}
	}
}

// Resolve (the pre-074 entry point the served :7880 modal calls) must keep
// working AND must be attributed to the guest class — the served UI is a real
// third decider the host cannot observe, and mislabelling it `host` would make
// the host's ledger claim it made a decision it never saw.
func TestResolve_DefaultsToGuestSource(t *testing.T) {
	t.Parallel()
	disp := newIDCapturingDispatcher()
	obs := &recordingObserver{}
	reg := NewRegistry(WithDispatcher(disp), WithTimeout(time.Hour))
	reg.AddResolutionObserver(obs)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = reg.RequestInteractive(context.Background(), toolSurface())
	}()
	req := disp.await(t)
	if err := reg.Resolve(req.RequestID, DecisionDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	<-done

	evs := obs.snapshot()
	if len(evs) != 1 || evs[0].Source != SourceGuest {
		t.Fatalf("want exactly one guest-sourced event, got %+v", evs)
	}
}

// --- additive fan-out ------------------------------------------------------

// AddDispatcher must ADD a listener, never replace the one the served surface
// installed: the whole design rests on one gate with N listeners.
func TestAddDispatcher_FansOutAlongsidePrimary(t *testing.T) {
	t.Parallel()
	primary := newIDCapturingDispatcher()
	extraA := newIDCapturingDispatcher()
	extraB := newIDCapturingDispatcher()

	reg := NewRegistry(WithDispatcher(primary), WithTimeout(time.Hour))
	reg.AddDispatcher(extraA)
	removeB := reg.AddDispatcher(extraB)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = reg.RequestInteractive(context.Background(), toolSurface())
	}()

	pReq := primary.await(t)
	aReq := extraA.await(t)
	bReq := extraB.await(t)
	if pReq.RequestID != aReq.RequestID || aReq.RequestID != bReq.RequestID {
		t.Fatalf("listeners saw different approval ids: %q %q %q",
			pReq.RequestID, aReq.RequestID, bReq.RequestID)
	}
	_ = reg.Resolve(pReq.RequestID, DecisionDeny)
	<-done

	// After removal, B must stop hearing anything while A keeps listening.
	removeB()
	removeB() // idempotent
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_, _ = reg.RequestInteractive(context.Background(), toolSurface())
	}()
	second := primary.await(t)
	_ = extraA.await(t)
	select {
	case got := <-extraB.ids:
		t.Fatalf("removed dispatcher still received %q", got.RequestID)
	case <-time.After(100 * time.Millisecond):
	}
	_ = reg.Resolve(second.RequestID, DecisionDeny)
	<-done2
}

func TestAddResolutionObserver_RemoveStopsDelivery(t *testing.T) {
	t.Parallel()
	disp := newIDCapturingDispatcher()
	obs := &recordingObserver{}
	reg := NewRegistry(WithDispatcher(disp), WithTimeout(time.Hour))
	remove := reg.AddResolutionObserver(obs)
	remove()
	remove() // idempotent

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = reg.RequestInteractive(context.Background(), toolSurface())
	}()
	req := disp.await(t)
	_ = reg.Resolve(req.RequestID, DecisionDeny)
	<-done

	if evs := obs.snapshot(); len(evs) != 0 {
		t.Fatalf("removed observer received %d events", len(evs))
	}
}

// --- exactly-once under every interleaving ---------------------------------

// raceCase drives one interleaving many times. The invariant under test is the
// same in all of them: EXACTLY ONE resolution event per approval id, a source
// drawn from the set of contestants that could legitimately have won, and a
// drained pending map.
func raceCase(t *testing.T, name string, iterations int, timeout time.Duration, contend func(reg *Registry, req PendingRequest, cancel context.CancelFunc), wantSources map[ResolutionSource]bool) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		for i := 0; i < iterations; i++ {
			disp := newIDCapturingDispatcher()
			obs := &recordingObserver{}
			reg := NewRegistry(WithDispatcher(disp), WithTimeout(timeout))
			reg.AddResolutionObserver(obs)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = reg.RequestInteractive(ctx, toolSurface())
			}()

			req := disp.await(t)
			contend(reg, req, cancel)
			<-done
			cancel()

			evs := obs.snapshot()
			if len(evs) != 1 {
				srcs := make([]string, 0, len(evs))
				for _, e := range evs {
					srcs = append(srcs, string(e.Source))
				}
				t.Fatalf("iteration %d: want exactly 1 resolution event, got %d (%s)",
					i, len(evs), strings.Join(srcs, ","))
			}
			ev := evs[0]
			if !wantSources[ev.Source] {
				t.Fatalf("iteration %d: unexpected winning source %q", i, ev.Source)
			}
			if ev.Request.RequestID != req.RequestID {
				t.Fatalf("iteration %d: event names %q, approval was %q",
					i, ev.Request.RequestID, req.RequestID)
			}
			// Every non-decision source is a DENY. There is no auto-allow
			// reachable through timeout, cancellation, or overflow.
			switch ev.Source {
			case SourceTimeout, SourceCancelled, SourceOverflow:
				if ev.Decision != DecisionDeny {
					t.Fatalf("iteration %d: source %q resolved as %q; absence of consent must be denial",
						i, ev.Source, ev.Decision)
				}
			}
			if n := reg.PendingCount(); n != 0 {
				t.Fatalf("iteration %d: pending map leaked %d entries", i, n)
			}
			if n := reg.PendingForFamily(FamilyTool); n != 0 {
				t.Fatalf("iteration %d: per-family counter leaked %d", i, n)
			}
		}
	})
}

// A decision landing at the same instant the fail-closed timer fires. Either
// may win; what must not happen is two resolutions, or a task that observes a
// deny while a surface observes an allow.
func TestApprovalRace_DecisionVsTimeout(t *testing.T) {
	t.Parallel()
	raceCase(t, "decision_vs_timeout", 200, 2*time.Millisecond,
		func(reg *Registry, req PendingRequest, _ context.CancelFunc) {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = reg.ResolveFrom(req.RequestID, DecisionAllowOnce, SourceHost)
			}()
			wg.Wait()
		},
		map[ResolutionSource]bool{SourceHost: true, SourceTimeout: true})
}

// The operator cancels the task from one surface while approving from another.
// task.cancel is the always-available undo path, so it must be able to win —
// and when it does, the approval is denied, not orphaned.
func TestApprovalRace_DecisionVsCancel(t *testing.T) {
	t.Parallel()
	raceCase(t, "decision_vs_cancel", 200, time.Hour,
		func(reg *Registry, req PendingRequest, cancel context.CancelFunc) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); cancel() }()
			go func() {
				defer wg.Done()
				_ = reg.ResolveFrom(req.RequestID, DecisionAllowOnce, SourceRemote)
			}()
			wg.Wait()
		},
		map[ResolutionSource]bool{SourceRemote: true, SourceCancelled: true})
}

// Two surfaces decide the same approval simultaneously, with OPPOSITE
// decisions. First to reach the registry wins; the loser changes nothing.
func TestApprovalRace_DoubleDecision(t *testing.T) {
	t.Parallel()
	raceCase(t, "double_decision", 200, time.Hour,
		func(reg *Registry, req PendingRequest, _ context.CancelFunc) {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = reg.ResolveFrom(req.RequestID, DecisionAllowAlways, SourceHost)
			}()
			go func() {
				defer wg.Done()
				_ = reg.ResolveFrom(req.RequestID, DecisionDeny, SourceRemote)
			}()
			wg.Wait()
		},
		map[ResolutionSource]bool{SourceHost: true, SourceRemote: true})
}

// The decision the parked task acts on must be the SAME decision the surfaces
// were told won. A split between them would let a task run an action every
// surface believes was denied.
func TestApprovalRace_TaskAndSurfacesAgree(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		disp := newIDCapturingDispatcher()
		obs := &recordingObserver{}
		reg := NewRegistry(WithDispatcher(disp), WithTimeout(3*time.Millisecond))
		reg.AddResolutionObserver(obs)

		resCh := make(chan Resolution, 1)
		go func() {
			res, _ := reg.RequestInteractive(context.Background(), toolSurface())
			resCh <- res
		}()
		req := disp.await(t)
		go func() { _ = reg.ResolveFrom(req.RequestID, DecisionAllowOnce, SourceHost) }()

		taskSaw := <-resCh
		// Give a losing timer the chance to misbehave before we look.
		time.Sleep(5 * time.Millisecond)
		evs := obs.snapshot()
		if len(evs) != 1 {
			t.Fatalf("iteration %d: %d resolution events", i, len(evs))
		}
		if evs[0].Decision != taskSaw.Decision {
			t.Fatalf("iteration %d: task acted on %q while surfaces were told %q",
				i, taskSaw.Decision, evs[0].Decision)
		}
	}
}

// --- timeout is a fail-closed deny -----------------------------------------

func TestApprovalTimeout_IsFailClosedDeny(t *testing.T) {
	t.Parallel()
	disp := newIDCapturingDispatcher()
	obs := &recordingObserver{}
	reg := NewRegistry(WithDispatcher(disp), WithTimeout(30*time.Millisecond))
	reg.AddResolutionObserver(obs)

	start := time.Now()
	resCh := make(chan Resolution, 1)
	go func() {
		res, _ := reg.RequestInteractive(context.Background(), toolSurface())
		resCh <- res
	}()
	req := disp.await(t)

	// deadline_at must be absolute and derived from the registry clock, not
	// recomputed by any surface from a relative budget.
	if !req.DeadlineAt.After(req.IssuedAt) {
		t.Fatalf("deadline %v is not after issue %v", req.DeadlineAt, req.IssuedAt)
	}

	select {
	case res := <-resCh:
		if res.Decision != DecisionDeny {
			t.Fatalf("timeout produced %q; want deny", res.Decision)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("task never unparked after the approval timed out")
	}

	evs := obs.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want 1 resolution event, got %d", len(evs))
	}
	if evs[0].Source != SourceTimeout || evs[0].Decision != DecisionDeny {
		t.Fatalf("got %q/%q; want timeout/deny", evs[0].Source, evs[0].Decision)
	}
	if evs[0].Latency <= 0 {
		t.Fatalf("latency %v; a timed-out approval waited a real interval", evs[0].Latency)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v; the timer is not driving this", elapsed)
	}
}

// --- queue overflow --------------------------------------------------------

// The cap denies with NO dispatch, so without a resolution event the denial is
// invisible on every surface and reaches the operator as an unexplained tool
// failure. It must be announced even though nothing was ever requested.
func TestApprovalOverflow_EmitsResolutionWithoutRequest(t *testing.T) {
	t.Parallel()
	disp := newIDCapturingDispatcher()
	obs := &recordingObserver{}
	reg := NewRegistry(WithDispatcher(disp), WithTimeout(time.Hour))
	reg.AddResolutionObserver(obs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Fill the family to its cap. Each surface must be distinct so the
	// transient-grants cache cannot short-circuit a later one.
	var wg sync.WaitGroup
	for i := 0; i < PromptQueueCap; i++ {
		s := PromptSurface{Tool: &ToolPromptSurface{ServerName: "srv", ToolName: string(rune('a' + i))}}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = reg.RequestInteractive(ctx, s)
		}()
		disp.await(t)
	}

	// The cap+1'th request is denied inline.
	res, err := reg.RequestInteractive(ctx, PromptSurface{
		Tool: &ToolPromptSurface{ServerName: "srv", ToolName: "overflowed"},
	})
	if err != nil {
		t.Fatalf("overflow request errored: %v", err)
	}
	if res.Decision != DecisionDeny {
		t.Fatalf("overflow produced %q; want deny", res.Decision)
	}

	evs := obs.snapshot()
	if len(evs) != 1 {
		t.Fatalf("want exactly 1 overflow resolution, got %d", len(evs))
	}
	if evs[0].Source != SourceOverflow || evs[0].Decision != DecisionDeny {
		t.Fatalf("got %q/%q; want overflow/deny", evs[0].Source, evs[0].Decision)
	}
	if evs[0].Latency != 0 {
		t.Fatalf("overflow latency = %v; nothing waited", evs[0].Latency)
	}
	if evs[0].Request.RequestID == "" {
		t.Fatal("overflow resolution carries no approval id")
	}
	// It must NOT have been dispatched to any surface.
	select {
	case got := <-disp.ids:
		t.Fatalf("overflow was dispatched as %q; the cap denies without dispatch", got.RequestID)
	default:
	}

	cancel()
	wg.Wait()
}

// The autonomous tier has no approval, therefore no approval event. An
// operator reading the absence of traffic must be reading a real absence, not
// a broken pipe.
func TestPostureAutoAllow_EmitsNothing(t *testing.T) {
	t.Parallel()
	disp := newIDCapturingDispatcher()
	obs := &recordingObserver{}
	reg := NewRegistry(WithDispatcher(disp), WithPosture(PostureAutoAllow))
	reg.AddResolutionObserver(obs)

	res, err := reg.RequestInteractive(context.Background(), toolSurface())
	if err != nil || res.Decision != DecisionAllowOnce {
		t.Fatalf("auto-allow returned %q/%v", res.Decision, err)
	}
	if evs := obs.snapshot(); len(evs) != 0 {
		t.Fatalf("auto-allow emitted %d resolution events", len(evs))
	}
	select {
	case <-disp.ids:
		t.Fatal("auto-allow dispatched a request")
	default:
	}
}

// --- action_kind is structural, summary is content -------------------------

// action_kind is what the HOST writes to its ledger. resourceKey() — which it
// superficially resembles — embeds the canonical path, the command pattern and
// the credential purpose, because it keys a grants cache where content is the
// point. Emitting resourceKey() as action_kind would put operator file paths
// into a store the contract says must record decisions, never content.
func TestActionKind_CarriesNoOperatorContent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		surface PromptSurface
		want    string
		leaks   []string
	}{
		{
			name: "fs read names the op, never the path",
			surface: PromptSurface{FS: &FSPromptSurface{
				Op: "read", CanonicalPath: "/home/nick/.ssh/id_ed25519",
			}},
			want:  "fs::file::read",
			leaks: []string{"/home/nick", "id_ed25519", ".ssh"},
		},
		{
			name: "bash names the class, never the command",
			surface: PromptSurface{Bash: &BashPromptSurface{
				Pattern: "rm -rf /var/secret", Argv: []string{"rm", "-rf", "/var/secret"},
			}},
			want:  "bash::command::exec",
			leaks: []string{"rm", "secret", "/var"},
		},
		{
			name: "cred names the provider, never the purpose",
			surface: PromptSurface{Cred: &CredPromptSurface{
				ProviderID: "anthropic", Purpose: "deploy to prod-eu-west",
			}},
			want:  "cred::anthropic::grant",
			leaks: []string{"deploy", "prod-eu-west"},
		},
		{
			name: "tool keeps the resourceKey shape",
			surface: PromptSurface{Tool: &ToolPromptSurface{
				ServerName: "filesystem", ToolName: "read_file",
			}},
			want: "tool::filesystem::read_file",
		},
		{
			name:    "builtin tool without a server",
			surface: PromptSurface{Tool: &ToolPromptSurface{ToolName: "Bash"}},
			want:    "tool::builtin::bash",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.surface.ActionKind()
			if got != tc.want {
				t.Fatalf("ActionKind() = %q; want %q", got, tc.want)
			}
			for _, leak := range tc.leaks {
				if strings.Contains(got, leak) {
					t.Fatalf("ActionKind() = %q leaks operator content %q", got, leak)
				}
			}
			if len(got) > actionKindMaxBytes {
				t.Fatalf("ActionKind() is %d bytes; cap is %d", len(got), actionKindMaxBytes)
			}
			for _, r := range got {
				if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789_:.-", r) {
					t.Fatalf("ActionKind() = %q contains %q, outside the contract grammar", got, r)
				}
			}
		})
	}
}

func TestActionKind_SanitisesHostileSegments(t *testing.T) {
	t.Parallel()
	got := PromptSurface{Tool: &ToolPromptSurface{
		ServerName: "Bad Server/../etc", ToolName: "Tool Name!",
	}}.ActionKind()
	if strings.ContainsAny(got, " /!") {
		t.Fatalf("ActionKind() = %q left unsanitised bytes in place", got)
	}
	if n := strings.Count(got, "::"); n != 2 {
		t.Fatalf("ActionKind() = %q has %d separators; grammar is domain::subsystem::action", got, n)
	}
}

func TestActionKind_EmptyForMalformedSurface(t *testing.T) {
	t.Parallel()
	if got := (PromptSurface{}).ActionKind(); got != "" {
		t.Fatalf("malformed surface produced action_kind %q", got)
	}
}

// summary is deliberately content — a decision surface that cannot see the
// path cannot decide — but it is hard-bounded, and the bound must cut on a
// rune boundary or the frame carries invalid UTF-8.
func TestSummary_BoundedOnRuneBoundary(t *testing.T) {
	t.Parallel()
	// 3-byte runes: 400 of them = 1200 bytes, so the cut lands mid-rune
	// unless the truncation walks back.
	long := strings.Repeat("な", 400)
	p := PendingRequest{Surface: PromptSurface{
		FS: &FSPromptSurface{Op: "write", CanonicalPath: long},
	}}
	got := p.Summary()
	if len(got) > SummaryMaxBytes {
		t.Fatalf("summary is %d bytes; cap is %d", len(got), SummaryMaxBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("summary is not valid UTF-8 — truncation cut mid-rune")
	}
	if got == "" {
		t.Fatal("summary truncated to nothing")
	}
}

func TestSummary_AppendsReason(t *testing.T) {
	t.Parallel()
	p := PendingRequest{Surface: PromptSurface{
		FS:     &FSPromptSurface{Op: "write", CanonicalPath: "/tmp/x"},
		Reason: "needed for the build",
	}}
	got := p.Summary()
	if !strings.Contains(got, "/tmp/x") || !strings.Contains(got, "needed for the build") {
		t.Fatalf("summary %q dropped the display or the reason", got)
	}
}

func TestProject_MirrorsFamilyFields(t *testing.T) {
	t.Parallel()
	p := PendingRequest{Surface: PromptSurface{
		FS: &FSPromptSurface{Op: "delete", CanonicalPath: "/tmp/gone", Dangerous: true},
	}}
	got := p.Project()
	if got.Op != "delete" || got.ResourceUID != "/tmp/gone" || !got.Dangerous {
		t.Fatalf("projection = %+v", got)
	}
	if got.ResourceDisplay != "delete /tmp/gone" {
		t.Fatalf("ResourceDisplay = %q", got.ResourceDisplay)
	}
}
