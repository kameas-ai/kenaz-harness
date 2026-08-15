package export

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
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
//
// FR-006, "consumers that predate the shape degrade to the final-answer
// view": `messages` carries exactly what it carried before the mission —
// the user turns and the answers — and a move-bearing turn's trajectory
// hangs off the answer in the additive `moves` array. A reader written
// against export_format_version 1 walks `messages`, ignores the field it
// does not know, and sees the conversation. Flattening the trajectory
// INTO `messages` would have handed that same reader thirteen messages
// per question with no way to tell which one was the answer.
type jsonMessage struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Sequence  int64          `json:"sequence"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	CreatedAt string         `json:"created_at"`
	ToolCalls []jsonToolCall `json:"tool_calls,omitempty"`
	Artifacts []jsonArtifact `json:"artifacts,omitempty"`

	// Kind / TurnSpanID identify a move-bearing answer. Empty on every
	// classic row, so omitempty reproduces the pre-mission bytes.
	Kind       string `json:"kind,omitempty"`
	TurnSpanID string `json:"turn_span_id,omitempty"`
	// Moves is the trajectory that produced this answer, in persisted
	// order. Absent on classic rows.
	Moves []jsonMove `json:"moves,omitempty"`
	// TrajectoryOnly marks a section with moves but no answer row: an
	// interrupted turn, or tool traffic that outlived the turn's last
	// piece of text. Content is empty because there was no answer.
	TrajectoryOnly bool `json:"trajectory_only,omitempty"`
}

// jsonMove is one entry of a turn's trajectory.
//
// Note what is NOT here: raw argument values. `args_summary` is the
// display layer's names-and-types rendering, and the model layer's raw
// arguments (session_messages.model_tool_args) are unreachable from this
// package. See moves.go for the contract.
type jsonMove struct {
	Kind        string `json:"kind"`
	MoveIndex   int    `json:"move_index"`
	TurnSpanID  string `json:"turn_span_id,omitempty"`
	Content     string `json:"content,omitempty"`
	ToolID      string `json:"tool_id,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	ArgsSummary string `json:"args_summary,omitempty"`
	Output      string `json:"output,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
}

// jsonToolCall mirrors a tool invocation in the JSON export.
//
// `arguments` was removed in WP05: it carried raw argument values, and a
// secret in a nested object or an array walked straight past the
// credential scanner into the exported file. `args_summary` replaces it
// with argument names and value types.
type jsonToolCall struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ArgsSummary string `json:"args_summary,omitempty"`
	Result      string `json:"result,omitempty"`
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
		HarnessVersion:      "kenaz-harness",
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

	items := projectExportItems(msgs)
	jmsgs := make([]jsonMessage, 0, len(items))
	for i, item := range items {
		m := item.Msg
		jm := jsonMessage{
			ID:             m.ID,
			SessionID:      m.SessionID,
			Sequence:       m.Sequence,
			Role:           string(m.Role),
			Content:        m.Content,
			CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
			Kind:           string(m.MoveKind()),
			TurnSpanID:     m.TurnSpanID(),
			TrajectoryOnly: item.TrailOnly,
		}

		// Tool calls.
		for _, tc := range m.ToolCalls {
			jm.ToolCalls = append(jm.ToolCalls, jsonToolCall{
				ID:          tc.ID,
				Name:        tc.Name,
				ArgsSummary: argsSummaryFromValues(tc.Arguments),
				Result:      capToolOutput(tc.Result),
			})
		}

		// The turn's trajectory (FR-006).
		for _, row := range item.Trail {
			d := describeMove(row)
			jm.Moves = append(jm.Moves, jsonMove{
				Kind:        d.Kind,
				MoveIndex:   d.MoveIndex,
				TurnSpanID:  d.TurnSpanID,
				Content:     d.Text,
				ToolID:      d.ToolID,
				ToolName:    d.ToolName,
				ArgsSummary: d.ArgsSummary,
				Output:      d.Output,
				IsError:     d.IsError,
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
