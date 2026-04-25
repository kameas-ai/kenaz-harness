package policy_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/policy"
)

// fakeKind is a minimal ControlKind used to exercise the registry.
type fakeKind struct {
	name           string
	posture        policy.FailurePosture
	validateErr    error
	lowerOut       string
	mergeErr       error
}

func (f *fakeKind) Kind() string                                { return f.name }
func (f *fakeKind) FailurePostureDefault() policy.FailurePosture { return f.posture }
func (f *fakeKind) ValidateParams(string, map[string]any) error  { return f.validateErr }
func (f *fakeKind) LowerToRego(policy.Clause) (string, error)    { return f.lowerOut, nil }
func (f *fakeKind) NarrowingMerge(_ policy.Clause, child policy.Clause) (policy.Clause, error) {
	if f.mergeErr != nil {
		return policy.Clause{}, f.mergeErr
	}
	return child, nil
}

func resetRegistry() { policy.UnregisterAllForTest() }

func TestRegisterLookup(t *testing.T) {
	resetRegistry()
	k := &fakeKind{name: "fake_kind", posture: policy.FailClosed, lowerOut: "package x"}
	policy.Register(k)

	got, ok := policy.Lookup("fake_kind")
	if !ok {
		t.Fatalf("Lookup miss")
	}
	if got.Kind() != "fake_kind" {
		t.Fatalf("Kind() = %q, want fake_kind", got.Kind())
	}
	if got.FailurePostureDefault() != policy.FailClosed {
		t.Fatalf("posture mismatch: %s", got.FailurePostureDefault())
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetRegistry()
	policy.Register(&fakeKind{name: "dup"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate registration")
		}
	}()
	policy.Register(&fakeKind{name: "dup"})
}

func TestRegisterNilPanics(t *testing.T) {
	resetRegistry()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil ControlKind")
		}
	}()
	policy.Register(nil)
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	resetRegistry()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on empty Kind() name")
		}
	}()
	policy.Register(&fakeKind{name: ""})
}

func TestRegisteredKindsSortedDeterministic(t *testing.T) {
	resetRegistry()
	policy.Register(&fakeKind{name: "zebra"})
	policy.Register(&fakeKind{name: "apple"})
	policy.Register(&fakeKind{name: "mango"})
	got := policy.RegisteredKinds()
	want := []string{"apple", "mango", "zebra"}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("expected sorted output; got %v", got)
	}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestResolveClauseKindsRejectsUnknown(t *testing.T) {
	resetRegistry()
	policy.Register(&fakeKind{name: "known"})
	artifacts := []policy.PolicyArtifact{{
		PolicyID: "p1",
		Clauses: []policy.Clause{
			{ClauseID: "c1", Kind: "known"},
			{ClauseID: "c2", Kind: "unknown"},
		},
	}}
	err := policy.ResolveClauseKinds(artifacts)
	if err == nil {
		t.Fatalf("expected unknown-kind error")
	}
	var uk *policy.UnknownKindError
	if !errors.As(err, &uk) {
		t.Fatalf("expected *UnknownKindError, got %T", err)
	}
	if uk.ClauseID != "c2" || uk.Kind != "unknown" {
		t.Fatalf("error shape unexpected: %+v", uk)
	}
}

func TestMustLookupPanicsOnMiss(t *testing.T) {
	resetRegistry()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on MustLookup miss")
		}
	}()
	policy.MustLookup("missing")
}
