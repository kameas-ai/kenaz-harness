package scheduler

import "github.com/sigil-tech/kaneaz-harness/core/session"

type MissedPolicy string

const (
	MissedSkip                 MissedPolicy = "skip"
	MissedRunOnResume          MissedPolicy = "run_on_resume"
	MissedRunOnResumeWithin    MissedPolicy = "run_on_resume_within"
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
	Spec     session.Spec `json:"spec"`
	Trigger  Trigger      `json:"trigger"`
	OnMissed OnMissed     `json:"on_missed"`
	Enabled  bool         `json:"enabled"`
	LastRun  int64        `json:"last_run,omitempty"`
	NextRun  int64        `json:"next_run,omitempty"`
}
