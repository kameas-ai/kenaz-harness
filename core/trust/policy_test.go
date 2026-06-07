package trust_test

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// TestDefaultAlgorithmPolicyEd25519Only — Open Question 2 default.
func TestDefaultAlgorithmPolicyEd25519Only(t *testing.T) {
	p := trust.DefaultAlgorithmPolicy()
	if !p.Permits(trust.AlgEd25519) {
		t.Error("default policy must permit Ed25519")
	}
	if p.Permits(trust.AlgECDSAP256) {
		t.Error("default policy must NOT permit ECDSA-P256 at v1.0")
	}
	if p.Permits(trust.AlgRSAPSSSHA256) {
		t.Error("default policy must NOT permit RSA-PSS at v1.0")
	}
}

// TestClockSkewSymmetric — tolerance applies to both past and future.
func TestClockSkewSymmetric(t *testing.T) {
	p := trust.ClockSkewPolicy{Tolerance: time.Minute}
	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	if !p.Within(now.Add(30*time.Second), now) {
		t.Error("30s in future must be within 1m tolerance")
	}
	if !p.Within(now.Add(-30*time.Second), now) {
		t.Error("30s in past must be within 1m tolerance")
	}
	if p.Within(now.Add(2*time.Minute), now) {
		t.Error("2m in future must NOT be within 1m tolerance")
	}
}

// TestClockSkewZeroTimestampSkipped — zero envelope timestamps are
// treated as "skip this gate" so legacy envelopes still verify.
func TestClockSkewZeroTimestampSkipped(t *testing.T) {
	p := trust.ClockSkewPolicy{Tolerance: time.Second}
	if !p.Within(time.Time{}, time.Now()) {
		t.Error("zero envelope timestamp must skip the clock-skew gate")
	}
}

// TestChainDepthZeroIsRawOnly — Max=0 permits chain length 0 only.
func TestChainDepthZeroIsRawOnly(t *testing.T) {
	p := trust.ChainDepthPolicy{Max: 0}
	if !p.Permits(0) {
		t.Error("ChainDepth Max=0 must permit zero-length chain")
	}
	if p.Permits(1) {
		t.Error("ChainDepth Max=0 must reject any chain")
	}
}
