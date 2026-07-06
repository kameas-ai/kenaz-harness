// Package acp defines the ACPAPI view-scoped accessor for the ACP
// (Agent Communication Protocol) peer management and envelope dispatch surface
// (acp-orchestration-integration-01NDFSEX06).
//
// The five RPC verbs here expose the ~80%-built core/acp/ layer to the
// frontend: peer discovery, trust management, envelope dispatch, and trace
// retrieval. The bindings follow the single-underscore View_Op naming
// convention (plan §8 R-6) so they map cleanly to ACP_ListPeers etc. in
// core/rpc/bindings.go.
package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	coreaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/acp"
	"github.com/kameas-ai/kenaz-harness/core/acp/envelope"
	"github.com/kameas-ai/kenaz-harness/core/acp/peers"
	"github.com/kameas-ai/kenaz-harness/core/acp/verify"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	cedarlib "github.com/cedar-policy/cedar-go"
)

// ── Wire types ────────────────────────────────────────────────────────────

// Peer is the wire-safe view of a peer profile for the frontend.
type Peer struct {
	ID               string `json:"id"`
	CardFingerprint  string `json:"cardFingerprint"`
	Transport        string `json:"transport"`
	TrustTier        string `json:"trustTier"`
	EndpointURL      string `json:"endpointUrl"`
	LastSeen         string `json:"lastSeen,omitempty"` // RFC3339; empty if never contacted
	AgentName        string `json:"agentName,omitempty"`
	AgentDescription string `json:"agentDescription,omitempty"`
}

// EnvelopeTrace is the wire-safe view of one envelope lifecycle event.
type EnvelopeTrace struct {
	EnvelopeID string `json:"envelopeId"`
	PeerID     string `json:"peerId"`
	Transport  string `json:"transport"`
	Direction  string `json:"direction"` // "send" | "receive"
	Outcome    string `json:"outcome"`   // "ok" | "denied_by_policy" | "transport_error" | "verify_rejected"
	BytesIn    int64  `json:"bytesIn"`
	BytesOut   int64  `json:"bytesOut"`
	ErrorCode  string `json:"errorCode,omitempty"`
	CreatedAt  string `json:"createdAt"` // RFC3339Nano
}

// DispatchResult is the result of ACP_Dispatch.
type DispatchResult struct {
	EnvelopeID string `json:"envelopeId"`
}

// TrustResult is the result of ACP_TrustPeer.
type TrustResult struct {
	PeerID    string `json:"peerId"`
	TrustTier string `json:"trustTier"`
}

// ── API surface ───────────────────────────────────────────────────────────

// ACPAPI is the view-scoped interface for the ACP surface.
type ACPAPI interface {
	ACP_ListPeers(ctx context.Context) ([]Peer, error)
	ACP_TrustPeer(ctx context.Context, cardBlob string) (TrustResult, error)
	ACP_RevokePeer(ctx context.Context, peerID string) error
	ACP_Dispatch(ctx context.Context, peerID, turnPayload string) (DispatchResult, error)
	ACP_GetTrace(ctx context.Context, envelopeID string) (EnvelopeTrace, error)
	// ACP_ListTraces returns the most-recent envelope traces for a given peer.
	// Returns up to maxN traces (or all if maxN <= 0), newest first. FR-005.
	ACP_ListTraces(ctx context.Context, peerID string) ([]EnvelopeTrace, error)
}

// RegistryIface is the narrow interface the ACP view needs from core/acp/peers.
type RegistryIface interface {
	All() []acp.PeerProfile
	Lookup(peerID string) (acp.PeerProfile, error)
	Load(profiles []acp.PeerProfile) error
}

// EnvelopeIface is the narrow interface the ACP view needs from core/acp/envelope.
type EnvelopeIface interface {
	Dispatch(ctx context.Context, peer acp.PeerProfile, skillID string, body json.RawMessage) (a2aTaskID string, stream <-chan acp.Message, err error)
	FetchCard(ctx context.Context, peer acp.PeerProfile) (acp.AgentCard, error)
}

// ── trace store ───────────────────────────────────────────────────────────

// traceStore holds the last N envelope traces in memory, keyed by
// envelope ID, with a secondary per-peer reverse index so callers can
// retrieve all recent traces for a given peer.
type traceStore struct {
	mu        sync.Mutex
	traces    map[string]EnvelopeTrace // keyed by envelopeID
	order     []string                 // insertion order for LRU eviction
	peerIndex map[string][]string      // peerID → []envelopeID (insertion order)
	maxN      int
}

func newTraceStore(maxN int) *traceStore {
	if maxN <= 0 {
		maxN = 1000
	}
	return &traceStore{
		traces:    make(map[string]EnvelopeTrace, maxN),
		peerIndex: make(map[string][]string),
		maxN:      maxN,
	}
}

func (s *traceStore) put(t EnvelopeTrace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.traces[t.EnvelopeID]; !exists {
		if len(s.order) >= s.maxN {
			// evict oldest
			oldest := s.order[0]
			s.order = s.order[1:]
			if evicted, ok := s.traces[oldest]; ok {
				// also drop from peerIndex
				if ids, pok := s.peerIndex[evicted.PeerID]; pok {
					updated := make([]string, 0, len(ids))
					for _, id := range ids {
						if id != oldest {
							updated = append(updated, id)
						}
					}
					if len(updated) == 0 {
						delete(s.peerIndex, evicted.PeerID)
					} else {
						s.peerIndex[evicted.PeerID] = updated
					}
				}
			}
			delete(s.traces, oldest)
		}
		s.order = append(s.order, t.EnvelopeID)
		s.peerIndex[t.PeerID] = append(s.peerIndex[t.PeerID], t.EnvelopeID)
	}
	s.traces[t.EnvelopeID] = t
}

func (s *traceStore) get(envelopeID string) (EnvelopeTrace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.traces[envelopeID]
	return t, ok
}

// listForPeer returns all traces for the given peerID, newest first.
func (s *traceStore) listForPeer(peerID string) []EnvelopeTrace {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, ok := s.peerIndex[peerID]
	if !ok {
		return nil
	}
	out := make([]EnvelopeTrace, 0, len(ids))
	// ids are insertion order; reverse for newest-first
	for i := len(ids) - 1; i >= 0; i-- {
		if t, ok := s.traces[ids[i]]; ok {
			out = append(out, t)
		}
	}
	return out
}

// ── peerStore ─────────────────────────────────────────────────────────────

// peerMeta holds runtime-enriched metadata not in PeerProfile.
type peerMeta struct {
	trustTier       string
	lastSeen        time.Time
	cardFingerprint string
}

// peerStore is the runtime peer registry that extends the core/acp/peers
// Registry with trust tier tracking and last-seen timestamps.
type peerStore struct {
	mu        sync.RWMutex
	peers     map[string]acp.PeerProfile
	meta      map[string]peerMeta
	reg       RegistryIface
}

func newPeerStore(reg RegistryIface) *peerStore {
	ps := &peerStore{
		peers: make(map[string]acp.PeerProfile),
		meta:  make(map[string]peerMeta),
		reg:   reg,
	}
	// Seed from the underlying registry.
	if reg != nil {
		for _, p := range reg.All() {
			ps.peers[p.PeerID] = p
			ps.meta[p.PeerID] = peerMeta{trustTier: "pending"}
		}
	}
	return ps
}

func (ps *peerStore) all() []Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	out := make([]Peer, 0, len(ps.peers))
	for _, p := range ps.peers {
		m := ps.meta[p.PeerID]
		w := toPeer(p, m)
		out = append(out, w)
	}
	return out
}

func (ps *peerStore) lookup(peerID string) (acp.PeerProfile, peerMeta, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	p, ok := ps.peers[peerID]
	if !ok {
		return acp.PeerProfile{}, peerMeta{}, false
	}
	return p, ps.meta[peerID], true
}

func (ps *peerStore) trust(profile acp.PeerProfile, card acp.AgentCard) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.peers[profile.PeerID] = profile
	fp := cardFingerprint(card)
	ps.meta[profile.PeerID] = peerMeta{
		trustTier:       "verified",
		cardFingerprint: fp,
	}
	// Also push to underlying registry so it participates in bundle reload.
	if ps.reg != nil {
		existing := ps.reg.All()
		updated := make([]acp.PeerProfile, 0, len(existing)+1)
		found := false
		for _, ep := range existing {
			if ep.PeerID == profile.PeerID {
				updated = append(updated, profile)
				found = true
			} else {
				updated = append(updated, ep)
			}
		}
		if !found {
			updated = append(updated, profile)
		}
		_ = ps.reg.Load(updated)
	}
}

func (ps *peerStore) revoke(peerID string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, ok := ps.peers[peerID]; !ok {
		return false
	}
	delete(ps.peers, peerID)
	delete(ps.meta, peerID)
	// Remove from underlying registry.
	if ps.reg != nil {
		existing := ps.reg.All()
		updated := make([]acp.PeerProfile, 0, len(existing))
		for _, ep := range existing {
			if ep.PeerID != peerID {
				updated = append(updated, ep)
			}
		}
		_ = ps.reg.Load(updated)
	}
	return true
}

func (ps *peerStore) touchLastSeen(peerID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if m, ok := ps.meta[peerID]; ok {
		m.lastSeen = time.Now().UTC()
		ps.meta[peerID] = m
	}
}

// ── API implementation ────────────────────────────────────────────────────

// API provides the five Wails-bound ACP operations.
type API struct {
	env      EnvelopeIface
	peerStor *peerStore
	traces   *traceStore
	verifier acp.Verifier
	auditEm  coreaudit.Emitter
	// cedarEng is the narrow cedar evaluation surface; nil when policy is unavailable.
	cedarEng CedarEngine
}

// CedarEngine is the narrow policy-evaluation surface the ACP view needs.
// The real implementation is core/policy/cedar.Engine. Nil disables policy
// gating (permissive).
type CedarEngine interface {
	Check(ctx context.Context, principal, action, resource string, attrs map[string]interface{}) (cedar.Outcome, string, error)
}

// Options holds optional construction parameters.
type Options struct {
	Verifier acp.Verifier
	Cedar    CedarEngine
	Audit    coreaudit.Emitter
}

// NewAPI constructs an API. reg and env are required. opts is optional.
func NewAPI(reg RegistryIface, env EnvelopeIface, opts Options) *API {
	v := opts.Verifier
	if v == nil {
		v = verify.UnsignedAcceptVerifier{}
	}
	return &API{
		env:      env,
		peerStor: newPeerStore(reg),
		traces:   newTraceStore(1000),
		verifier: v,
		auditEm:  opts.Audit,
		cedarEng: opts.Cedar,
	}
}

// ACP_ListPeers returns the current peer list with trust state and last-seen
// timestamps. FR-001.
func (a *API) ACP_ListPeers(_ context.Context) ([]Peer, error) {
	return a.peerStor.all(), nil
}

// ACP_TrustPeer verifies the signed card in cardBlob via the configured
// Verifier and — on success — adds the peer to the runtime store at trust
// tier "verified". Returns the resulting TrustResult. FR-002.
//
// cardBlob is a JSON-encoded AgentCard.
func (a *API) ACP_TrustPeer(ctx context.Context, cardBlob string) (TrustResult, error) {
	var card acp.AgentCard
	if err := json.Unmarshal([]byte(cardBlob), &card); err != nil {
		return TrustResult{}, fmt.Errorf("acp: ACP_TrustPeer: invalid card JSON: %w", err)
	}
	if card.AgentID == "" {
		return TrustResult{}, fmt.Errorf("acp: ACP_TrustPeer: card is missing agent_id")
	}
	res, err := a.verifier.Verify(ctx, card)
	if err != nil {
		return TrustResult{}, fmt.Errorf("acp: ACP_TrustPeer: verifier error: %w", err)
	}
	if !res.Accepted {
		return TrustResult{}, fmt.Errorf("acp: ACP_TrustPeer: card rejected: %s", res.Reason)
	}
	// Derive transport from EndpointURL prefix.
	transport := inferTransport(card.EndpointURL)
	profile := acp.PeerProfile{
		PeerID:       card.AgentID,
		EndpointURL:  card.EndpointURL,
		Transport:    transport,
		CardSource:   acp.CardSourceInline,
		InlineCard:   &card,
	}
	a.peerStor.trust(profile, card)
	return TrustResult{
		PeerID:    card.AgentID,
		TrustTier: "verified",
	}, nil
}

// ACP_RevokePeer removes the peer from the runtime store and invalidates
// outstanding envelopes. FR-003.
func (a *API) ACP_RevokePeer(_ context.Context, peerID string) error {
	if peerID == "" {
		return fmt.Errorf("acp: ACP_RevokePeer: peer_id must not be empty")
	}
	if !a.peerStor.revoke(peerID) {
		return fmt.Errorf("acp: ACP_RevokePeer: peer %q not found", peerID)
	}
	return nil
}

// ACP_Dispatch builds an envelope, runs the Cedar acp_send gate, sends via
// the matched transport, and returns an envelope_id. FR-004.
func (a *API) ACP_Dispatch(ctx context.Context, peerID, turnPayload string) (DispatchResult, error) {
	if peerID == "" {
		return DispatchResult{}, fmt.Errorf("acp: ACP_Dispatch: peer_id must not be empty")
	}
	profile, meta, ok := a.peerStor.lookup(peerID)
	if !ok {
		return DispatchResult{}, fmt.Errorf("acp: ACP_Dispatch: peer %q not found", peerID)
	}

	// Cedar gate: acp_send
	if a.cedarEng != nil {
		attrs := map[string]interface{}{
			"peer_id":         peerID,
			"peer_trust_tier": meta.trustTier,
			"transport":       string(profile.Transport),
			"direction":       "send",
		}
		// Pass the raw peerID; the EngineAdapter builds the proper
		// ACPEnvelope::"<peerID>" EntityUID via cedar.ACPEnvelopeUID.
		outcome, reason, err := a.cedarEng.Check(ctx,
			cedar.PrincipalLocal, cedar.ActionACPSend, peerID,
			attrs,
		)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("acp: ACP_Dispatch: cedar error: %w", err)
		}
		if outcome == cedar.Deny {
			envelopeID := newEnvelopeID()
			t := EnvelopeTrace{
				EnvelopeID: envelopeID,
				PeerID:     peerID,
				Transport:  string(profile.Transport),
				Direction:  "send",
				Outcome:    "denied_by_policy",
				ErrorCode:  "acp:policy_denied",
				CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			}
			a.traces.put(t)
			emitACPEnvelope(ctx, a.auditEm, t)
			return DispatchResult{}, fmt.Errorf("acp: ACP_Dispatch: policy denied: %s", reason)
		}
	}

	// Parse turn payload as raw JSON; fall back to string-wrapping.
	var body json.RawMessage
	if json.Valid([]byte(turnPayload)) {
		body = json.RawMessage(turnPayload)
	} else {
		b, _ := json.Marshal(turnPayload)
		body = b
	}

	envelopeID := newEnvelopeID()
	bytesSent := int64(len(body))

	a2aTaskID, stream, err := a.env.Dispatch(ctx, profile, "", body)
	var outcome, errorCode string
	if err != nil {
		outcome = "transport_error"
		errorCode = "acp:transport_error"
	} else {
		outcome = "ok"
		_ = a2aTaskID
		// drain the stream so goroutines don't leak
		go func() {
			if stream != nil {
				for range stream {
				}
			}
		}()
	}

	t := EnvelopeTrace{
		EnvelopeID: envelopeID,
		PeerID:     peerID,
		Transport:  string(profile.Transport),
		Direction:  "send",
		Outcome:    outcome,
		BytesOut:   bytesSent,
		ErrorCode:  errorCode,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	a.traces.put(t)
	emitACPEnvelope(ctx, a.auditEm, t)
	a.peerStor.touchLastSeen(peerID)

	if err != nil {
		return DispatchResult{}, fmt.Errorf("acp: ACP_Dispatch: %w", err)
	}
	return DispatchResult{EnvelopeID: envelopeID}, nil
}

// ACP_GetTrace returns the full envelope lifecycle for the given envelope_id.
// FR-005.
func (a *API) ACP_GetTrace(_ context.Context, envelopeID string) (EnvelopeTrace, error) {
	if envelopeID == "" {
		return EnvelopeTrace{}, fmt.Errorf("acp: ACP_GetTrace: envelope_id must not be empty")
	}
	t, ok := a.traces.get(envelopeID)
	if !ok {
		return EnvelopeTrace{}, fmt.Errorf("acp: ACP_GetTrace: envelope %q not found", envelopeID)
	}
	return t, nil
}

// ACP_ListTraces returns the most-recent envelope traces for the given
// peerID, newest first. Returns an empty slice (not an error) when no
// traces exist for the peer yet. FR-005.
func (a *API) ACP_ListTraces(_ context.Context, peerID string) ([]EnvelopeTrace, error) {
	if peerID == "" {
		return nil, fmt.Errorf("acp: ACP_ListTraces: peer_id must not be empty")
	}
	return a.traces.listForPeer(peerID), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func toPeer(p acp.PeerProfile, m peerMeta) Peer {
	w := Peer{
		ID:          p.PeerID,
		Transport:   string(p.Transport),
		TrustTier:   m.trustTier,
		EndpointURL: p.EndpointURL,
	}
	// Prefer the stored fingerprint (computed at trust time); fall back
	// to computing it fresh from the inline card if available.
	if m.cardFingerprint != "" {
		w.CardFingerprint = m.cardFingerprint
	} else if p.InlineCard != nil {
		w.CardFingerprint = cardFingerprint(*p.InlineCard)
	}
	if p.InlineCard != nil {
		w.AgentName = p.InlineCard.Name
		w.AgentDescription = p.InlineCard.Description
	}
	if !m.lastSeen.IsZero() {
		w.LastSeen = m.lastSeen.Format(time.RFC3339)
	}
	return w
}

func cardFingerprint(card acp.AgentCard) string {
	// v1: derive a short fingerprint from the agent_id + version.
	// The a2a-signed-cards-trust mission will replace this with a real
	// Ed25519 key fingerprint once signing is available.
	if card.AgentID == "" {
		return ""
	}
	return card.AgentID + "@" + card.Version
}

func inferTransport(endpointURL string) acp.TransportKind {
	hasPrefix := func(s, p string) bool {
		return len(s) >= len(p) && s[:len(p)] == p
	}
	switch {
	case hasPrefix(endpointURL, "unix://"):
		return acp.TransportUDS
	case hasPrefix(endpointURL, "http://127.0.0.1"):
		return acp.TransportLoopback
	case hasPrefix(endpointURL, "http://localhost"):
		return acp.TransportLoopback
	case hasPrefix(endpointURL, "https://127.0.0.1"):
		return acp.TransportLoopback
	case hasPrefix(endpointURL, "https://localhost"):
		return acp.TransportLoopback
	// IPv6 loopback: http://[::1] or https://[::1]
	case hasPrefix(endpointURL, "http://[::1]"):
		return acp.TransportLoopback
	case hasPrefix(endpointURL, "https://[::1]"):
		return acp.TransportLoopback
	default:
		return acp.TransportLAN
	}
}

// emitACPEnvelope fires a KindACPEnvelope audit event (fire-and-forget).
func emitACPEnvelope(ctx context.Context, em coreaudit.Emitter, t EnvelopeTrace) {
	if em == nil {
		return
	}
	payload := coreaudit.ACPEnvelopePayload{
		EnvelopeID: t.EnvelopeID,
		PeerID:     t.PeerID,
		Transport:  t.Transport,
		Direction:  t.Direction,
		Outcome:    t.Outcome,
		BytesIn:    t.BytesIn,
		BytesOut:   t.BytesOut,
		ErrorCode:  t.ErrorCode,
	}
	coreaudit.MustEmit(ctx, em, coreaudit.KindACPEnvelope, payload, time.Now().UTC())
}

// newEnvelopeID mints a cryptographically-random opaque envelope
// identifier. Uses crypto/rand so collisions are negligible even at
// high dispatch rates; the "env_" prefix keeps it human-distinguishable
// from other IDs in audit logs.
func newEnvelopeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic; fall back to a
		// time-based suffix so the caller can still record a trace.
		return fmt.Sprintf("env_fallback_%d", time.Now().UnixNano())
	}
	return "env_" + hex.EncodeToString(b)
}

// NullAPI is a no-op implementation returned when the ACP layer is not
// wired (e.g. when running without a configured peer registry). Every
// method returns a descriptive error so the frontend can surface a clear
// "not configured" state.
type NullAPI struct{}

// NewNullAPI returns a NullAPI.
func NewNullAPI() *NullAPI { return &NullAPI{} }

func (NullAPI) ACP_ListPeers(_ context.Context) ([]Peer, error) {
	return nil, nil
}
func (NullAPI) ACP_TrustPeer(_ context.Context, _ string) (TrustResult, error) {
	return TrustResult{}, fmt.Errorf("acp: not configured")
}
func (NullAPI) ACP_RevokePeer(_ context.Context, _ string) error {
	return fmt.Errorf("acp: not configured")
}
func (NullAPI) ACP_Dispatch(_ context.Context, _, _ string) (DispatchResult, error) {
	return DispatchResult{}, fmt.Errorf("acp: not configured")
}
func (NullAPI) ACP_GetTrace(_ context.Context, _ string) (EnvelopeTrace, error) {
	return EnvelopeTrace{}, fmt.Errorf("acp: not configured")
}
func (NullAPI) ACP_ListTraces(_ context.Context, _ string) ([]EnvelopeTrace, error) {
	return nil, nil
}

// ── ensure *API and NullAPI both satisfy ACPAPI ──────────────────────────

var _ ACPAPI = (*API)(nil)
var _ ACPAPI = NullAPI{}

// ── Cedar engine adapter ──────────────────────────────────────────────────

// EngineAdapter wraps a *cedar.Engine (the harness's concrete engine from
// core/policy/cedar) and makes it satisfy the CedarEngine interface used
// by this view. The adapter translates the string-keyed principal/resource
// arguments into the cedar.EntityUID type the engine expects, and converts
// context attrs from map[string]interface{} to map[cedarlib.String]cedarlib.Value.
//
// Callers in api.go should wrap the concrete *cedar.Engine with NewEngineAdapter
// before passing it in acpview.Options.Cedar.
type EngineAdapter struct {
	eng *cedar.Engine
}

// NewEngineAdapter constructs an EngineAdapter. eng must not be nil.
func NewEngineAdapter(eng *cedar.Engine) *EngineAdapter {
	return &EngineAdapter{eng: eng}
}

// Check implements CedarEngine. For ACP calls the view passes
// cedar.PrincipalLocal ("local") as principal and the raw peerID as
// resource; this method builds the proper Cedar EntityUIDs from those
// values. attrs is the context map built by ACP_Dispatch / ACP_Receive.
func (a *EngineAdapter) Check(
	ctx context.Context,
	principal, action, resource string,
	attrs map[string]interface{},
) (cedar.Outcome, string, error) {
	// Translate principal: "local" → User::"local" via cedar.UserUID().
	var principalUID cedarlib.EntityUID
	if principal == cedar.PrincipalLocal {
		principalUID = cedar.UserUID()
	} else {
		principalUID = cedarlib.NewEntityUID(cedar.EntityTypeUser, cedarlib.String(principal))
	}

	// Translate resource: resource is the raw peerID; build the
	// ACPEnvelope::"<peerID>" entity UID via the canonical helper so
	// the id is validated before evaluation.
	resourceUID := cedar.ACPEnvelopeUID(resource)

	// Build context attrs: convert map[string]interface{} →
	// map[cedarlib.String]cedarlib.Value so the engine can evaluate
	// attribute conditions like `context.peer_trust_tier == "verified"`.
	ctxMap := make(map[cedarlib.String]cedarlib.Value, len(attrs))
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			ctxMap[cedarlib.String(k)] = cedarlib.String(val)
		case bool:
			ctxMap[cedarlib.String(k)] = cedarlib.Boolean(val)
		case int:
			ctxMap[cedarlib.String(k)] = cedarlib.Long(val)
		case int64:
			ctxMap[cedarlib.String(k)] = cedarlib.Long(val)
		default:
			// Best-effort: marshal to string so the attribute is present
			// (even if not type-correct for the policy).
			ctxMap[cedarlib.String(k)] = cedarlib.String(fmt.Sprintf("%v", val))
		}
	}

	dec := a.eng.Evaluate(ctx, principalUID, action, resourceUID, ctxMap)
	return dec.Outcome, dec.Reason, nil
}

// DefaultRegistry returns a new peers.Registry seeded with no profiles.
// Useful for constructing the API in the zero-configuration path.
func DefaultRegistry() RegistryIface {
	return peers.NewRegistry(nil, peers.NoopEmitter{})
}

// DefaultEnvelope returns a new envelope.Envelope with no transports
// registered (in-memory only). Used in the demo and test paths.
func DefaultEnvelope() *envelope.Envelope {
	return envelope.New()
}
