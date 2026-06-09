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
func (g *Gate) Check(req llm.GenerationRequest, prof llm.ProviderProfile) (llm.CapabilityDescriptor, error) {
	desc := g.cat.Describe(prof.Kind, prof.Model)
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

// CheckAttachments validates every image/document ContentBlock in
// req.Messages against the resolved AttachmentDescriptor for
// (prof.Kind, prof.Model). Returns the first violation as a typed error.
// MUST be called before the adapter's wire call (multimodal-io-01KQ8TDF FR-008).
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
