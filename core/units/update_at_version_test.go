package units_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

func TestUpdateAtVersion_ConcurrentWriters(t *testing.T) {
	for _, backend := range []string{"sqlite", "memory"} {
		t.Run(backend, func(t *testing.T) {
			var store units.Store
			if backend == "sqlite" {
				store = units.NewSQLStore(openTestDB(t))
			} else {
				store = units.NewMemoryStore()
			}
			manager := units.NewManager(store)
			ctx := context.Background()
			doc, err := manager.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "session", Classification: units.ClassPersonal, LoadPolicy: units.LoadOnDemand, Title: "shared draft", Body: "original"})
			if err != nil {
				t.Fatal(err)
			}
			const writers = 8
			start := make(chan struct{})
			results := make(chan error, writers)
			var wg sync.WaitGroup
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					_, err := manager.UpdateAtVersion(ctx, doc.ID, doc.Version, fmt.Sprintf("writer %d", i), nil)
					results <- err
				}(i)
			}
			close(start)
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, units.ErrVersionConflict) {
					t.Errorf("unexpected error: %v", err)
				}
			}
			if successes != 1 {
				t.Fatalf("%d writers succeeded; want exactly one", successes)
			}
			current, err := manager.Get(ctx, doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Version != doc.Version+1 {
				t.Fatalf("version=%d", current.Version)
			}
			history, err := manager.ListVersions(ctx, doc.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != 1 {
				t.Fatalf("history=%d; want one committed update", len(history))
			}
			if _, err = manager.UpdateAtVersion(ctx, doc.ID, doc.Version, "stale overwrite", nil); !errors.Is(err, units.ErrVersionConflict) {
				t.Fatalf("stale error=%v", err)
			}
			if _, err = manager.UpdateAtVersion(ctx, doc.ID, -1, "unchecked overwrite", nil); !errors.Is(err, units.ErrVersionConflict) {
				t.Fatalf("negative base error=%v", err)
			}
		})
	}
}
