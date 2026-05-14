package export

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// jsonMeta is the header block embedded at the top of every JSON export.
type jsonMeta struct {
	ExportFormatVersion int    `json:"export_format_version"`
	HarnessVersion      string `json:"harness_version"`
	ExportedAt          string `json:"exported_at"`
	SessionID           string `json:"session_id"`
	ExportFormat        string `json:"export_format"`
}

// jsonSession is the session row in a JSON export.
type jsonSession struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	ContextKind  string `json:"context_kind,omitempty"`
}

// jsonMessage is a single message entry in the JSON export.
type jsonMessage struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Sequence  int64           `json:"sequence"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	CreatedAt string          `json:"created_at"`
	ToolCalls []jsonToolCall  `json:"tool_calls,omitempty"`
	Artifacts []jsonArtifact  `json:"artifacts,omitempty"`
}

// jsonToolCall mirrors a tool invocation in the JSON export.
type jsonToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    string         `json:"result,omitempty"`
}

// jsonArtifact carries inline attachment bytes as base64.
type jsonArtifact struct {
	// Name is the original filename or a generated name.
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	// DataBase64 is the attachment bytes encoded as standard base64.
	DataBase64 string `json:"data_base64"`
}

// jsonExport is the top-level envelope written to the .json file.
type jsonExport struct {
	Meta     jsonMeta      `json:"meta"`
	Session  jsonSession   `json:"session"`
	Messages []jsonMessage `json:"messages"`
}

// renderJSON produces the JSON export of a session.
func renderJSON(
	sess session.Record,
	msgs []session.Message,
	now time.Time,
) ([]byte, error) {
	meta := jsonMeta{
		ExportFormatVersion: ExportFormatVersion,
		HarnessVersion:      "kaneaz-harness",
		ExportedAt:          now.UTC().Format(time.RFC3339),
		SessionID:           sess.ID,
		ExportFormat:        string(FormatJSON),
	}

	jsess := jsonSession{
		ID:           sess.ID,
		Name:         sess.Name,
		CreatedAt:    sess.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    sess.UpdatedAt.UTC().Format(time.RFC3339),
		SystemPrompt: sess.SystemPrompt,
		ContextKind:  sess.ContextKind,
	}

	jmsgs := make([]jsonMessage, 0, len(msgs))
	for i, m := range msgs {
		jm := jsonMessage{
			ID:        m.ID,
			SessionID: m.SessionID,
			Sequence:  m.Sequence,
			Role:      string(m.Role),
			Content:   m.Content,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		}

		// Tool calls.
		for _, tc := range m.ToolCalls {
			jm.ToolCalls = append(jm.ToolCalls, jsonToolCall{
				ID:        tc.ID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Result:    tc.Result,
			})
		}

		// Attachments (inline base64).
		for bIdx, block := range m.ContentBlocks {
			if block.Source == nil || block.Source.Data == "" {
				continue
			}
			name := block.Source.OriginalName
			if name == "" {
				ext := mediaTypeExt(block.Source.MediaType)
				name = fmt.Sprintf("msg%d-attach%d%s", i+1, bIdx+1, ext)
			}
			jm.Artifacts = append(jm.Artifacts, jsonArtifact{
				Name:       name,
				MediaType:  block.Source.MediaType,
				DataBase64: base64.StdEncoding.EncodeToString([]byte(block.Source.Data)),
			})
		}

		jmsgs = append(jmsgs, jm)
	}

	payload := jsonExport{
		Meta:     meta,
		Session:  jsess,
		Messages: jmsgs,
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("json export: marshal: %w", err)
	}
	return b, nil
}
