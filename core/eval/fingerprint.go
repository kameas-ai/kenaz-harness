package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// FingerprintRequest computes a stable SHA-256 fingerprint of the
// GenerationRequest that identifies the logical LLM call for caching
// purposes. The fingerprint is computed over the canonical JSON of a
// reduced shape: (profile_id, model, system, messages). Parameters such as
// retry policy or session_id are intentionally excluded so that two
// logically identical requests in different sessions produce the same key.
//
// The messages array is serialized using the redacted form (text content only)
// so that image / document attachments do not cause cache misses on minor
// binary differences. This is a best-effort cache key; --allow-live falls
// through on misses.
func FingerprintRequest(req corellm.GenerationRequest) string {
	type canonMsg struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
	}
	type canonReq struct {
		ProfileID string      `json:"profile_id"`
		Model     string      `json:"model,omitempty"`
		System    string      `json:"system,omitempty"`
		Messages  []canonMsg  `json:"messages"`
	}
	cr := canonReq{
		ProfileID: req.ProfileID,
		Model:     req.Model,
		System:    req.System,
	}
	for _, m := range req.Messages {
		cm := canonMsg{Role: string(m.Role)}
		for _, b := range m.Content {
			if b.Type == "text" || b.Type == "" {
				cm.Content = append(cm.Content, struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}{Type: "text", Text: b.Text})
			}
		}
		cr.Messages = append(cr.Messages, cm)
	}
	raw, _ := json.Marshal(cr)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
