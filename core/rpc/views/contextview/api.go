// Package contextview defines the ContextAPI view-scoped accessor.
//
// The package is named `contextview` (not `context`) because Go's stdlib
// `context` package would shadow this import in callers. Plan §2.2 lists
// the accessor as `Context` on HarnessAPI; the package alias is the
// implementation detail.
package contextview

import "context"

// ContextEntry is reference-only metadata about a shared-context entry.
type ContextEntry struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// ContextAPI is the view-scoped accessor for shared-context distribution.
type ContextAPI interface {
	List(ctx context.Context) ([]ContextEntry, error)
	StartStream(ctx context.Context) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
