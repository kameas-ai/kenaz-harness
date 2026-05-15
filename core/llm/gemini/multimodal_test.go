package gemini

import (
	"errors"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// TestEncodeImagePart_PNG verifies PNG encodes to inlineData correctly.
func TestEncodeImagePart_PNG(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{
		Kind:      "base64",
		MediaType: "image/png",
		Data:      "iVBORw0KGgo=", // minimal base64
	}
	part, err := encodeImagePart(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.InlineData == nil {
		t.Fatal("expected inlineData, got nil")
	}
	if part.InlineData.MimeType != "image/png" {
		t.Errorf("want mimeType=image/png, got %q", part.InlineData.MimeType)
	}
	if part.InlineData.Data != src.Data {
		t.Errorf("data mismatch: got %q, want %q", part.InlineData.Data, src.Data)
	}
}

// TestEncodeImagePart_JPEG verifies JPEG encodes correctly.
func TestEncodeImagePart_JPEG(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/jpeg", Data: "/9j/AA=="}
	part, err := encodeImagePart(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.InlineData.MimeType != "image/jpeg" {
		t.Errorf("want image/jpeg, got %q", part.InlineData.MimeType)
	}
}

// TestEncodeImagePart_WebP verifies WebP encodes correctly.
func TestEncodeImagePart_WebP(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/webp", Data: "UklGRg=="}
	part, err := encodeImagePart(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.InlineData.MimeType != "image/webp" {
		t.Errorf("want image/webp, got %q", part.InlineData.MimeType)
	}
}

// TestEncodeImagePart_HEIC verifies HEIC encodes correctly.
func TestEncodeImagePart_HEIC(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/heic", Data: "AAAAFAAA"}
	part, err := encodeImagePart(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.InlineData.MimeType != "image/heic" {
		t.Errorf("want image/heic, got %q", part.InlineData.MimeType)
	}
}

// TestEncodeImagePart_JPGNormalized verifies image/jpg is normalised to image/jpeg.
func TestEncodeImagePart_JPGNormalized(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/jpg", Data: "/9j/AA=="}
	part, err := encodeImagePart(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if part.InlineData.MimeType != "image/jpeg" {
		t.Errorf("image/jpg should normalise to image/jpeg, got %q", part.InlineData.MimeType)
	}
}

// TestEncodeImagePart_GIF rejects GIF with ErrInvalidRequest.
func TestEncodeImagePart_GIF(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/gif", Data: "R0lGODlh"}
	_, err := encodeImagePart(src)
	if err == nil {
		t.Fatal("expected error for GIF")
	}
	var invalidReq *llm.ErrInvalidRequest
	if !errors.As(err, &invalidReq) {
		t.Errorf("want *ErrInvalidRequest, got %T: %v", err, err)
	}
}

// TestEncodeImagePart_URI rejects URI kind with ErrInvalidRequest.
func TestEncodeImagePart_URI(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "uri", MediaType: "image/png", URI: "gs://bucket/image.png"}
	_, err := encodeImagePart(src)
	if err == nil {
		t.Fatal("expected error for URI source")
	}
	var invalidReq *llm.ErrInvalidRequest
	if !errors.As(err, &invalidReq) {
		t.Errorf("want *ErrInvalidRequest for URI source, got %T: %v", err, err)
	}
}

// TestEncodeImagePart_UnsupportedMIME rejects an unsupported MIME type.
func TestEncodeImagePart_UnsupportedMIME(t *testing.T) {
	t.Parallel()
	src := &llm.MediaSource{Kind: "base64", MediaType: "image/bmp", Data: "AAAA"}
	_, err := encodeImagePart(src)
	if err == nil {
		t.Fatal("expected error for image/bmp")
	}
	var invalidReq *llm.ErrInvalidRequest
	if !errors.As(err, &invalidReq) {
		t.Errorf("want *ErrInvalidRequest for BMP, got %T: %v", err, err)
	}
}

// TestDecodeInlineData round-trips inlineData back to MediaSource with kind=base64.
func TestDecodeInlineData(t *testing.T) {
	t.Parallel()
	id := &geminiInlineData{MimeType: "image/png", Data: "abc123=="}
	src := decodeInlineData(id)
	if src == nil {
		t.Fatal("expected non-nil MediaSource")
	}
	if src.Kind != "base64" {
		t.Errorf("want kind=base64, got %q", src.Kind)
	}
	if src.MediaType != "image/png" {
		t.Errorf("want media_type=image/png, got %q", src.MediaType)
	}
	if src.Data != "abc123==" {
		t.Errorf("want data=abc123==, got %q", src.Data)
	}
}

// TestDecodeInlineData_Nil handles nil input gracefully.
func TestDecodeInlineData_Nil(t *testing.T) {
	t.Parallel()
	if decodeInlineData(nil) != nil {
		t.Error("decodeInlineData(nil) should return nil")
	}
}

// TestNormalizeImageMIME verifies various MIME normalizations.
func TestNormalizeImageMIME(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"image/jpg", "image/jpeg"},
		{"image/jpeg", "image/jpeg"},
		{"image/png", "image/png"},
		{"image/webp", "image/webp"},
		{"image/JPEG", "image/jpeg"},
		{"IMAGE/PNG", "image/png"},
		{"image/heic", "image/heic"},
	}
	for _, tc := range cases {
		got := normalizeImageMIME(tc.in)
		if got != tc.want {
			t.Errorf("normalizeImageMIME(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestToGeminiRequest_ImageBlock verifies that an image block in a message
// is translated to an inlineData part.
func TestToGeminiRequest_ImageBlock(t *testing.T) {
	t.Parallel()
	req := llm.GenerationRequest{
		Messages: []llm.Message{{
			Role: llm.RoleUser,
			Content: []llm.ContentBlock{
				{Type: "text", Text: "what is in this image?"},
				{
					Type: "image",
					Source: &llm.MediaSource{
						Kind:      "base64",
						MediaType: "image/png",
						Data:      "iVBORw0KGgo=",
					},
				},
			},
		}},
	}
	gr, err := ToGeminiRequest(req, llm.ProviderProfile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gr.Contents) != 1 {
		t.Fatalf("want 1 content, got %d", len(gr.Contents))
	}
	parts := gr.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("want 2 parts (text + image), got %d", len(parts))
	}
	if parts[0].Text != "what is in this image?" {
		t.Errorf("unexpected text part: %q", parts[0].Text)
	}
	if parts[1].InlineData == nil {
		t.Fatal("expected inlineData for image block")
	}
	if parts[1].InlineData.MimeType != "image/png" {
		t.Errorf("unexpected mime type: %q", parts[1].InlineData.MimeType)
	}
}
