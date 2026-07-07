package contextbootstrap

// confidence.go implements the corroboration-based confidence model (§9.1 /
// FR-005 / FR-006).
//
// Starting policy (tunable via recipe ConfidenceRules):
//   - Assert at ≥3 distinct sources/people, OR one trusted person.
//   - A trusted person counts as ≈3 corroborations (TrustedPersonWeight).
//   - Tentative/clarify below the assert threshold.

// scoreNode computes the confidence for a candidate node given the
// corroboration count + the set of corroborating sources, weighted by
// the trust map. Returns a value in [0.0, 1.0].
//
// The function uses a logistic-like curve so that the first corroborations
// matter most (marginal confidence gain decreases with each additional one).
func scoreNode(
	corroborations int,
	corroboratingSources []string,
	trustMap map[string]TrustedPerson,
	rules ConfidenceRules,
) (confidence float64, isAsserted bool) {
	if rules.AssertMinCorroborations <= 0 {
		rules.AssertMinCorroborations = 3
	}
	if rules.TrustedPersonWeight <= 0 {
		rules.TrustedPersonWeight = 3
	}

	// Compute effective corroboration count, boosted by trusted sources.
	effective := corroborations
	for _, src := range corroboratingSources {
		if tp, ok := trustMap[normalizeIdentifier(src)]; ok {
			// A trusted person adds (TrustedPersonWeight - 1) extra corroborations.
			bonus := rules.TrustedPersonWeight - 1
			if tp.TrustLevel == "high" {
				effective += bonus
			} else if tp.TrustLevel == "medium" {
				effective += bonus / 2
			}
			// low trust: no bonus
		}
	}

	// Compute confidence in [0.0, 1.0].
	// Simple model: confidence = min(effective / assertMin, 1.0)
	// capped to 0.95 to signal "very confident but not certain".
	assertMin := float64(rules.AssertMinCorroborations)
	if assertMin <= 0 {
		assertMin = 1
	}
	confidence = float64(effective) / assertMin
	if confidence > 0.95 {
		confidence = 0.95
	}
	if confidence < 0 {
		confidence = 0
	}
	isAsserted = effective >= rules.AssertMinCorroborations
	return confidence, isAsserted
}

// normalizeIdentifier lowercases and trims an identifier for trust-map lookup.
func normalizeIdentifier(s string) string {
	result := s
	// Simple lowercase; we avoid importing unicode to keep stdlib-only.
	lower := make([]byte, len(result))
	for i := 0; i < len(result); i++ {
		c := result[i]
		if c >= 'A' && c <= 'Z' {
			lower[i] = c + 32
		} else {
			lower[i] = c
		}
	}
	// Trim leading/trailing spaces.
	out := string(lower)
	start, end := 0, len(out)
	for start < end && out[start] == ' ' {
		start++
	}
	for end > start && out[end-1] == ' ' {
		end--
	}
	return out[start:end]
}

// buildTrustMap converts a []TrustedPerson slice into a lookup map keyed by
// normalized identifier.
func buildTrustMap(people []TrustedPerson) map[string]TrustedPerson {
	m := make(map[string]TrustedPerson, len(people))
	for _, p := range people {
		m[normalizeIdentifier(p.Identifier)] = p
	}
	return m
}
