package verify

import (
	"context"
	"errors"
	"testing"

	"kaneaz-harness/core/acp"
)

func TestUnsignedAcceptVerifier(t *testing.T) {
	t.Parallel()
	v := UnsignedAcceptVerifier{}
	res, err := v.Verify(context.Background(), acp.AgentCard{Name: "test"})
	if err != nil {
		t.Fatalf("Verify err = %v", err)
	}
	if !res.Accepted {
		t.Fatalf("Verify Accepted = false, want true")
	}
	if !IsUnsignedAccept(v) {
		t.Fatalf("IsUnsignedAccept = false, want true")
	}
}

func TestRefusalVerifier(t *testing.T) {
	t.Parallel()
	v := RefusalVerifier{Reason: "no trust anchor"}
	res, err := v.Verify(context.Background(), acp.AgentCard{Name: "test"})
	if !errors.Is(err, acp.ErrVerifierRejected) {
		t.Fatalf("Verify err = %v, want ErrVerifierRejected", err)
	}
	if res.Accepted {
		t.Fatalf("Verify Accepted = true, want false")
	}
	if res.Reason != "no trust anchor" {
		t.Fatalf("Reason = %q, want %q", res.Reason, "no trust anchor")
	}
}

func TestRefusalVerifierDefaultReason(t *testing.T) {
	t.Parallel()
	v := RefusalVerifier{}
	res, _ := v.Verify(context.Background(), acp.AgentCard{Name: "test"})
	if res.Reason == "" {
		t.Fatal("default refusal reason must not be empty")
	}
}
