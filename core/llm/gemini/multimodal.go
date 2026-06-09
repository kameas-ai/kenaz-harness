package gemini

import (
	"strings"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// allowedImageMIMEs is the set of MIME types Gemini accepts inline via
// inlineData parts. GIF is intentionally excluded — the Gemini API
// returns a 400 error for image/gif inputs.
var allowedImageMIMEs = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/heic": true,
	"image/heif": true,
}

// normalizeImageMIME normalises minor MIME type variations that arrive
// in practice. It applies before the allow-list check.
func normalizeImageMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpg":
		return "image/jpeg"
	default:
		return strings.ToLower(strings.TrimSpace(mime))
	}
}

// encodeImagePart converts an llm.MediaSource into a Gemini inlineData part.
//
// Constraints:
//   - Kind=="uri" → ErrInvalidRequest (File API out of scope).
//   - image/gif → ErrInvalidRequest.
//   - Other unsupported MIME types → ErrInvalidRequest.
func encodeImagePart(src *llm.MediaSource) (geminiPart, error) {
	if src == nil {
		return geminiPart{}, &llm.ErrInvalidRequest{
			Status:  0,
			Message: "gemini: nil MediaSource",
		}
	}
	// URI sources require the Gemini File API — out of scope.
	if src.Kind == "uri" {
		return geminiPart{}, &llm.ErrInvalidRequest{
			Status:  0,
			Message: "gemini: MediaSource.Kind==\"uri\" requires the File API (out of scope); use base64 inline data",
		}
	}

	mime := normalizeImageMIME(src.MediaType)

	// GIF is explicitly rejected.
	if mime == "image/gif" {
		return geminiPart{}, &llm.ErrInvalidRequest{
			Status:  0,
			Message: "gemini: image/gif is not supported; convert to PNG, JPEG, or WebP",
		}
	}

	if !allowedImageMIMEs[mime] {
		return geminiPart{}, &llm.ErrInvalidRequest{
			Status:  0,
			Message: "gemini: unsupported image MIME type: " + src.MediaType,
		}
	}

	return geminiPart{
		InlineData: &geminiInlineData{
			MimeType: mime,
			Data:     src.Data,
		},
	}, nil
}

// decodeInlineData converts a Gemini inlineData part back to an llm.MediaSource.
// Used by tests and future output-decode paths.
func decodeInlineData(id *geminiInlineData) *llm.MediaSource {
	if id == nil {
		return nil
	}
	return &llm.MediaSource{
		Kind:      "base64",
		MediaType: id.MimeType,
		Data:      id.Data,
	}
}
