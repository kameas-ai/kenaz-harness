package advice

// ChipConfidenceThreshold is spec §2d's conservative chip-render gate:
// "A chip renders only at confidence >= 75 ... Below-threshold
// recommendations are still CAPTURED as labels (shown=false) — the
// filter shapes UX, not the training corpus." Defined once, here, so
// the WP07 frontend-delivery gate (whatever renders the chip) and the
// WP08 label-capture bridge (core/advice/labels.CaptureAdvisor, which
// derives the Row.Shown propensity field) can never drift apart on what
// "shown" means — both compare a Recommendation's Confidence against
// this SAME constant rather than each hardcoding 75 independently.
const ChipConfidenceThreshold = 75

// ShouldShowChip reports whether rec clears spec §2d's chip-render gate:
// confidence >= ChipConfidenceThreshold AND Decision == true. A "no"
// decision is never shown regardless of confidence — spec's kinds all
// phrase their question as "should X happen", so a confident "no" is not
// something a passive chip should ever surface (nothing to accept).
func ShouldShowChip(rec Recommendation) bool {
	return rec.Decision && rec.Confidence >= ChipConfidenceThreshold
}
