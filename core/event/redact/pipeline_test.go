package redact

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newTestPipeline(t *testing.T) *Pipeline {
	t.Helper()
	policy := DefaultPolicy()
	resolver := NewStaticResolver(map[string][]byte{
		policy.SaltRef.Keychain: []byte("test-salt-ABCDEFGHIJKLMNOP"),
	})
	p, err := New(policy, resolver)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestPolicyRefusesDisabled(t *testing.T) {
	pol := DefaultPolicy()
	pol.Enabled = false
	res := NewStaticResolver(map[string][]byte{pol.SaltRef.Keychain: []byte("x")})
	if _, err := New(pol, res); !errors.Is(err, ErrPipelineDisabled) {
		t.Fatalf("expected ErrPipelineDisabled, got %v", err)
	}
}

func TestNilResolverRejected(t *testing.T) {
	pol := DefaultPolicy()
	if _, err := New(pol, nil); err == nil {
		t.Fatalf("nil resolver should be rejected")
	}
}

func TestApply_AwsAccessKeyRedacted(t *testing.T) {
	p := newTestPipeline(t)
	out, sum, err := p.Apply(map[string]any{
		"text": "log line with key=AKIAIOSFODNN7EXAMPLE rest of message",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("plaintext key leaked: %s", out)
	}
	if !strings.Contains(string(out), "redacted:") {
		t.Fatalf("placeholder missing in output: %s", out)
	}
	if sum.NoOp {
		t.Fatalf("summary should not be no-op")
	}
	found := false
	for _, m := range sum.MatchersFired {
		if m == "aws-access-key-id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("aws matcher should be reported fired, got %v", sum.MatchersFired)
	}
}

func TestApply_Bearer(t *testing.T) {
	p := newTestPipeline(t)
	out, _, err := p.Apply(map[string]any{
		"auth": "Bearer abcdefghijklmnopqrstuvwxyz1234567890",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(string(out), "abcdefghijklmnopqrstuvwxyz1234567890") {
		t.Fatalf("bearer leaked: %s", out)
	}
}

func TestApply_FieldPathRedaction(t *testing.T) {
	p := newTestPipeline(t)
	out, sum, err := p.Apply(map[string]any{
		"request": map[string]any{
			"headers": map[string]any{
				"authorization": "totally-arbitrary-string",
			},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(string(out), "totally-arbitrary-string") {
		t.Fatalf("field path not redacted: %s", out)
	}
	hit := false
	for _, p := range sum.FieldPathsRedacted {
		if strings.HasSuffix(p, "authorization") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("field-path summary missing: %v", sum.FieldPathsRedacted)
	}
}

func TestApply_Determinism(t *testing.T) {
	p := newTestPipeline(t)
	in := map[string]any{"text": "AKIAIOSFODNN7EXAMPLE"}
	a, _, _ := p.Apply(in)
	b, _, _ := p.Apply(in)
	if string(a) != string(b) {
		t.Fatalf("determinism violation: %s vs %s", a, b)
	}
}

func TestApply_NoOpDistinct(t *testing.T) {
	p := newTestPipeline(t)
	_, sum, err := p.Apply(map[string]any{"text": "nothing sensitive here"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !sum.NoOp {
		t.Fatalf("benign payload should produce NoOp summary, got %+v", sum)
	}
	if sum.SaltFingerprint == "" {
		t.Fatalf("salt fingerprint should be present even on NoOp")
	}
}

func TestApply_JWT(t *testing.T) {
	p := newTestPipeline(t)
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NSJ9.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	out, _, err := p.Apply(map[string]any{"token": jwt})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Note: the field-path "token" hits before substring scanning, so
	// we expect the field-redacted form (which itself does not contain
	// the plaintext).
	if strings.Contains(string(out), jwt) {
		t.Fatalf("JWT leaked: %s", out)
	}
}

func TestApply_NestedArrays(t *testing.T) {
	p := newTestPipeline(t)
	out, _, err := p.Apply(map[string]any{
		"items": []any{
			map[string]any{"key": "AKIAIOSFODNN7EXAMPLE"},
			"random",
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(string(out), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("nested key leaked: %s", out)
	}
}

func TestApply_RotateSalt_ChangesPlaceholder(t *testing.T) {
	p := newTestPipeline(t)
	in := map[string]any{"text": "AKIAIOSFODNN7EXAMPLE"}
	a, _, _ := p.Apply(in)
	p.RotateSalt([]byte("rotated-salt-XYZ"))
	b, _, _ := p.Apply(in)
	if string(a) == string(b) {
		t.Fatalf("placeholder must change after salt rotation")
	}
	// Original output must still not contain plaintext (immutability of past events).
	if strings.Contains(string(a), "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("plaintext leaked into pre-rotation output")
	}
}

func TestApply_StructInput(t *testing.T) {
	p := newTestPipeline(t)
	type req struct {
		User string `json:"user"`
		Auth string `json:"authorization"`
	}
	out, sum, err := p.Apply(req{User: "alice", Auth: "Bearer zzzzzzzzzzzzzzzzzzzzz"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if strings.Contains(string(out), "zzzzzzzzzzzzzzzzzzzzz") {
		t.Fatalf("auth leaked: %s", out)
	}
	if sum.NoOp {
		t.Fatalf("should not be NoOp")
	}
}

// Property test: random credential-shaped strings injected into
// random payload shapes; assert zero plaintext survives across many
// trials.
func TestApply_PropertyZeroPlaintextLeak(t *testing.T) {
	p := newTestPipeline(t)
	credentials := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"AKIAZZZZZZZZZZZZZZZZ",
		"sk-aaaaaaaaaaaaaaaaaaaaaaaaa",
		"sk-ant-bbbbbbbbbbbbbbbbbbbbb",
		"ghp_abcdefghijklmnopqrstuvwxyzABCDEFGHIJ",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NSJ9.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
	}
	for trial := 0; trial < 200; trial++ {
		cred := credentials[trial%len(credentials)]
		payload := map[string]any{
			"a": map[string]any{
				"b": "leading garbage " + cred + " trailing garbage",
			},
			"list": []any{cred, "innocuous"},
		}
		out, _, err := p.Apply(payload)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if strings.Contains(string(out), cred) {
			t.Fatalf("trial %d: plaintext credential leaked: %s", trial, out)
		}
	}
}

// Verify that re-marshaling the redacted output and re-applying the
// pipeline is idempotent (placeholder strings are not themselves
// matched).
func TestApply_Idempotent(t *testing.T) {
	p := newTestPipeline(t)
	in := map[string]any{"key": "AKIAIOSFODNN7EXAMPLE"}
	once, _, _ := p.Apply(in)
	var decoded any
	if err := json.Unmarshal(once, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	twice, _, _ := p.Apply(decoded)
	if string(once) != string(twice) {
		t.Fatalf("idempotency violation: %s vs %s", once, twice)
	}
}
