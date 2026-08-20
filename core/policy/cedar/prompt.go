// Package cedar — prompt.go: universal interactive permission helper.
//
// WP02 of mission cedar-credential-policy-01KQ8TDE. When `Engine.Evaluate`
// returns `NotApplicable` for one of the four resource families
// (Credential / BashCommand / FilesystemOp / Tool), the caller invokes
// `Registry.RequestInteractive(ctx, surface)`. The helper:
//
//  1. enqueues a pending request keyed by an opaque request id;
//  2. emits an event on the family's broker topic via the injected
//     PromptDispatcher so the frontend modal can render and prompt the
//     user;
//  3. blocks until one of (a) the user resolves the request through
//     `Permissions.Resolve`, (b) the per-request 5-minute timeout fires,
//     (c) the caller's context cancels, or (d) the queue overflowed at
//     enqueue time and we already returned an auto-deny.
//
// The Registry is process-wide and safe for concurrent use from many
// goroutines. The pending map MUST stay leak-free — every entry created
// is removed on the resolve / timeout / cancel path. A leak-test in
// prompt_test.go asserts the post-test map is empty.
//
// This file deliberately stays transport-agnostic: it knows the four
// topic names but does NOT call runtime.EventsEmit. Dispatch is via the
// PromptDispatcher interface; the rpc layer wires a concrete
// implementation that fans into the StreamBroker (the only authorised
// caller of runtime.EventsEmit).
package cedar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Default timing + capacity constants. Spec §4.2 FR-011 + tasks.md WP02.
const (
	// PromptTimeout is the per-pending-request budget (NFR-004). On
	// expiry the registry resolves the request as Deny with reason
	// "timeout".
	PromptTimeout = 5 * time.Minute

	// PromptQueueCap is the maximum simultaneous pending requests per
	// broker topic. Above this, RequestInteractive auto-denies the new
	// request with reason "queue overflow" — the modal queue is bounded
	// the same way (cap 5) so backend + frontend agree.
	PromptQueueCap = 5
)

// Family identifies which of the four resource families triggered the
// prompt. Determines the broker topic.
type Family string

const (
	FamilyBash       Family = "bash"
	FamilyFilesystem Family = "fs"
	FamilyCredential Family = "cred"
	FamilyTool       Family = "tool"
)

// Topic returns the broker topic for the family. The four user-visible
// names (FR-007) are byte-stable contracts with the frontend.
func (f Family) Topic() string {
	switch f {
	case FamilyBash:
		return TopicBashPermissionPending
	case FamilyFilesystem:
		return TopicFSPermissionPending
	case FamilyCredential:
		return TopicCredPermissionPending
	case FamilyTool:
		return TopicToolPermissionPending
	}
	return ""
}

// Topic name constants. Match the spec §4.2 FR-007 set verbatim.
const (
	TopicBashPermissionPending = "bash:permission-pending"
	TopicFSPermissionPending   = "fs:permission-pending"
	TopicCredPermissionPending = "cred:permission-pending"
	TopicToolPermissionPending = "tool:permission-pending"
)

// AllTopics is the canonical list of topics the rpc layer registers.
// Used by the broker registration site so the four names are declared
// in exactly one place.
func AllTopics() []string {
	return []string{
		TopicBashPermissionPending,
		TopicFSPermissionPending,
		TopicCredPermissionPending,
		TopicToolPermissionPending,
	}
}

// PromptDecision is the resolved outcome emitted by the user through
// the `Permissions.Resolve` RPC (or synthesised by the timeout /
// overflow paths). Distinct from cedar.Decision (the engine-level
// audit record) — that's a struct describing what cedar evaluated;
// PromptDecision is a closed-set string the user picks from the modal.
type PromptDecision string

const (
	// DecisionAllowOnce — grant for this single call. The transient-
	// grants cache picks it up so a second gate query for the SAME
	// resource within the same process returns Allow without re-
	// prompting (FR-010).
	DecisionAllowOnce PromptDecision = "allow_once"

	// DecisionAllowAlways — grant + persist a Cedar policy file. The
	// caller (gate site, WP03–WP06) decides which body to write; the
	// prompt registry returns the decision and steps out of the way.
	DecisionAllowAlways PromptDecision = "allow_always"

	// DecisionDeny — refuse this call. No policy is written; the next
	// gate query re-prompts. Synthesised on timeout / overflow / Esc.
	DecisionDeny PromptDecision = "deny"
)

// Resolution is the resolved-decision payload. Returned by
// RequestInteractive and accepted by Registry.Resolve.
type Resolution struct {
	// Decision is the user's pick.
	Decision PromptDecision `json:"decision"`

	// Reason is a redaction-safe diagnostic string. Set by the registry
	// on timeout / overflow / cancel; user-driven resolutions leave it
	// empty. The audit emit site picks it up so the audit row carries
	// "timeout" / "queue overflow" / "ctx canceled" text.
	Reason string `json:"reason,omitempty"`
}

// PromptSurface is the human-friendly payload the modal renders. Tagged
// union: exactly one of Bash / FS / Cred / Tool is non-nil. The
// Family() accessor returns the kind discriminator, so the modal
// frontend can dispatch on the topic alone (each topic carries a single
// surface kind) AND backend tests can assert kind without touching the
// inner struct.
type PromptSurface struct {
	// Bash, FS, Cred, Tool are tagged-union variants. Exactly one MUST
	// be non-nil for a well-formed surface.
	Bash *BashPromptSurface  `json:"bash,omitempty"`
	FS   *FSPromptSurface    `json:"fs,omitempty"`
	Cred *CredPromptSurface  `json:"cred,omitempty"`
	Tool *ToolPromptSurface  `json:"tool,omitempty"`

	// Reason is the model's stated reason for wanting the resource
	// (FR-012). Truncated to first sentence by the frontend; we pass
	// the raw string here.
	Reason string `json:"reason,omitempty"`

	// SessionID anchors the request to a specific chat session so the
	// transient-grants cache can scope grants per-session (FR-010).
	// Empty string is acceptable; transient grants then live in the
	// "global" bucket.
	SessionID string `json:"session_id,omitempty"`
}

// BashPromptSurface — bash:permission-pending payload. The modal
// renders the full argv; the backend NEVER persists it (audit lint
// gate enforces this — WP07).
type BashPromptSurface struct {
	Pattern   string   `json:"pattern"`            // FR-014 derived pattern
	Argv      []string `json:"argv"`               // raw for modal display
	WorkingDir string  `json:"working_dir,omitempty"`
	Dangerous bool     `json:"dangerous"`          // §4.3 FR-015 flag
}

// FSPromptSurface — fs:permission-pending payload.
type FSPromptSurface struct {
	Op            string `json:"op"` // read | write | delete | move
	CanonicalPath string `json:"canonical_path"`
	Dangerous     bool   `json:"dangerous"` // FR-019
}

// CredPromptSurface — cred:permission-pending payload.
type CredPromptSurface struct {
	ProviderID string `json:"provider_id"`
	Purpose    string `json:"purpose"`
}

// ToolPromptSurface — tool:permission-pending payload.
type ToolPromptSurface struct {
	ToolName     string `json:"tool_name"`
	ServerName   string `json:"server_name,omitempty"`
	ArgsRedacted string `json:"args_redacted,omitempty"`
}

// Family returns the family discriminator for the surface, or empty if
// the surface is malformed (zero or multiple variants set).
func (s PromptSurface) Family() Family {
	count := 0
	var fam Family
	if s.Bash != nil {
		fam = FamilyBash
		count++
	}
	if s.FS != nil {
		fam = FamilyFilesystem
		count++
	}
	if s.Cred != nil {
		fam = FamilyCredential
		count++
	}
	if s.Tool != nil {
		fam = FamilyTool
		count++
	}
	if count != 1 {
		return ""
	}
	return fam
}

// resourceKey returns a stable string key the transient-grants cache
// indexes against. Encodes the family + the resource id so two
// different families never collide.
func (s PromptSurface) resourceKey() string {
	switch s.Family() {
	case FamilyBash:
		return "bash::" + s.Bash.Pattern
	case FamilyFilesystem:
		return "fs::" + s.FS.Op + "::" + s.FS.CanonicalPath
	case FamilyCredential:
		return "cred::" + s.Cred.ProviderID + "::" + s.Cred.Purpose
	case FamilyTool:
		return "tool::" + s.Tool.ServerName + "::" + s.Tool.ToolName
	}
	return ""
}

// PendingRequest is the broker-emitted payload. The frontend uses
// RequestID to round-trip back through `Permissions.Resolve`.
type PendingRequest struct {
	RequestID string        `json:"request_id"`
	Family    Family        `json:"family"`
	Surface   PromptSurface `json:"surface"`
	IssuedAt  time.Time     `json:"issued_at"`
	DeadlineAt time.Time    `json:"deadline_at"`
}

// SummaryMaxBytes caps PendingRequest.Summary at 512 UTF-8 bytes,
// truncated on a rune boundary. Matches the live sibling cap on
// cmd/harness-vm's task.error.message_truncated (512 runes) in intent,
// and fixes the runes-vs-bytes ambiguity by choosing bytes — the E2E
// frame budget beyond the host is a byte budget.
const SummaryMaxBytes = 512

// actionKindMaxBytes bounds the structural resource key. Spec 074
// contracts/approval-events.md §3.1: ≤128 ASCII bytes.
const actionKindMaxBytes = 128

// Projection is the flat, surface-agnostic view of a PendingRequest that
// every decision surface renders from. It exists so the served (:7880)
// permission modal, the :7881 approval wire, and anything downstream
// project from ONE function — the shared approval_id plus this shared
// projection are the drift guard between the copies of the approval
// concept that now exist.
type Projection struct {
	// ResourceDisplay is the human-readable identification of what is
	// about to happen: a command line, a file path, a provider, a tool
	// name. It is CONTENT — derived from the operator's work.
	ResourceDisplay string
	// ResourceUID is the stable identifier for the resource (bash
	// pattern, canonical path, provider id, server__tool).
	ResourceUID string
	// Op is the filesystem operation, empty for other families.
	Op string
	// Dangerous mirrors the family's dangerous-tier flag so a surface
	// can style the decision WITHOUT parsing ResourceDisplay.
	Dangerous bool
}

// Project flattens the request's tagged-union surface. It is the single
// projection function; core/rpc's FlattenPendingRequest (which builds
// FlatPermissionRequest — exported by consent-surfaces-truth-01PMTR01
// WP03 so core/serve's dispatch shares it) and the :7881 approval
// bridge both call it.
func (p PendingRequest) Project() Projection {
	var out Projection
	s := p.Surface
	switch {
	case s.Bash != nil:
		if len(s.Bash.Argv) > 0 {
			out.ResourceDisplay = strings.Join(s.Bash.Argv, " ")
		} else {
			out.ResourceDisplay = s.Bash.Pattern
		}
		out.ResourceUID = s.Bash.Pattern
		out.Dangerous = s.Bash.Dangerous
	case s.FS != nil:
		op, path := s.FS.Op, s.FS.CanonicalPath
		switch {
		case op != "" && path != "":
			out.ResourceDisplay = op + " " + path
		case path != "":
			out.ResourceDisplay = path
		default:
			out.ResourceDisplay = op
		}
		out.ResourceUID = path
		out.Op = op
		out.Dangerous = s.FS.Dangerous
	case s.Cred != nil:
		provider, purpose := s.Cred.ProviderID, s.Cred.Purpose
		switch {
		case provider != "" && purpose != "":
			out.ResourceDisplay = provider + " · " + purpose
		case provider != "":
			out.ResourceDisplay = provider
		default:
			out.ResourceDisplay = purpose
		}
		out.ResourceUID = provider
	case s.Tool != nil:
		server, tool := s.Tool.ServerName, s.Tool.ToolName
		switch {
		case server != "" && tool != "":
			out.ResourceDisplay = server + "__" + tool
		case tool != "":
			out.ResourceDisplay = tool
		default:
			out.ResourceDisplay = server
		}
		out.ResourceUID = out.ResourceDisplay
	}
	return out
}

// Summary is the ≤512-UTF-8-byte, rune-boundary-truncated one-line
// identification of what is about to happen: the projection's
// ResourceDisplay, with the model's stated Reason appended when present.
//
// SUMMARY IS CONTENT. Callers that put it on a wire inherit spec 074's
// content discipline: it MUST NOT enter a push payload, MUST NOT be
// persisted, and MUST NOT be written to any ledger. It is deliberately
// NOT the place to dump tool arguments, tool output, prompt text, or
// diffs — it identifies the action, it does not carry the payload.
func (p PendingRequest) Summary() string {
	s := p.Project().ResourceDisplay
	if r := strings.TrimSpace(p.Surface.Reason); r != "" {
		if s != "" {
			s += " — " + r
		} else {
			s = r
		}
	}
	return truncateBytesOnRune(s, SummaryMaxBytes)
}

// ActionKind returns the STRUCTURAL resource class for the request:
// `<domain>::<subsystem>::<action>`, ASCII [a-z0-9_:.-], ≤128 bytes.
//
// It is deliberately NOT resourceKey(). resourceKey embeds operator
// content for three of the four families (the canonical path for fs, the
// derived command pattern for bash, the purpose string for cred) because
// it exists to key a transient-grants cache, where content is exactly
// what must distinguish two entries. ActionKind exists for the opposite
// job: it is the value a surface keys a notification category off and
// the value the HOST writes to its ledger, and spec 074
// contracts/approval-events.md §3.1 requires it to embed no path,
// argument, URL, or credential name. Content rides in Summary, which the
// same contract forbids from ever reaching the ledger.
//
// Only the tool family's resourceKey shape survives unchanged — server
// and tool names are catalog identifiers, and `tool::filesystem::
// read_file` is the contract's own worked example.
func (s PromptSurface) ActionKind() string {
	var kind string
	switch s.Family() {
	case FamilyBash:
		// The bash pattern is a derived command shape, not a class —
		// `rm -rf` is content. The class of every bash approval is the
		// same: something wants to run a command.
		kind = "bash::command::exec"
	case FamilyFilesystem:
		kind = "fs::file::" + sanitiseKeySegment(s.FS.Op)
	case FamilyCredential:
		// ProviderID is a configured provider identifier (the same class
		// as a tool server name), not the credential itself. Purpose is
		// free text and is excluded.
		kind = "cred::" + sanitiseKeySegment(s.Cred.ProviderID) + "::grant"
	case FamilyTool:
		server := sanitiseKeySegment(s.Tool.ServerName)
		if server == "unknown" {
			server = "builtin"
		}
		kind = "tool::" + server + "::" + sanitiseKeySegment(s.Tool.ToolName)
	default:
		return ""
	}
	if len(kind) > actionKindMaxBytes {
		kind = kind[:actionKindMaxBytes]
	}
	return kind
}

// sanitiseKeySegment lowercases seg and replaces every byte outside
// [a-z0-9_.-] with '_', so a composed ActionKind always matches the
// contract's ASCII grammar. An empty or fully-stripped segment becomes
// "unknown" rather than collapsing two adjacent separators.
func sanitiseKeySegment(seg string) string {
	seg = strings.ToLower(strings.TrimSpace(seg))
	if seg == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(seg))
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '.', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// truncateBytesOnRune returns s bounded to max UTF-8 bytes, cutting only
// on a rune boundary so the result is always valid UTF-8. No ellipsis is
// appended: the cap is a hard frame budget and three extra bytes would
// breach it.
func truncateBytesOnRune(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// PromptDispatcher dispatches a PendingRequest to the broker. The rpc
// layer wires a concrete implementation that emits on the family's
// topic; tests inject a recording fake.
type PromptDispatcher interface {
	Dispatch(ctx context.Context, topic string, payload PendingRequest)
}

// PromptDispatcherFunc adapts a function literal to PromptDispatcher.
type PromptDispatcherFunc func(ctx context.Context, topic string, payload PendingRequest)

// Dispatch implements PromptDispatcher.
func (f PromptDispatcherFunc) Dispatch(ctx context.Context, topic string, payload PendingRequest) {
	if f == nil {
		return
	}
	f(ctx, topic, payload)
}

// ResolutionSource names WHICH surface produced a resolution. It is a
// CLASS, never a device or account identity — spec 074
// contracts/approval-events.md §3.3 forbids device identity from
// crossing into the VM, so the registry deliberately has no field that
// could carry one.
//
// Inbound (accepted by ResolveFrom): SourceHost, SourceGuest,
// SourceRemote. Outbound-only (synthesised by the registry itself):
// SourceTimeout, SourceCancelled, SourceOverflow.
type ResolutionSource string

const (
	// SourceGuest is a decision taken at the harness's own served
	// (:7880) permission modal. It is the default for Resolve, which is
	// what the served RPC handler calls.
	SourceGuest ResolutionSource = "guest"
	// SourceHost is a decision taken at the kenaz desktop ApprovalPanel
	// and forwarded over :7881.
	SourceHost ResolutionSource = "host"
	// SourceRemote is a decision taken on a paired device and brokered
	// by the host over :7881.
	SourceRemote ResolutionSource = "remote"
	// SourceTimeout is the registry's own fail-closed deny on expiry.
	SourceTimeout ResolutionSource = "timeout"
	// SourceCancelled is a ctx cancellation — the caller (task) went
	// away, which denies the pending approval.
	SourceCancelled ResolutionSource = "cancelled"
	// SourceOverflow is the queue-cap auto-deny. It is the one source
	// that resolves an approval which was NEVER dispatched, so a
	// surface can render "denied — too many pending approvals" rather
	// than an unexplained tool failure.
	SourceOverflow ResolutionSource = "overflow"
)

// ResolvedEvent is delivered to every registered ResolutionObserver
// exactly once per approval id, in every interleaving. See
// AddResolutionObserver for the exactly-once argument.
type ResolvedEvent struct {
	// Request is the pending request as dispatched. For SourceOverflow
	// it is the request that was never dispatched (the id was already
	// minted when the cap check rejected it).
	Request PendingRequest
	// Decision is the winning decision. Always DecisionDeny for the
	// timeout / cancelled / overflow sources — absence of consent is
	// denial, and there is no auto-allow reachable through this path.
	Decision PromptDecision
	// Source is the class of surface that produced the decision.
	Source ResolutionSource
	// ResolvedAt is the registry clock reading at resolution.
	ResolvedAt time.Time
	// Latency is ResolvedAt - Request.IssuedAt, floored at zero. It is
	// exactly zero for SourceOverflow.
	Latency time.Duration
}

// ResolutionObserver is notified once per approval id when the approval
// leaves the pending set, whatever resolved it. Implementations MUST NOT
// block: they run on the resolving goroutine (a user's Resolve call, the
// timeout timer, or the cancelling task).
type ResolutionObserver interface {
	Resolved(ev ResolvedEvent)
}

// ResolutionObserverFunc adapts a function literal to ResolutionObserver.
type ResolutionObserverFunc func(ev ResolvedEvent)

// Resolved implements ResolutionObserver.
func (f ResolutionObserverFunc) Resolved(ev ResolvedEvent) {
	if f == nil {
		return
	}
	f(ev)
}

// pendingEntry holds the state the registry tracks per in-flight prompt.
type pendingEntry struct {
	request   PendingRequest
	resolveCh chan Resolution
	timer     *time.Timer

	// resolved is closed exactly once when the entry leaves the map —
	// guards against double-resolve on the timer-vs-Resolve race.
	resolved sync.Once
}

// Registry owns the pending-request map, the transient-grants cache,
// and the broker dispatcher. The rpc layer constructs a process-
// singleton at API.New() time and threads it into both the cedar
// callers (gate sites — WP03–WP06) and the permissions view.
type Registry struct {
	dispatcher PromptDispatcher

	// hooksMu guards the additive fan-out registrations below. It is a
	// SEPARATE mutex from mu: fan-out snapshots are taken on paths that
	// already released mu (dispatch and resolution both run outside the
	// pending-map critical section), and keeping the two locks disjoint
	// means an observer can never deadlock against the pending map.
	hooksMu          sync.RWMutex
	nextHookID       uint64
	extraDispatchers []dispatcherEntry
	observers        []observerEntry

	// permHooks fires v2 lifecycle hooks (permission_request /
	// permission_denied). nil disables lifecycle hooks.
	permHooks PermissionHookRunner

	// posture controls how interactive prompts are handled based on the
	// session's resolved autonomy tier (WP05). PostureDefault is the
	// baseline; PostureAutoAllow short-circuits every RequestInteractive
	// to Allow without UI dispatch; PostureAlwaysPrompt skips the
	// transient-grants cache so every call surfaces to the user.
	posture PromptPosture

	// mu guards pending + transient. RWMutex chosen because the gate
	// fast path (transient lookup) is read-heavy.
	mu        sync.RWMutex
	pending   map[string]*pendingEntry
	transient map[string]Resolution // per-resource grants; cleared on Reload

	// perTopicCount tracks queue depth per family for the cap-of-5
	// overflow check. Map updated under mu.
	perTopicCount map[Family]int

	// timeNow + idGen are seams for deterministic tests.
	timeNow func() time.Time
	idGen   func() string

	// timeout is configurable for tests; defaults to PromptTimeout.
	timeout time.Duration
}

// PromptPosture controls how the registry handles interactive prompts
// based on the session's resolved autonomy tier.
//
// PostureDefault is the unchanged v0.3.0 behaviour: prompts surface to
// the UI on NotApplicable, transient-grants cache short-circuits repeat
// requests.
//
// PostureAutoAllow bypasses the prompt entirely: RequestInteractive
// returns Allow immediately without dispatching any broker event. This
// is the autonomy-dial "autonomous" / "bold" fast-path (WP05).
//
// PostureAlwaysPrompt skips the transient-grants cache and always
// surfaces the prompt to the UI, even when a prior Allow-once grant
// covers the resource. This is the "strict" / "cautious" posture that
// forces explicit user confirmation on every call (WP05).
type PromptPosture string

const (
	// PostureDefault is the unmodified prompt behaviour (v0.3.0 baseline).
	PostureDefault PromptPosture = "default"
	// PostureAutoAllow auto-resolves every prompt as Allow without
	// surfacing the UI modal. Cedar deny remains the floor — callers MUST
	// gate through the engine before calling RequestInteractive.
	PostureAutoAllow PromptPosture = "auto-allow"
	// PostureAlwaysPrompt skips the transient-grants cache so every call
	// is surfaced to the user, even when a cached Allow-once grant exists.
	PostureAlwaysPrompt PromptPosture = "always-prompt"
)

// RegistryOption configures a Registry at construction time.
type RegistryOption func(*Registry)

// WithDispatcher injects the broker dispatcher. Without it the
// registry still functions but emits nothing — useful for unit tests
// that drive the round-trip without a broker.
func WithDispatcher(d PromptDispatcher) RegistryOption {
	return func(r *Registry) { r.dispatcher = d }
}

// dispatcherEntry / observerEntry are the registration records behind
// AddDispatcher / AddResolutionObserver. The id makes removal O(n)
// without relying on interface comparability (a func literal adapted
// through PromptDispatcherFunc is not comparable).
type dispatcherEntry struct {
	id uint64
	d  PromptDispatcher
}

type observerEntry struct {
	id uint64
	o  ResolutionObserver
}

// AddDispatcher registers an ADDITIONAL fan-out target for pending
// requests, alongside the one installed by WithDispatcher. It returns a
// remove func that is safe to call more than once.
//
// This is how the :7881 approval bridge attaches to the gate WITHOUT
// becoming a second gate (spec 074 ADR-approval-broker §Decision-4):
// one registry, one timer, one serializer, N listeners.
func (r *Registry) AddDispatcher(d PromptDispatcher) (remove func()) {
	if r == nil || d == nil {
		return func() {}
	}
	r.hooksMu.Lock()
	r.nextHookID++
	id := r.nextHookID
	r.extraDispatchers = append(r.extraDispatchers, dispatcherEntry{id: id, d: d})
	r.hooksMu.Unlock()
	return func() {
		r.hooksMu.Lock()
		defer r.hooksMu.Unlock()
		for i, e := range r.extraDispatchers {
			if e.id == id {
				r.extraDispatchers = append(r.extraDispatchers[:i], r.extraDispatchers[i+1:]...)
				return
			}
		}
	}
}

// AddResolutionObserver registers an observer notified exactly once per
// approval id when that approval leaves the pending set. It returns a
// remove func that is safe to call more than once.
//
// EXACTLY-ONCE ARGUMENT. Every resolution path (Resolve / ResolveFrom,
// timeoutFire, cancel) deletes the pending entry under r.mu and bails
// when the entry is already gone, so at most one goroutine per approval
// id ever reaches pendingEntry.resolved. Observers are notified from
// INSIDE that sync.Once, so a decision racing the timeout, a second
// decision racing the first, and a cancel racing either all collapse to
// one notification carrying the winner. Queue overflow is the single
// path with no pending entry; it is a straight-line synchronous deny
// that notifies once and never enters the map.
func (r *Registry) AddResolutionObserver(o ResolutionObserver) (remove func()) {
	if r == nil || o == nil {
		return func() {}
	}
	r.hooksMu.Lock()
	r.nextHookID++
	id := r.nextHookID
	r.observers = append(r.observers, observerEntry{id: id, o: o})
	r.hooksMu.Unlock()
	return func() {
		r.hooksMu.Lock()
		defer r.hooksMu.Unlock()
		for i, e := range r.observers {
			if e.id == id {
				r.observers = append(r.observers[:i], r.observers[i+1:]...)
				return
			}
		}
	}
}

// notifyResolved fans a ResolvedEvent out to every registered observer.
// Callers MUST hold neither r.mu nor pendingEntry state.
func (r *Registry) notifyResolved(ev ResolvedEvent) {
	r.hooksMu.RLock()
	obs := make([]ResolutionObserver, 0, len(r.observers))
	for _, e := range r.observers {
		obs = append(obs, e.o)
	}
	r.hooksMu.RUnlock()
	for _, o := range obs {
		o.Resolved(ev)
	}
}

// resolvedEvent builds a ResolvedEvent for req at the registry clock.
func (r *Registry) resolvedEvent(req PendingRequest, decision PromptDecision, source ResolutionSource) ResolvedEvent {
	now := r.timeNow()
	lat := now.Sub(req.IssuedAt)
	if lat < 0 {
		lat = 0
	}
	return ResolvedEvent{
		Request:    req,
		Decision:   decision,
		Source:     source,
		ResolvedAt: now,
		Latency:    lat,
	}
}

// WithPosture sets the prompt posture for the registry. Defaults to
// PostureDefault when not supplied. Tests and production wiring can
// override to PostureAutoAllow (autonomous tier) or PostureAlwaysPrompt
// (strict tier). See PromptPosture for semantics.
func WithPosture(p PromptPosture) RegistryOption {
	return func(r *Registry) { r.posture = p }
}

// WithTimeout overrides PromptTimeout. Test-only seam; production
// callers leave the default.
func WithTimeout(d time.Duration) RegistryOption {
	return func(r *Registry) { r.timeout = d }
}

// WithTimeNow injects a clock. Test-only.
func WithTimeNow(f func() time.Time) RegistryOption {
	return func(r *Registry) { r.timeNow = f }
}

// WithIDGen injects an id generator. Test-only.
func WithIDGen(f func() string) RegistryOption {
	return func(r *Registry) { r.idGen = f }
}

// NewRegistry constructs a Registry. A nil dispatcher is allowed (no
// broker emission); tests use this to assert resolution semantics
// without wiring an emitter.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		pending:       map[string]*pendingEntry{},
		transient:     map[string]Resolution{},
		perTopicCount: map[Family]int{},
		timeNow:       time.Now,
		idGen:         defaultIDGen,
		timeout:       PromptTimeout,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// defaultIDGen produces a 96-bit random hex id. Cryptographic random
// is fine here — collision risk is negligible across the cap-of-5
// queue; readability of the id is irrelevant since the frontend treats
// it as opaque.
func defaultIDGen() string {
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Failing here means /dev/urandom is broken — give the caller
		// SOMETHING that is at least unique within the process so the
		// pending map doesn't double-key. time.Now nanoseconds + a
		// fixed prefix is enough for the leak-test to pass.
		return fmt.Sprintf("rid-fallback-%d", time.Now().UnixNano())
	}
	return "rid-" + hex.EncodeToString(buf[:])
}

// ErrInvalidSurface is returned by RequestInteractive when the surface
// has zero or multiple variants set (i.e. .Family() is empty).
var ErrInvalidSurface = errors.New("cedar/prompt: surface MUST set exactly one of Bash/FS/Cred/Tool")

// RequestInteractive enqueues a pending request, emits a broker event,
// and blocks until one of resolve / timeout / ctx-cancel / overflow
// fires. Returns the Resolution.
//
// The function honours four exit paths:
//
//   - User resolves through `Permissions.Resolve`: returns the user's
//     decision verbatim. Allow-once seeds the transient-grants cache.
//   - 5-minute timeout: returns `{Decision: Deny, Reason: "timeout"}`.
//     Spec FR-011 + NFR-004. (TODO: emit `KindPermissionTimeout` audit
//     event once that kind is registered in WP07.)
//   - ctx cancellation: returns `{Decision: Deny, Reason: "ctx canceled"}`.
//     The pending entry is cleaned up so the registry never leaks.
//   - Queue overflow at enqueue time: returns
//     `{Decision: Deny, Reason: "queue overflow"}` immediately, with
//     no broker emission and no pending entry.
//
// Concurrent callers across different sessions are safe — each
// pending entry has its own resolveCh, and the per-topic counter is
// updated under mu.
func (r *Registry) RequestInteractive(
	ctx context.Context,
	surface PromptSurface,
) (Resolution, error) {
	if r == nil {
		return Resolution{Decision: DecisionDeny, Reason: "registry nil"}, errors.New("cedar/prompt: registry nil")
	}
	fam := surface.Family()
	if fam == "" {
		return Resolution{Decision: DecisionDeny, Reason: "invalid surface"}, ErrInvalidSurface
	}

	// WP05 — autonomy posture fast paths.
	//
	// PostureAutoAllow: the resolved tier (autonomous/bold) permits the
	// call without user interaction. Return Allow immediately, before any
	// transient-cache or dispatcher work. Cedar deny has already been
	// checked by the call site gate; this path is only reached on
	// NotApplicable where no Cedar policy matched.
	if r.posture == PostureAutoAllow {
		return Resolution{Decision: DecisionAllowOnce, Reason: "auto-allow (autonomy posture)"}, nil
	}

	// Transient-grants fast path: a previous Allow-once for the same
	// resource short-circuits with Allow. FR-010.
	// PostureAlwaysPrompt skips this cache so every call surfaces to the
	// UI regardless of prior grants (strict/cautious tier).
	key := surface.resourceKey()
	if r.posture != PostureAlwaysPrompt {
		r.mu.RLock()
		if existing, ok := r.transient[key]; ok && existing.Decision == DecisionAllowOnce {
			r.mu.RUnlock()
			return existing, nil
		}
		r.mu.RUnlock()
	}

	// v2 lifecycle hook: permission_request (WP06).
	// Fire before raising the modal. A hook allow short-circuits the prompt;
	// a hook deny short-circuits with a deny result. Cedar deny is the floor
	// and is enforced at the call site (callers only reach here on
	// NotApplicable), so hooks cannot widen what Cedar explicitly denies.
	if r.permHooks != nil {
		resource := surface.resourceKey()
		actionKind := string(fam)
		hookAllow, hookDeny, hookReason := r.permHooks.FirePermissionRequest(
			ctx, surface.SessionID, actionKind, resource, "")
		if hookDeny {
			return Resolution{Decision: DecisionDeny, Reason: "hook_deny: " + hookReason}, nil
		}
		if hookAllow {
			// Seed the transient-grants cache so a subsequent query sees Allow.
			r.mu.Lock()
			r.transient[key] = Resolution{Decision: DecisionAllowOnce, Reason: "hook_allow: " + hookReason}
			r.mu.Unlock()
			return Resolution{Decision: DecisionAllowOnce, Reason: "hook_allow: " + hookReason}, nil
		}
	}

	// Try to enqueue. Overflow auto-denies without touching the
	// dispatcher.
	now := r.timeNow()
	deadline := now.Add(r.timeout)
	id := r.idGen()
	entry := &pendingEntry{
		request: PendingRequest{
			RequestID:  id,
			Family:     fam,
			Surface:    surface,
			IssuedAt:   now,
			DeadlineAt: deadline,
		},
		resolveCh: make(chan Resolution, 1),
	}

	r.mu.Lock()
	if r.perTopicCount[fam] >= PromptQueueCap {
		r.mu.Unlock()
		// The cap denies WITHOUT dispatching, so no surface ever saw a
		// request. Observers are still notified so the denial is
		// legible ("too many pending approvals") instead of presenting
		// as an unexplained tool failure — spec 074
		// contracts/approval-events.md §3.3. Latency is zero by
		// construction: the request never waited on anyone.
		ev := r.resolvedEvent(entry.request, DecisionDeny, SourceOverflow)
		ev.Latency = 0
		r.notifyResolved(ev)
		if r.permHooks != nil {
			r.permHooks.FirePermissionDenied(ctx, surface.SessionID, string(fam), surface.resourceKey(), "queue overflow")
		}
		return Resolution{Decision: DecisionDeny, Reason: "queue overflow"}, nil
	}
	r.pending[id] = entry
	r.perTopicCount[fam]++
	// Schedule the timeout under the mutex so a concurrent Resolve sees
	// either both (entry + timer) or none.
	entry.timer = time.AfterFunc(r.timeout, func() {
		r.timeoutFire(id)
	})
	r.mu.Unlock()

	// Dispatch outside the lock — a slow emitter mustn't stall other
	// gate sites. nil dispatcher is intentional: tests skip emission.
	if r.dispatcher != nil {
		r.dispatcher.Dispatch(ctx, fam.Topic(), entry.request)
	}
	// Additive fan-out targets (the :7881 approval bridge). Same payload,
	// same topic, no second gate.
	r.hooksMu.RLock()
	extra := make([]PromptDispatcher, 0, len(r.extraDispatchers))
	for _, e := range r.extraDispatchers {
		extra = append(extra, e.d)
	}
	r.hooksMu.RUnlock()
	for _, d := range extra {
		d.Dispatch(ctx, fam.Topic(), entry.request)
	}

	// Block on resolution / timeout / cancel.
	select {
	case res := <-entry.resolveCh:
		return res, nil
	case <-ctx.Done():
		// Caller bailed (e.g. session closed). Clean up the entry so
		// the registry doesn't leak; if the user later clicks Resolve,
		// the RPC returns ErrUnknownRequest and the modal's stale
		// state is reconciled by the frontend.
		//
		// cancel reports whether cancellation actually WON at the
		// registry. That distinction is load-bearing, because both
		// cases are reachable here: Go's select picks at random among
		// ready cases, so a resolution delivered in the same instant
		// the context was cancelled can land us in this branch with
		// the decision already decided and already announced.
		if r.cancel(id, "ctx canceled") {
			return Resolution{Decision: DecisionDeny, Reason: "ctx canceled"}, ctx.Err()
		}
		// Cancellation lost: some other path removed the entry and is
		// therefore committed to firing resolveCh. Wait for it.
		// Synthesising a deny here instead would let the caller act on
		// a decision NO surface was ever told about — the registry, the
		// served modal and the host would all record the winner while
		// the gate site quietly denied. The send is guaranteed and
		// immediate: fire() runs under the same sync.Once that removed
		// the entry, and resolution observers are contractually
		// non-blocking.
		return <-entry.resolveCh, nil
	}
}

// Resolve injects a user-driven resolution. Called by the rpc
// `Permissions.Resolve` handler. Returns ErrUnknownRequest when the
// id has already been resolved or never existed.
//
// AllowOnce decisions seed the transient-grants cache so a second
// gate query for the same resource within the same process returns
// Allow without re-prompting. AllowAlways is NOT cached here — the
// caller persists a Cedar policy file, and the engine reload picks it
// up; we do not double-track.
func (r *Registry) Resolve(requestID string, decision PromptDecision) error {
	return r.ResolveFrom(requestID, decision, SourceGuest)
}

// ResolveFrom is Resolve with an explicit decision SOURCE class, so a
// resolution observer can report which surface won the race. Resolve
// delegates here with SourceGuest — the harness's own served (:7880)
// modal, which is the only caller that existed before spec 074.
//
// source MUST be one of SourceHost / SourceGuest / SourceRemote: the
// timeout / cancelled / overflow classes are registry-synthesised and
// are rejected here so no external caller can forge one.
//
// Returns ErrUnknownRequest when the id has already been resolved or
// never existed. The :7881 adapter absorbs that error rather than
// propagating it — a duplicate decision on an at-least-once event
// stream is expected traffic, not a fault.
func (r *Registry) ResolveFrom(requestID string, decision PromptDecision, source ResolutionSource) error {
	if r == nil {
		return errors.New("cedar/prompt: registry nil")
	}
	if decision != DecisionAllowOnce && decision != DecisionAllowAlways && decision != DecisionDeny {
		return fmt.Errorf("cedar/prompt: invalid decision %q", decision)
	}
	switch source {
	case SourceHost, SourceGuest, SourceRemote:
	default:
		return fmt.Errorf("cedar/prompt: invalid resolution source %q", source)
	}

	r.mu.Lock()
	entry, ok := r.pending[requestID]
	if !ok {
		r.mu.Unlock()
		return ErrUnknownRequest
	}
	delete(r.pending, requestID)
	if cnt := r.perTopicCount[entry.request.Family]; cnt > 0 {
		r.perTopicCount[entry.request.Family] = cnt - 1
	}
	if decision == DecisionAllowOnce {
		key := entry.request.Surface.resourceKey()
		if key != "" {
			r.transient[key] = Resolution{Decision: DecisionAllowOnce}
		}
	}
	r.mu.Unlock()

	resolution := Resolution{Decision: decision}
	r.fire(entry, resolution, source)
	return nil
}

// ErrUnknownRequest is returned by Resolve when the request id is
// unknown — either it never existed or it already resolved (timeout,
// cancellation, prior Resolve). The rpc layer surfaces this so the
// frontend can reconcile a stale modal.
var ErrUnknownRequest = errors.New("cedar/prompt: unknown request id")

// timeoutFire is the scheduled callback that resolves a stale request
// as Deny + "timeout". Idempotent against a concurrent Resolve.
func (r *Registry) timeoutFire(requestID string) {
	r.mu.Lock()
	entry, ok := r.pending[requestID]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.pending, requestID)
	if cnt := r.perTopicCount[entry.request.Family]; cnt > 0 {
		r.perTopicCount[entry.request.Family] = cnt - 1
	}
	r.mu.Unlock()

	// TODO(WP07): once `KindPermissionTimeout` is registered, emit an
	// audit event here so the timeout is auditable.
	r.fire(entry, Resolution{Decision: DecisionDeny, Reason: "timeout"}, SourceTimeout)
}

// cancel removes a pending entry without firing the resolve channel.
// Used on ctx cancellation — the caller has already returned, so a
// channel send would deadlock. The timer is stopped so a late-firing
// timeoutFire is a no-op.
//
// It reports whether cancellation WON: true when this call removed the
// entry, false when some other resolution path got there first and is
// therefore committed to delivering a real decision. The caller needs
// that answer to avoid synthesising a deny over a decision that already
// won and was already announced to every surface.
func (r *Registry) cancel(requestID, reason string) (won bool) {
	r.mu.Lock()
	entry, ok := r.pending[requestID]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.pending, requestID)
	if cnt := r.perTopicCount[entry.request.Family]; cnt > 0 {
		r.perTopicCount[entry.request.Family] = cnt - 1
	}
	r.mu.Unlock()

	entry.resolved.Do(func() {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		// The caller has already returned its own deny, so nothing is
		// sent on resolveCh — but the approval DID leave the pending
		// set, and every surface that rendered it needs to learn that
		// it is void. Notified from inside the same sync.Once that
		// guards fire(), so a cancel racing a decision or a timeout
		// still yields exactly one notification.
		r.notifyResolved(r.resolvedEvent(entry.request, DecisionDeny, SourceCancelled))
	})
	_ = reason
	return true
}

// fire delivers the resolution to the pending entry's channel exactly
// once and stops the timer. Subsequent fire / cancel calls on the same
// entry are no-ops.
//
// v2 lifecycle hook: permission_denied (trust-surfaces-that-fire-
// 01PMZ202 WP10 / UNIT-9). fire() is the one choke point both
// ResolveFrom (a user's explicit Deny) and timeoutFire (the 5-minute
// timeout) funnel through, so wiring it here — rather than at each
// caller — covers both without duplicating the guard. FireAsync is
// fire-and-forget and does not block resolveCh delivery.
// cancel() (ctx cancellation) deliberately does NOT fire this hook: the
// caller bailed, Cedar never rendered a decision, so there is nothing
// to report as "denied".
func (r *Registry) fire(entry *pendingEntry, res Resolution, source ResolutionSource) {
	entry.resolved.Do(func() {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		if res.Decision == DecisionDeny && r.permHooks != nil {
			r.permHooks.FirePermissionDenied(context.Background(),
				entry.request.Surface.SessionID,
				string(entry.request.Family),
				entry.request.Surface.resourceKey(),
				res.Reason,
			)
		}
		r.notifyResolved(r.resolvedEvent(entry.request, res.Decision, source))
		select {
		case entry.resolveCh <- res:
		default:
			// Channel is buffered with cap 1; a non-blocking send
			// failing means the resolveCh has already been written to
			// (impossible under the resolved.Once guard) OR the
			// receiver has gone away.
		}
	})
}

// PendingCount returns the number of in-flight pending requests
// across all families. Test helper for the leak-detection assertion.
func (r *Registry) PendingCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pending)
}

// PendingForFamily returns the in-flight count for one family. Test
// helper.
func (r *Registry) PendingForFamily(f Family) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.perTopicCount[f]
}

// TransientGrantCount returns the number of cached Allow-once grants.
// Test helper.
func (r *Registry) TransientGrantCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.transient)
}

// ListPending returns a snapshot of all in-flight pending requests.
// Used by the permissions view's `ListPending` call (not `ListGrants` —
// that method covers persisted Allow-always grants, a different
// concept) so the frontend's four permission modals can reconcile their
// queue against the server's parked set after a reload
// (consent-surfaces-truth-01PMTR01 WP03).
func (r *Registry) ListPending() []PendingRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PendingRequest, 0, len(r.pending))
	for _, e := range r.pending {
		out = append(out, e.request)
	}
	return out
}

// ListTransientGrants returns a snapshot of the per-resource Allow-
// once cache. Used by `Permissions.ListGrants` so the Settings panel
// can show "this session" grants alongside persisted .cedar files.
func (r *Registry) ListTransientGrants() []TransientGrant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TransientGrant, 0, len(r.transient))
	for k := range r.transient {
		out = append(out, TransientGrant{ResourceKey: k})
	}
	return out
}

// TransientGrant is a single entry in the per-process Allow-once
// cache. ResourceKey is the family-prefixed string from
// PromptSurface.resourceKey().
type TransientGrant struct {
	ResourceKey string `json:"resource_key"`
}

// ClearTransientGrants drops every cached Allow-once grant. Called by
// the engine reload path so a freshly-reloaded policy bundle is the
// single source of truth — a stale transient grant for a resource
// that's now denied by a new .cedar file mustn't keep flowing through.
func (r *Registry) ClearTransientGrants() {
	r.mu.Lock()
	r.transient = map[string]Resolution{}
	r.mu.Unlock()
}

// Posture returns the registry's current prompt posture. A zero-value
// posture (not set via WithPosture) is normalised to PostureDefault.
func (r *Registry) Posture() PromptPosture {
	r.mu.RLock()
	p := r.posture
	r.mu.RUnlock()
	if p == "" {
		return PostureDefault
	}
	return p
}

// SetPosture updates the prompt posture on a live registry. Safe for
// concurrent use; takes effect on the next RequestInteractive call.
// Production wiring calls this when the session's resolved autonomy
// tier changes (e.g. session override applied mid-turn).
func (r *Registry) SetPosture(p PromptPosture) {
	r.mu.Lock()
	r.posture = p
	r.mu.Unlock()
}
