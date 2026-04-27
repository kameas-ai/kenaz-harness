package slashcmd

import "context"

// stubCommand is the shared shape for the four v1 commands whose
// real implementations live in the agent-kernel-graph mission. Each
// returns the canned coming-soon body via comingSoonResult so the
// surface advertises the full API today.
type stubCommand struct {
	name        string
	description string
}

func (s stubCommand) Name() string        { return s.name }
func (s stubCommand) Description() string { return s.description }
func (s stubCommand) Hidden() bool        { return false }
func (s stubCommand) ComingSoon() bool    { return true }

func (s stubCommand) Run(_ context.Context, _ Env, _ []string) (Result, error) {
	return comingSoonResult(s.name), nil
}
