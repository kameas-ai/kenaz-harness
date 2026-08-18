package slashcmd

import "context"

// stubCommand is a kept building block for commands registered before
// their real implementation lands — see cmd_stubs_test.go for the
// dated keep-decision (engineer-truth-pass-01PMTP01 WP07). It is NOT
// currently used by the four v1 commands it once described: /memorize,
// /recall, /forget (cmd_memory.go) and /branch (cmd_branch.go) are all
// real today and return ComingSoon() == false, along with every other
// registered command. stubCommand is the only ComingSoon()==true
// implementation in the package, and nothing registers it — so today
// it has zero non-test callers. It stays because a future command that
// needs to advertise itself before it is wired needs exactly this
// shape, and CLAUDE.md's disposition rubric treats a reusable building
// block differently from dead application logic.
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
