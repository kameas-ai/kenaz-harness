package session

import "github.com/kameas-ai/kenaz-harness/core/bundle"

type Spec struct {
	BundleSet    []bundle.Dependency `json:"bundle_set"`
	PromptRef    string              `json:"prompt_ref,omitempty"`
	InitialInput string              `json:"initial_input,omitempty"`
	Model        string              `json:"model"`
	Params       map[string]any      `json:"params,omitempty"`
	ParentEvent  *ParentRef          `json:"parent_event,omitempty"`
}

type ParentRef struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting_for_input"
	StatusCompleted Status = "completed"
	StatusErrored   Status = "errored"
	StatusInterrupt Status = "interrupted"
)

type Session struct {
	ID        string `json:"id"`
	Spec      Spec   `json:"spec"`
	Status    Status `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}
