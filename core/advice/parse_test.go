package advice

import "testing"

func TestValidateConfidence(t *testing.T) {
	if ValidateConfidence(-1) == nil {
		t.Error("ValidateConfidence(-1) = nil, want an error")
	}
	if ValidateConfidence(0) != nil {
		t.Error("ValidateConfidence(0) != nil, want nil")
	}
	if ValidateConfidence(100) != nil {
		t.Error("ValidateConfidence(100) != nil, want nil")
	}
	if ValidateConfidence(101) == nil {
		t.Error("ValidateConfidence(101) = nil, want an error")
	}
}

func TestFeaturesHash_DeterministicForEqualStructs(t *testing.T) {
	type f struct {
		A string
		B int
	}
	h1, err := FeaturesHash(f{A: "x", B: 1})
	if err != nil {
		t.Fatalf("FeaturesHash: %v", err)
	}
	h2, err := FeaturesHash(f{A: "x", B: 1})
	if err != nil {
		t.Fatalf("FeaturesHash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hashes differ for materially identical features: %q vs %q", h1, h2)
	}
	h3, err := FeaturesHash(f{A: "x", B: 2})
	if err != nil {
		t.Fatalf("FeaturesHash: %v", err)
	}
	if h1 == h3 {
		t.Error("hashes match for materially DIFFERENT features")
	}
}

func TestFeaturesHash_UnmarshalableErrors(t *testing.T) {
	if _, err := FeaturesHash(make(chan int)); err == nil {
		t.Fatal("expected an error hashing an unmarshalable value, got nil")
	}
}
