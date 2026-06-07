package scheduler_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

func TestRenderPromptTemplate(t *testing.T) {
	now := time.Date(2026, 5, 12, 9, 30, 0, 0, time.UTC)
	job := scheduler.Job{
		Trigger: scheduler.Trigger{Cron: "0 9 * * *"},
	}

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "date variable",
			template: "Today is {{date}}",
			want:     "Today is 2026-05-12",
		},
		{
			name:     "time variable",
			template: "It is {{time}} UTC",
			want:     "It is 09:30 UTC",
		},
		{
			name:     "cron_expr variable",
			template: "Fired by {{cron_expr}}",
			want:     "Fired by 0 9 * * *",
		},
		{
			name:     "no variables",
			template: "Static prompt",
			want:     "Static prompt",
		},
		{
			name:     "all variables",
			template: "Date={{date}} Time={{time}} Cron={{cron_expr}}",
			want:     "Date=2026-05-12 Time=09:30 Cron=0 9 * * *",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduler.RenderPromptTemplate(tc.template, job, now)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseOutputSink(t *testing.T) {
	tests := []struct {
		raw      string
		wantKind string
		wantPath string
		wantErr  bool
	}{
		{"", "banner", "", false},
		{"banner", "banner", "", false},
		{"none", "none", "", false},
		{"file:/tmp/out.txt", "file", "/tmp/out.txt", false},
		{"file:", "file", "", true},
		{"email:foo@bar.com", "none", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			kind, path, err := scheduler.ParseOutputSink(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Errorf("ParseOutputSink(%q) err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
			}
			if kind != tc.wantKind {
				t.Errorf("ParseOutputSink(%q) kind=%q, want %q", tc.raw, kind, tc.wantKind)
			}
			if path != tc.wantPath {
				t.Errorf("ParseOutputSink(%q) path=%q, want %q", tc.raw, path, tc.wantPath)
			}
		})
	}
}

func TestNoopChatRunDispatcher(t *testing.T) {
	d := scheduler.NoopChatRunDispatcher{}
	now := time.Now()
	job := scheduler.Job{
		Kind: scheduler.JobKindChatRun,
		ChatRun: &scheduler.ChatRunSpec{
			ID:             "cr-001",
			PromptTemplate: "Hello {{date}}",
			OutputSink:     "banner",
		},
		Trigger: scheduler.Trigger{Cron: "0 9 * * *"},
	}

	rec, err := d.DispatchChatRun(context.Background(), job, now)
	if err != nil {
		t.Fatalf("DispatchChatRun returned error: %v", err)
	}
	if rec.Status != "completed" {
		t.Errorf("status=%q, want completed", rec.Status)
	}
	if rec.ChatRunID != "cr-001" {
		t.Errorf("chatRunID=%q, want cr-001", rec.ChatRunID)
	}
	if !strings.Contains(rec.OutputSnippet, "noop") {
		t.Errorf("output_snippet=%q, expected to contain 'noop'", rec.OutputSnippet)
	}
	if rec.EndedAt == nil {
		t.Error("EndedAt should be set by noop dispatcher")
	}
}

func TestJobEffectiveKind(t *testing.T) {
	tests := []struct {
		kind scheduler.JobKind
		want scheduler.JobKind
	}{
		{"", scheduler.JobKindSession},
		{scheduler.JobKindSession, scheduler.JobKindSession},
		{scheduler.JobKindChatRun, scheduler.JobKindChatRun},
	}
	for _, tc := range tests {
		j := scheduler.Job{Kind: tc.kind}
		if got := j.EffectiveKind(); got != tc.want {
			t.Errorf("Job{Kind:%q}.EffectiveKind() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}
