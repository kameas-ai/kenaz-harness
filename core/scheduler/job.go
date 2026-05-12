package scheduler

import "github.com/sigil-tech/kaneaz-harness/core/session"

// JobKind identifies the dispatch path for a Job.
//
// "session" (the zero value) is the legacy kind used by the
// workflow-schedule path; it dispatches via the workflow runner.
// "chat_run" is the new kind introduced by
// scheduled-chat-runs-01KX5R8B: it dispatches via ChatRunDispatcher
// using a prompt template.
type JobKind string

const (
	// JobKindSession is the legacy kind — backward-compatible default.
	// Jobs with an empty Kind field are treated as JobKindSession.
	JobKindSession JobKind = "session"
	// JobKindChatRun is the scheduled-chat-run kind.
	// Jobs with Kind == JobKindChatRun carry a non-nil ChatRun field.
	JobKindChatRun JobKind = "chat_run"
)

// ChatRunSpec carries the chat-run–specific dispatch parameters.
// Populated when Job.Kind == JobKindChatRun; nil otherwise.
type ChatRunSpec struct {
	// ID is the scheduled_chat_runs.id; used to load the latest record
	// at dispatch time so inline edits (model, sink, template) take
	// effect without re-registering the cron schedule.
	ID string `json:"id"`
	// PromptTemplate is the raw template string. Interpolation variables
	// are resolved at dispatch time:
	//   {{date}}      — current date in YYYY-MM-DD (UTC)
	//   {{time}}      — current time in HH:MM (UTC)
	//   {{cron_expr}} — the cron expression that fired
	PromptTemplate string `json:"prompt_template"`
	// Model is the target model override. Empty means "use the active
	// default from the settings store".
	Model string `json:"model,omitempty"`
	// OutputSink controls where dispatch output is routed:
	//   "banner"      — Wails runtime event scheduled-chat:banner
	//   "file:<path>" — write UTF-8 text to the given file path
	//   "none"        — silent (history-only)
	OutputSink string `json:"output_sink,omitempty"`
}

type MissedPolicy string

const (
	MissedSkip             MissedPolicy = "skip"
	MissedRunOnResume      MissedPolicy = "run_on_resume"
	MissedRunOnResumeWithin MissedPolicy = "run_on_resume_within"
)

type Trigger struct {
	Cron string `json:"cron"`
	TZ   string `json:"tz,omitempty"`
}

type OnMissed struct {
	Policy MissedPolicy `json:"policy"`
	Within string       `json:"within,omitempty"`
}

type Job struct {
	ID       string       `json:"id"`
	// Kind identifies the dispatch path. Defaults to JobKindSession for
	// backward-compatibility when the field is absent in persisted records.
	Kind     JobKind      `json:"kind,omitempty"`
	// Spec carries the session-dispatch parameters (used when Kind ==
	// JobKindSession or Kind == ""). Ignored when Kind == JobKindChatRun.
	Spec     session.Spec `json:"spec"`
	// ChatRun carries chat-run dispatch parameters. Non-nil when and only
	// when Kind == JobKindChatRun.
	ChatRun  *ChatRunSpec `json:"chat_run,omitempty"`
	Trigger  Trigger      `json:"trigger"`
	OnMissed OnMissed     `json:"on_missed"`
	Enabled  bool         `json:"enabled"`
	LastRun  int64        `json:"last_run,omitempty"`
	NextRun  int64        `json:"next_run,omitempty"`
}

// EffectiveKind returns the resolved Kind, treating an empty string as
// JobKindSession for backward compatibility with persisted records that
// predate the Kind field.
func (j Job) EffectiveKind() JobKind {
	if j.Kind == "" {
		return JobKindSession
	}
	return j.Kind
}
