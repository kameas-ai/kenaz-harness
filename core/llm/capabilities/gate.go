package capabilities

import (
	"strings"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// Gate enforces FR-013: a request that opts into a capability the
// (provider, model) does not support fails before any wire call.
type Gate struct {
	cat *Catalog
}

// NewGate returns a Gate bound to cat.
func NewGate(cat *Catalog) *Gate {
	return &Gate{cat: cat}
}

// Check returns ErrCapabilityUnsupported when req opts into any
// capability the descriptor for (profile.Kind, profile.Model) does
// not advertise. The descriptor is also returned so callers can
// reuse it (e.g., adapters that only emit cache markers when the
// model supports caching).
//
// prof.CapabilityHints overlays onto the static descriptor before the
// check runs (model-settings-reach-the-model-01PMZ101 WP14 / FR-017,
// register D-2: "WIRE CapabilityHints, PROBE-DRIVEN"). The static
// catalog baseline (custom-openai.yaml / ollama.yaml / the per-model
// YAML rows) is computed first and always applies; a hint only
// overrides the specific keys the caller (Registry, populated from a
// live endpoint probe via CapabilityCache) actually set. An absent
// hint leaves the static baseline untouched, which is what keeps a
// probe failure or cache miss from degrading a request the curated
// data already answers correctly (spec A-5: "the static baseline ...
// lands FIRST and stays"; the probe result merges over it, never
// replaces it).
func (g *Gate) Check(req llm.GenerationRequest, prof llm.ProviderProfile) (llm.CapabilityDescriptor, error) {
	desc := g.cat.Describe(prof.Kind, prof.Model)
	applyCapabilityHints(&desc, prof.CapabilityHints)
	want := req.RequestedCapabilities()
	missing := make([]llm.Capability, 0)
	for _, c := range want {
		if !desc.Has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return desc, &llm.ErrCapabilityUnsupported{
			Provider:     prof.Kind,
			Model:        prof.Model,
			Capabilities: missing,
		}
	}
	return desc, nil
}

// defaultVisionMimeTypes is the MIME allow-list used when a
// CapVision=true hint would otherwise leave ImageInputMimeTypes empty.
// mimeAllowed treats an empty list as "feature disabled" (gate.go's own
// comment on mimeAllowed), so a hint that flips ImageInput on without
// also populating this list would refuse every image while looking
// wired — exactly the half-fix spec re-derivation finding R-5 warns
// about. This is the same MIME set custom-openai.yaml and ollama.yaml
// already curate for their vision-capable model rows.
var defaultVisionMimeTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// CheckAttachments validates every image/document ContentBlock in
// req.Messages against the resolved AttachmentDescriptor for
// (prof.Kind, prof.Model). Returns the first violation as a typed error.
// MUST be called before the adapter's wire call (multimodal-io-01KQ8TDF FR-008).
//
// prof.CapabilityHints[llm.CapVision] overlays onto ImageInput the same
// way Check overlays onto Supported (WP14 / FR-017, R-5). This is a
// SEPARATE projection from Check's — AttachmentDescriptor has no
// map[Capability]bool of its own, and CapabilityDescriptor (which
// ToDescriptor produces) is never consulted here, so overlaying hints
// only in Check would leave every image attachment refused for a
// profile whose probe found real vision support. See
// applyVisionHintToAttachments.
//
// Validation order per block:
//  1. Audio MIME types (audio/*) → ErrAttachmentAudioUnsupported (unconditional).
//  2. Image block: assert ImageInput; assert MIME in ImageInputMimeTypes;
//     assert SizeBytes ≤ MaxImageBytes (when non-zero); assert pixel count
//     ≤ MaxImagePixels (when both cap and dims are set).
//  3. Document block: assert DocumentInput; assert MIME in DocumentInputMimeTypes;
//     assert SizeBytes ≤ MaxDocumentBytes (when non-zero).
//  4. After full pass: assert per-message image count ≤ MaxImageCountPerMessage.
func (g *Gate) CheckAttachments(req llm.GenerationRequest, prof llm.ProviderProfile) error {
	desc := g.cat.AttachmentLimits(prof.Kind, prof.Model)
	applyVisionHintToAttachments(&desc, prof.CapabilityHints)

	for _, msg := range req.Messages {
		imageCount := 0
		for _, block := range msg.Content {
			switch block.Type {
			case "image":
				// Unconditionally reject audio (shouldn't appear as image type,
				// but guard against mislabelled blocks).
				if block.Source != nil && strings.HasPrefix(block.Source.MediaType, "audio/") {
					return &llm.ErrAttachmentAudioUnsupported{Mime: block.Source.MediaType}
				}
				// Image input enabled?
				if !desc.ImageInput {
					return &llm.ErrAttachmentMimeUnsupported{
						Provider: prof.Kind,
						Mime:     mimeOf(block.Source),
					}
				}
				// MIME type allowed?
				if !mimeAllowed(mimeOf(block.Source), desc.ImageInputMimeTypes) {
					return &llm.ErrAttachmentMimeUnsupported{
						Provider: prof.Kind,
						Mime:     mimeOf(block.Source),
					}
				}
				// Byte size cap.
				if block.Source != nil && block.Source.SizeBytes > 0 && desc.MaxImageBytes > 0 {
					if block.Source.SizeBytes > desc.MaxImageBytes {
						return &llm.ErrAttachmentTooLarge{
							Provider: prof.Kind,
							Mime:     block.Source.MediaType,
							Given:    block.Source.SizeBytes,
							Cap:      desc.MaxImageBytes,
						}
					}
				}
				// Pixel cap (best-effort: skipped when dims absent).
				if block.Source != nil && block.Source.ImageDimensions != nil && desc.MaxImagePixels > 0 {
					pixels := block.Source.ImageDimensions.Pixels()
					if pixels > 0 && pixels > desc.MaxImagePixels {
						return &llm.ErrAttachmentDimensionExceeded{
							Provider: prof.Kind,
							Given:    pixels,
							Cap:      desc.MaxImagePixels,
						}
					}
				}
				imageCount++

			case "document":
				// Check for audio masquerading as document.
				if block.Source != nil && strings.HasPrefix(block.Source.MediaType, "audio/") {
					return &llm.ErrAttachmentAudioUnsupported{Mime: block.Source.MediaType}
				}
				// Document input enabled?
				if !desc.DocumentInput {
					return &llm.ErrAttachmentMimeUnsupported{
						Provider: prof.Kind,
						Mime:     mimeOf(block.Source),
					}
				}
				// MIME type allowed?
				if !mimeAllowed(mimeOf(block.Source), desc.DocumentInputMimeTypes) {
					return &llm.ErrAttachmentMimeUnsupported{
						Provider: prof.Kind,
						Mime:     mimeOf(block.Source),
					}
				}
				// Byte size cap.
				if block.Source != nil && block.Source.SizeBytes > 0 && desc.MaxDocumentBytes > 0 {
					if block.Source.SizeBytes > desc.MaxDocumentBytes {
						return &llm.ErrAttachmentTooLarge{
							Provider: prof.Kind,
							Mime:     block.Source.MediaType,
							Given:    block.Source.SizeBytes,
							Cap:      desc.MaxDocumentBytes,
						}
					}
				}

			default:
				// text / tool_use / tool_result — check for audio sneaking in as text.
				// Purposely very narrow: only reject if a Source field exists AND
				// the mime is audio (shouldn't happen in practice).
				if block.Source != nil && strings.HasPrefix(block.Source.MediaType, "audio/") {
					return &llm.ErrAttachmentAudioUnsupported{Mime: block.Source.MediaType}
				}
			}
		}

		// Per-message image count cap.
		if desc.MaxImageCountPerMessage > 0 && imageCount > desc.MaxImageCountPerMessage {
			return &llm.ErrAttachmentCountExceeded{
				Provider: prof.Kind,
				Given:    imageCount,
				Cap:      desc.MaxImageCountPerMessage,
			}
		}
	}
	return nil
}

// applyCapabilityHints overlays hints onto desc.Supported in place.
// hints is prof.CapabilityHints — a nil or empty map is a no-op, which
// is the common case (no probe has run for this profile yet, or the
// adapter has no CapabilitiesProvider implementation). Each hint key
// wins over whatever the static catalog said for that specific
// capability; keys the hint map is silent on are left exactly as the
// catalog produced them.
func applyCapabilityHints(desc *llm.CapabilityDescriptor, hints map[llm.Capability]bool) {
	if len(hints) == 0 {
		return
	}
	if desc.Supported == nil {
		desc.Supported = map[llm.Capability]bool{}
	}
	for cap, v := range hints {
		desc.Supported[cap] = v
	}
}

// applyVisionHintToAttachments overlays hints[llm.CapVision] onto
// desc.ImageInput. When the hint turns vision on and the catalog
// baseline left ImageInputMimeTypes empty, it is populated from
// defaultVisionMimeTypes — otherwise mimeAllowed's empty-list-means-
// disabled rule would refuse every image despite ImageInput=true (R-5).
// A hint that turns vision off is honoured as-is; no MIME list is
// needed in that case since CheckAttachments never reaches it.
func applyVisionHintToAttachments(desc *AttachmentDescriptor, hints map[llm.Capability]bool) {
	v, ok := hints[llm.CapVision]
	if !ok {
		return
	}
	desc.ImageInput = v
	if v && len(desc.ImageInputMimeTypes) == 0 {
		desc.ImageInputMimeTypes = defaultVisionMimeTypes
	}
}

// mimeOf safely returns MediaSource.MediaType or "" when src is nil.
func mimeOf(src *llm.MediaSource) string {
	if src == nil {
		return ""
	}
	return src.MediaType
}

// mimeAllowed returns true when mime is in allowed, or when allowed is empty
// (empty list means "any", which defaults to the safe set in YAML). Comparison
// is case-insensitive.
func mimeAllowed(mime string, allowed []string) bool {
	if len(allowed) == 0 {
		return false // no allowed types means the feature is disabled
	}
	lower := strings.ToLower(mime)
	for _, a := range allowed {
		if strings.ToLower(a) == lower {
			return true
		}
	}
	return false
}
