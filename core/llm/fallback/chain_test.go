package fallback

import (
	"testing"
)

func TestChainValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		chain   Chain
		wantErr bool
	}{
		{
			name: "valid minimal chain",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test Chain",
				Entries: []ChainEntry{
					{
						ProviderID: "openrouter",
						Triggers:   []TriggerCondition{TriggerError5xx},
					},
				},
			},
		},
		{
			name: "missing id",
			chain: Chain{
				Name: "No ID",
				Entries: []ChainEntry{
					{ProviderID: "openrouter", Triggers: []TriggerCondition{TriggerError5xx}},
				},
			},
			wantErr: true,
		},
		{
			name: "missing name",
			chain: Chain{
				ID: "no-name",
				Entries: []ChainEntry{
					{ProviderID: "openrouter", Triggers: []TriggerCondition{TriggerError5xx}},
				},
			},
			wantErr: true,
		},
		{
			name: "missing provider_id in entry",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test",
				Entries: []ChainEntry{
					{Triggers: []TriggerCondition{TriggerError5xx}},
				},
			},
			wantErr: true,
		},
		{
			name: "empty triggers in entry",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test",
				Entries: []ChainEntry{
					{ProviderID: "openrouter", Triggers: nil},
				},
			},
			wantErr: true,
		},
		{
			name: "unknown trigger condition",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test",
				Entries: []ChainEntry{
					{ProviderID: "openrouter", Triggers: []TriggerCondition{"error_unknown_xyz"}},
				},
			},
			wantErr: true,
		},
		{
			name: "max_attempts exceeds ceiling",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test",
				Entries: []ChainEntry{
					{
						ProviderID:  "openrouter",
						Triggers:    []TriggerCondition{TriggerError5xx},
						MaxAttempts: MaxWholeChainAttempts + 1,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "max_attempts at ceiling is OK",
			chain: Chain{
				ID:   "test-chain",
				Name: "Test",
				Entries: []ChainEntry{
					{
						ProviderID:  "openrouter",
						Triggers:    []TriggerCondition{TriggerError5xx},
						MaxAttempts: MaxWholeChainAttempts,
					},
				},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.chain.Validate()
			if tc.wantErr && err == nil {
				t.Error("Validate() = nil; want error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate() = %v; want nil", err)
			}
		})
	}
}

func TestErrFallbackExhausted(t *testing.T) {
	t.Parallel()

	inner := &testSentinelError{msg: "provider timeout"}
	e := &ErrFallbackExhausted{ChainID: "my-chain", Attempts: 5, LastErr: inner}

	if e.Error() == "" {
		t.Error("ErrFallbackExhausted.Error() must not be empty")
	}
	if e.Unwrap() != inner {
		t.Error("ErrFallbackExhausted.Unwrap() must return LastErr")
	}
}

type testSentinelError struct{ msg string }

func (e *testSentinelError) Error() string { return e.msg }
