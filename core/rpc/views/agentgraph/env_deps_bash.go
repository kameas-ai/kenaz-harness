package agentgraph

import (
	"context"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// BashOutputStoreAdapter wraps core/tools/bash.Store onto the
// agentgraph.BashOutputStore interface. The bash tool writes Records
// keyed by run-id every Call; the kernel's read_bash_output executor
// reads them back through this adapter.
type BashOutputStoreAdapter struct {
	store *corebash.Store
}

// NewBashOutputStoreAdapter constructs the adapter. A nil store causes
// every Get to return ErrNoBashOutput (matches the kernel's nilBashOutput).
func NewBashOutputStoreAdapter(store *corebash.Store) *BashOutputStoreAdapter {
	return &BashOutputStoreAdapter{store: store}
}

// Get satisfies agentgraph.BashOutputStore.
func (a *BashOutputStoreAdapter) Get(ctx context.Context, runID string) (coreag.BashOutput, error) {
	if a == nil || a.store == nil {
		return coreag.BashOutput{}, coreag.ErrNoBashOutput
	}
	rec, err := a.store.Get(ctx, runID)
	if err != nil {
		return coreag.BashOutput{}, coreag.ErrNoBashOutput
	}
	return coreag.BashOutput{
		RunID:    rec.RunID,
		Command:  rec.Command,
		Stdout:   rec.Stdout,
		Stderr:   rec.Stderr,
		ExitCode: rec.ExitCode,
	}, nil
}

// Compile-time witness.
var _ coreag.BashOutputStore = (*BashOutputStoreAdapter)(nil)
