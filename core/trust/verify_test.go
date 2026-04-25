package trust_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"kaneaz-harness/core/trust"
	"kaneaz-harness/core/trust/internal/fingerprint"
)

// signedFixture builds a freshly-signed Ed25519 envelope plus the
// matching anchor. Used by every pipeline test below.
type signedFixture struct {
	pub    ed25519.PublicKey
	priv   ed25519.PrivateKey
	keyID  string
	anchor trust.Anchor
}

func newSignedFixture(t *testing.T, anchorID, agentID string) signedFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID := fingerprint.Compute(pub)
	return signedFixture{
		pub:   pub,
		priv:  priv,
		keyID: keyID,
		anchor: trust.Anchor{
			AnchorID:  anchorID,
			Kind:      trust.AnchorPinnedPeer,
			PeerID:    agentID,
			Algorithm: trust.AlgEd25519,
			PublicKey: trust.PublicKey{
				Algorithm:   trust.AlgEd25519,
				Bytes:       append([]byte(nil), pub...),
				Fingerprint: keyID,
			},
		},
	}
}

func (f signedFixture) sign(payload []byte, agentID string) trust.Envelope {
	sig := ed25519.Sign(f.priv, payload)
	return trust.Envelope{
		Payload:   payload,
		Signature: sig,
		Algorithm: trust.AlgEd25519,
		KeyID:     f.keyID,
		IssuedAt:  time.Now().UTC(),
		AgentID:   agentID,
	}
}

func newEngine(t *testing.T) (trust.TrustEngine, *trust.MemoryEmitter) {
	t.Helper()
	em := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{}, nil, em)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	return eng, em
}

// TestVerifyHappyPath — end-to-end accept of a freshly-signed envelope.
// Asserts NFR-005 (exactly one audit emission) and that the result is
// shaped per the (Decision=accepted, AnchorID set, RejectionCode empty)
// invariant.
func TestVerifyHappyPath(t *testing.T) {
	eng, em := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	em.Reset()

	payload := []byte("hello, world")
	env := fx.sign(payload, "agent-1")
	res, err := eng.Verify(context.Background(), payload, env, trust.VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Decision != trust.DecisionAccepted {
		t.Fatalf("Decision = %v, want accepted", res.Decision)
	}
	if res.AnchorID != "anc-1" {
		t.Fatalf("AnchorID = %q, want %q", res.AnchorID, "anc-1")
	}
	if res.RejectionCode != "" {
		t.Errorf("accepted result must have empty RejectionCode; got %q", res.RejectionCode)
	}
	events := em.Events()
	if len(events) != 1 || events[0].Kind != trust.EventVerificationAccepted {
		t.Fatalf("expected exactly one verification-accepted event, got %v", events)
	}
}

// TestVerifyTamperDetected — single-byte rewrite of the payload after
// signing fails verification. Spec US2 / SC-002.
func TestVerifyTamperDetected(t *testing.T) {
	eng, em := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}

	payload := []byte("untouched payload")
	env := fx.sign(payload, "agent-1")
	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0x01
	em.Reset()

	res, err := eng.Verify(context.Background(), tampered, env, trust.VerifyOptions{})
	if err == nil {
		t.Fatal("Verify accepted tampered payload; expected error")
	}
	if res.Decision != trust.DecisionRejected {
		t.Fatalf("Decision = %v, want rejected", res.Decision)
	}
	if res.RejectionCode != trust.RejSignatureInvalid {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejSignatureInvalid)
	}
	var rej *trust.RejectionError
	if !errors.As(err, &rej) {
		t.Fatalf("error is not a *RejectionError: %T", err)
	}
	events := em.Events()
	if len(events) != 1 || events[0].Kind != trust.EventVerificationRejected {
		t.Fatalf("expected exactly one verification-rejected event, got %v", events)
	}
}

// TestVerifyAlgorithmNotPermitted — operator policy rejects the alg
// before signature math (FR-004 / RejAlgorithmNotPermit).
func TestVerifyAlgorithmNotPermitted(t *testing.T) {
	em := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{
		AlgorithmPolicy: trust.AlgorithmPolicy{Allowed: []trust.Algorithm{trust.AlgECDSAP256}},
	}, nil, em)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	env := fx.sign([]byte("p"), "agent-1")
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejAlgorithmNotPermit {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejAlgorithmNotPermit)
	}
}

// TestVerifyAnchorMissing — no anchor matches the envelope's key id.
func TestVerifyAnchorMissing(t *testing.T) {
	eng, _ := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	// Intentionally do not install the anchor.
	env := fx.sign([]byte("p"), "agent-1")
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejAnchorMissing {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejAnchorMissing)
	}
}

// TestVerifyAnchorRemoved — installed-then-removed anchor produces a
// distinct code from anchor_missing (spec edge case).
func TestVerifyAnchorRemoved(t *testing.T) {
	eng, _ := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	if err := eng.RemoveAnchor(context.Background(), "anc-1"); err != nil {
		t.Fatalf("RemoveAnchor: %v", err)
	}
	env := fx.sign([]byte("p"), "agent-1")
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejAnchorRemoved {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejAnchorRemoved)
	}
}

// TestVerifyKeyRevoked — revocation by key id rejects subsequent
// envelopes (FR-006).
func TestVerifyKeyRevoked(t *testing.T) {
	eng, _ := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	if err := eng.IngestRevocation(context.Background(), trust.RevocationRecord{
		RevocationID: "rev-1",
		SubjectKind:  trust.RevocationByKeyID,
		SubjectID:    fx.keyID,
		EffectiveAt:  time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("IngestRevocation: %v", err)
	}
	env := fx.sign([]byte("p"), "agent-1")
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejKeyRevoked {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejKeyRevoked)
	}
}

// TestVerifyClockSkewExceeded — issued_at outside the configured
// tolerance is rejected as clock_skew_exceeded (FR-016) — distinct
// from signature_invalid.
func TestVerifyClockSkewExceeded(t *testing.T) {
	em := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{
		ClockSkew: trust.ClockSkewPolicy{Tolerance: time.Second},
	}, nil, em)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	now := time.Now().UTC()
	env := fx.sign([]byte("p"), "agent-1")
	env.IssuedAt = now.Add(-1 * time.Hour)
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{Now: now})
	if res.RejectionCode != trust.RejClockSkewExceeded {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejClockSkewExceeded)
	}
}

// TestVerifyChainDepthExceeded — defense-in-depth chain limit.
func TestVerifyChainDepthExceeded(t *testing.T) {
	em := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{
		ChainDepth: trust.ChainDepthPolicy{Max: 1},
	}, nil, em)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	env := fx.sign([]byte("p"), "agent-1")
	env.Chain = []string{"a", "b", "c", "d"}
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejChainDepthExceeded {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejChainDepthExceeded)
	}
}

// TestVerifySignatureInvalidOnBadShape — empty signature / payload
// fails the shape gate as signature_invalid.
func TestVerifySignatureInvalidOnBadShape(t *testing.T) {
	eng, _ := newEngine(t)
	res, _ := eng.Verify(context.Background(), []byte("p"), trust.Envelope{}, trust.VerifyOptions{})
	if res.RejectionCode != trust.RejSignatureInvalid {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejSignatureInvalid)
	}
}

// TestVerifyRotationGracePeriod — verification against the previous
// key during the overlap window succeeds with CacheState=grace
// (FR-013).
func TestVerifyRotationGracePeriod(t *testing.T) {
	eng, _ := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}

	// Generate a brand-new key and rotate to it with a 24h overlap.
	newPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	newKey := trust.PublicKey{
		Algorithm:   trust.AlgEd25519,
		Bytes:       append([]byte(nil), newPub...),
		Fingerprint: fingerprint.Compute(newPub),
	}
	if err := eng.BeginRotation(context.Background(), "anc-1", newKey, 24*time.Hour); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}

	// Sign with the *previous* key — the original priv from fx.
	env := fx.sign([]byte("payload"), "agent-1")
	res, err := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	if err != nil {
		t.Fatalf("Verify in grace period failed: %v", err)
	}
	if res.Decision != trust.DecisionAccepted {
		t.Fatalf("grace-period verify Decision = %v, want accepted", res.Decision)
	}
	if res.CacheState != trust.CacheGrace {
		t.Fatalf("grace-period CacheState = %v, want grace (FR-013 signal)", res.CacheState)
	}
}

// TestVerifyKeyExpired — verification against the previous key after
// the overlap window has lapsed produces RejKeyExpired.
func TestVerifyKeyExpired(t *testing.T) {
	em := trust.NewMemoryEmitter()
	eng, err := trust.NewEngineWithEmitter(trust.Config{
		// Wide clock-skew tolerance so this test isolates the
		// rotation-overlap-lapsed failure mode without tripping the
		// clock-skew gate.
		ClockSkew: trust.ClockSkewPolicy{Tolerance: 24 * time.Hour},
	}, nil, em)
	if err != nil {
		t.Fatalf("NewEngineWithEmitter: %v", err)
	}
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	newPub, _, _ := ed25519.GenerateKey(rand.Reader)
	newKey := trust.PublicKey{
		Algorithm:   trust.AlgEd25519,
		Bytes:       append([]byte(nil), newPub...),
		Fingerprint: fingerprint.Compute(newPub),
	}
	// 1ns overlap so by the time we verify it is already lapsed.
	if err := eng.BeginRotation(context.Background(), "anc-1", newKey, time.Nanosecond); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	env := fx.sign([]byte("p"), "agent-1")
	// Use Now well after the overlap window so withinGrace returns
	// false. ClockSkew tolerance is wide so we stay in the rotation
	// branch.
	res, _ := eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{
		Now: time.Now().UTC().Add(time.Hour),
	})
	if res.RejectionCode != trust.RejKeyExpired {
		t.Fatalf("RejectionCode = %q, want %q", res.RejectionCode, trust.RejKeyExpired)
	}
}

// TestVerifyIdentityCollision — two anchors claim the same agent_id
// with different fingerprints; the second presentation is rejected.
func TestVerifyIdentityCollision(t *testing.T) {
	eng, _ := newEngine(t)
	fx1 := newSignedFixture(t, "anc-1", "shared-agent")
	fx2 := newSignedFixture(t, "anc-2", "shared-agent")
	if err := eng.InstallAnchor(context.Background(), fx1.anchor); err != nil {
		t.Fatalf("InstallAnchor 1: %v", err)
	}

	// First verify binds shared-agent to fx1's fingerprint.
	env1 := fx1.sign([]byte("p"), "shared-agent")
	if _, err := eng.Verify(context.Background(), env1.Payload, env1, trust.VerifyOptions{}); err != nil {
		t.Fatalf("first verify: %v", err)
	}

	// Now install fx2 — different key, same agent_id. Anchor store
	// should refuse identity collision; the verify pipeline will also
	// catch it on the wire if InstallAnchor is bypassed.
	err := eng.InstallAnchor(context.Background(), fx2.anchor)
	if !errors.Is(err, trust.ErrIdentityCollision) {
		t.Fatalf("InstallAnchor with colliding agent_id returned %v, want ErrIdentityCollision (FR-015)", err)
	}
}

// TestVerifyEventInvariant — exactly-one VerificationResult ↔
// exactly-one audit emission, regardless of decision.
func TestVerifyEventInvariant(t *testing.T) {
	eng, em := newEngine(t)
	fx := newSignedFixture(t, "anc-1", "agent-1")
	if err := eng.InstallAnchor(context.Background(), fx.anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}
	em.Reset()
	for i := 0; i < 5; i++ {
		env := fx.sign([]byte("p"), "agent-1")
		_, _ = eng.Verify(context.Background(), env.Payload, env, trust.VerifyOptions{})
	}
	if got := len(em.Events()); got != 5 {
		t.Fatalf("got %d audit events for 5 verify calls, want 5 (NFR-005 / C-005)", got)
	}
}
