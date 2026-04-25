package trust

import "context"

// Preflight findings the v1.0 engine surfaces (plan §6.5). Codes are
// stable strings so operators can alert on them.
const (
	PreflightCodeBackendUnavailable     = "backend_unavailable"
	PreflightCodeAnchorAlgUnsupported   = "anchor_algorithm_unsupported"
	PreflightCodeAnchorKeyMalformed     = "anchor_key_malformed"
	PreflightCodeRevocationUnreachable  = "revocation_endpoint_unreachable"
	PreflightCodeAnchorRotationOverlap  = "anchor_rotation_overlap_lapsed"
)

// runPreflight is the engine's internal Preflight implementation.
// External callers reach it through [TrustEngine.Preflight]. Concrete
// findings depend on the configured backends; WP05 fills in the
// oskeychain-specific health probe.
func runPreflight(_ context.Context, _ Config, _ AnchorStore) ([]PreflightFinding, error) {
	// Placeholder — WP05 implements the full check matrix once the
	// oskeychain backend is wired.
	return []PreflightFinding{}, nil
}
