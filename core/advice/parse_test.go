package advice

import "testing"

func TestParseRecommendation_HappyPath(t *testing.T) {
	decision, confidence, err := ParseRecommendation(`{"decision": true, "confidence": 42}`)
	if err != nil {
		t.Fatalf("ParseRecommendation: %v", err)
	}
	if !decision || confidence != 42 {
		t.Errorf("got (%v, %d), want (true, 42)", decision, confidence)
	}
}

func TestParseRecommendation_TolerantOfMarkdownFence(t *testing.T) {
	decision, confidence, err := ParseRecommendation("```json\n{\"decision\": false, \"confidence\": 5}\n```")
	if err != nil {
		t.Fatalf("ParseRecommendation: %v", err)
	}
	if decision || confidence != 5 {
		t.Errorf("got (%v, %d), want (false, 5)", decision, confidence)
	}
}

func TestParseRecommendation_NoJSONObjectErrors(t *testing.T) {
	if _, _, err := ParseRecommendation("I refuse to answer in JSON."); err == nil {
		t.Fatal("expected an error for a response with no JSON object, got nil")
	}
}

func TestParseRecommendation_MalformedJSONErrors(t *testing.T) {
	if _, _, err := ParseRecommendation(`{"decision": true, "confidence": }`); err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

func TestParseRecommendation_OutOfRangeConfidenceErrors(t *testing.T) {
	cases := []string{
		`{"decision": true, "confidence": -1}`,
		`{"decision": true, "confidence": 101}`,
	}
	for _, c := range cases {
		if _, _, err := ParseRecommendation(c); err == nil {
			t.Errorf("ParseRecommendation(%q) = nil error, want an error (never clamp)", c)
		}
	}
}

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
