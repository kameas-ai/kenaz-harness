package fallback

import (
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		triggers    []TriggerCondition
		wantMatch   bool
		wantTrigger TriggerCondition
	}{
		{
			name:      "nil error never matches",
			err:       nil,
			triggers:  []TriggerCondition{TriggerErrorAny},
			wantMatch: false,
		},
		{
			name:      "empty triggers never match",
			err:       errors.New("some error"),
			triggers:  nil,
			wantMatch: false,
		},
		{
			name:        "TriggerErrorAny matches any non-nil error",
			err:         errors.New("arbitrary error"),
			triggers:    []TriggerCondition{TriggerErrorAny},
			wantMatch:   true,
			wantTrigger: TriggerErrorAny,
		},
		{
			name:        "ErrTransient 529 matches TriggerError5xx",
			err:         &llm.ErrTransient{Status: 529, Message: "overloaded"},
			triggers:    []TriggerCondition{TriggerError5xx},
			wantMatch:   true,
			wantTrigger: TriggerError5xx,
		},
		{
			name:        "ErrTransient 503 matches TriggerError5xx",
			err:         &llm.ErrTransient{Status: 503, Message: "service unavailable"},
			triggers:    []TriggerCondition{TriggerError5xx},
			wantMatch:   true,
			wantTrigger: TriggerError5xx,
		},
		{
			name:        "ErrTransient 429 matches TriggerError5xx (rate-limit-as-5xx)",
			err:         &llm.ErrTransient{Status: 429, Message: "rate limited"},
			triggers:    []TriggerCondition{TriggerError5xx},
			wantMatch:   true,
			wantTrigger: TriggerError5xx,
		},
		{
			name:        "ErrTransient 429 matches TriggerError429",
			err:         &llm.ErrTransient{Status: 429, Message: "rate limited"},
			triggers:    []TriggerCondition{TriggerError429},
			wantMatch:   true,
			wantTrigger: TriggerError429,
		},
		{
			name:      "ErrAuth does NOT match TriggerError5xx",
			err:       &llm.ErrAuth{Status: 401, Message: "unauthorized"},
			triggers:  []TriggerCondition{TriggerError5xx},
			wantMatch: false,
		},
		{
			name:        "ErrAuth matches TriggerErrorAuthFailed",
			err:         &llm.ErrAuth{Status: 401, Message: "unauthorized"},
			triggers:    []TriggerCondition{TriggerErrorAuthFailed},
			wantMatch:   true,
			wantTrigger: TriggerErrorAuthFailed,
		},
		{
			name: "ErrProviderAuthFailed matches TriggerErrorAuthFailed",
			err: &llm.ErrProviderAuthFailed{
				Provider: "anthropic",
				Cause:    &llm.ErrAuth{Status: 403, Message: "forbidden"},
			},
			triggers:    []TriggerCondition{TriggerErrorAuthFailed},
			wantMatch:   true,
			wantTrigger: TriggerErrorAuthFailed,
		},
		{
			name:        "ErrInvalidRequest 400 context overflow matches TriggerErrorContextOverflow",
			err:         &llm.ErrInvalidRequest{Status: 400, Message: "This model's maximum context length is 4096 tokens"},
			triggers:    []TriggerCondition{TriggerErrorContextOverflow},
			wantMatch:   true,
			wantTrigger: TriggerErrorContextOverflow,
		},
		{
			name:      "ErrInvalidRequest 400 safety block does NOT match TriggerErrorContextOverflow",
			err:       &llm.ErrInvalidRequest{Status: 400, Message: "Your request was blocked due to safety policy"},
			triggers:  []TriggerCondition{TriggerErrorContextOverflow},
			wantMatch: false,
		},
		{
			name:        "ErrInvalidRequest 400 safety matches TriggerErrorSafetyBlock",
			err:         &llm.ErrInvalidRequest{Status: 400, Message: "Your request was blocked due to safety policy"},
			triggers:    []TriggerCondition{TriggerErrorSafetyBlock},
			wantMatch:   true,
			wantTrigger: TriggerErrorSafetyBlock,
		},
		{
			name:      "ErrInvalidRequest 400 unknown message does NOT match safety",
			err:       &llm.ErrInvalidRequest{Status: 400, Message: "bad request parameter"},
			triggers:  []TriggerCondition{TriggerErrorSafetyBlock},
			wantMatch: false,
		},
		{
			name:        "first matching trigger wins in list",
			err:         &llm.ErrTransient{Status: 503},
			triggers:    []TriggerCondition{TriggerError429, TriggerError5xx, TriggerErrorAny},
			wantMatch:   true,
			wantTrigger: TriggerError5xx,
		},
		{
			name:        "TriggerErrorAny matches arbitrary error after specific miss",
			err:         errors.New("some other error"),
			triggers:    []TriggerCondition{TriggerError5xx, TriggerErrorAny},
			wantMatch:   true,
			wantTrigger: TriggerErrorAny,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotMatch, gotTrigger := Match(tc.err, tc.triggers)
			if gotMatch != tc.wantMatch {
				t.Errorf("Match() matched=%v, want %v", gotMatch, tc.wantMatch)
			}
			if tc.wantMatch && gotTrigger != tc.wantTrigger {
				t.Errorf("Match() trigger=%q, want %q", gotTrigger, tc.wantTrigger)
			}
		})
	}
}
