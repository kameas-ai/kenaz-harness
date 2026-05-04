package onboarding

import "context"

// ProviderCounter returns the number of LLM providers currently
// configured. Wired to llm/registry.Registry in production.
type ProviderCounter interface {
	CountProviders(ctx context.Context) (int, error)
}

// CompletionReader returns whether Settings.OnboardingCompleted is set.
type CompletionReader interface {
	IsCompleted(ctx context.Context) (bool, error)
}

// FirstRunDetector composes ProviderCounter + CompletionReader so the
// rpc layer can ask one question instead of stitching the two flags
// itself. Mission harness-self-mcp-onboarding-01KQ8TDU WP08.
type FirstRunDetector struct {
	Providers  ProviderCounter
	Completion CompletionReader
}

// IsFirstRun returns true when:
//   - No providers are configured AND
//   - The user has not previously dismissed the onboarding dialog
//     (Settings.OnboardingCompleted == false).
//
// Either field may be nil — a nil ProviderCounter is treated as
// "0 providers"; a nil CompletionReader is treated as "not completed".
// This keeps the chassis-only test fixture compiling.
func (d FirstRunDetector) IsFirstRun(ctx context.Context) (bool, error) {
	if d.Completion != nil {
		c, err := d.Completion.IsCompleted(ctx)
		if err != nil {
			return false, err
		}
		if c {
			return false, nil
		}
	}
	count := 0
	if d.Providers != nil {
		n, err := d.Providers.CountProviders(ctx)
		if err != nil {
			return false, err
		}
		count = n
	}
	return count == 0, nil
}
