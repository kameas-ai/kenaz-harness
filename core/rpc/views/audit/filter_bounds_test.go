package audit

// Dogfood 2026-10-08 P1: Filter accepted Since/Until/ActorIDs on the
// wire and ignored them on BOTH paths — the store path called
// ByTimeRange with zero bounds and matchesFilterQuery checked only
// Verbose/Kinds/FreeText. These tests pin the honoured semantics on the
// ring path and on a REAL sqlite store (CLAUDE.md blind spot #2: a
// MemoryBackend fixture would skip the SQL emitted_at bounds entirely).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
)

var boundsBase = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

// boundsEntries spans three UTC days: Oct 7 23:00, Oct 8 00:00 (the
// inclusive lower edge), Oct 8 12:00, Oct 8 23:59:59.5 (inside the
// inclusive upper edge), Oct 9 00:00 (just outside it).
func boundsEntries() []Entry {
	ts := []time.Time{
		boundsBase.Add(-time.Hour),
		boundsBase,
		boundsBase.Add(12 * time.Hour),
		boundsBase.Add(24*time.Hour - 500*time.Millisecond),
		boundsBase.Add(24 * time.Hour),
	}
	out := make([]Entry, len(ts))
	for i, t := range ts {
		out[i] = Entry{
			ID:        "b-" + string(rune('a'+i)),
			Timestamp: t.Format(time.RFC3339Nano),
			Category:  "STORAGE",
			Subject:   "fleet.config.applied",
		}
	}
	return out
}

func ids(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The exact bounds the frontend now sends for Since=Until=2026-10-08.
var (
	daySince = boundsBase
	dayUntil = time.Date(2026, 10, 8, 23, 59, 59, 999999999, time.UTC)
)

func boundsCases() []struct {
	name string
	q    eventlog.FilterQuery
	want []string
} {
	return []struct {
		name string
		q    eventlog.FilterQuery
		want []string
	}{
		{"no bounds", eventlog.FilterQuery{}, []string{"b-e", "b-d", "b-c", "b-b", "b-a"}},
		{"since only (inclusive)", eventlog.FilterQuery{Since: daySince}, []string{"b-e", "b-d", "b-c", "b-b"}},
		{"until only (inclusive)", eventlog.FilterQuery{Until: dayUntil}, []string{"b-d", "b-c", "b-b", "b-a"}},
		{"single day", eventlog.FilterQuery{Since: daySince, Until: dayUntil}, []string{"b-d", "b-c", "b-b"}},
		{"future window", eventlog.FilterQuery{Since: boundsBase.Add(48 * time.Hour)}, []string{}},
	}
}

func TestFilter_SinceUntil_Ring(t *testing.T) {
	ctx := context.Background()
	api := NewAPI()
	for _, e := range boundsEntries() {
		api.Push(e)
	}
	for _, c := range boundsCases() {
		got, err := api.Filter(ctx, c.q)
		if err != nil {
			t.Fatalf("%s: Filter: %v", c.name, err)
		}
		if !equalIDs(ids(got), c.want) {
			t.Errorf("%s: ring Filter = %v, want %v", c.name, ids(got), c.want)
		}
	}
}

func TestFilter_SinceUntil_Store(t *testing.T) {
	ctx := context.Background()
	db, store := openStoreAt(t, t.TempDir())
	defer func() { _ = db.Close(ctx) }()
	api := NewAPI(WithStore(store))
	for _, e := range boundsEntries() {
		api.Push(e)
	}
	for _, c := range boundsCases() {
		got, err := api.Filter(ctx, c.q)
		if err != nil {
			t.Fatalf("%s: Filter: %v", c.name, err)
		}
		if !equalIDs(ids(got), c.want) {
			t.Errorf("%s: store Filter = %v, want %v", c.name, ids(got), c.want)
		}
	}
}

// TestFilter_ActorIDs_Store appends rows carrying distinct emitter_ids
// straight to the real sqlite store (Push cannot set one — Entry has no
// emitter field) and asserts ActorIDs selects by emitter_id.
func TestFilter_ActorIDs_Store(t *testing.T) {
	ctx := context.Background()
	db, store := openStoreAt(t, t.TempDir())
	defer func() { _ = db.Close(ctx) }()
	api := NewAPI(WithStore(store))

	for i, emitter := range []string{"actor-a", "actor-b", ""} {
		payload, _ := json.Marshal(auditPersistedPayload{Category: "POLICY", Subject: "policy.decision.denied"})
		row := eventlog.Row{
			EventID:          "act-" + string(rune('a'+i)),
			Kind:             "policy.decision.denied",
			EmitterID:        emitter,
			EmittedAt:        boundsBase.Add(time.Duration(i) * time.Minute),
			Payload:          payload,
			RedactionSummary: "test",
			SchemaVersion:    1,
		}
		if err := store.AppendComputed(ctx, row); err != nil {
			t.Fatalf("AppendComputed: %v", err)
		}
	}

	cases := []struct {
		actors []string
		want   []string
	}{
		{nil, []string{"act-c", "act-b", "act-a"}},
		{[]string{"actor-a"}, []string{"act-a"}},
		{[]string{"actor-a", "actor-b"}, []string{"act-b", "act-a"}},
		{[]string{"nobody"}, []string{}},
	}
	for _, c := range cases {
		got, err := api.Filter(ctx, eventlog.FilterQuery{ActorIDs: c.actors})
		if err != nil {
			t.Fatalf("%v: Filter: %v", c.actors, err)
		}
		if !equalIDs(ids(got), c.want) {
			t.Errorf("ActorIDs=%v: store Filter = %v, want %v", c.actors, ids(got), c.want)
		}
	}
}

// TestFilter_ActorIDs_Ring: ring entries carry no emitter, so a non-empty
// ActorIDs filter must match nothing — never be ignored (which would
// return every entry as if it belonged to the requested actor).
func TestFilter_ActorIDs_Ring(t *testing.T) {
	ctx := context.Background()
	api := NewAPI()
	for _, e := range boundsEntries() {
		api.Push(e)
	}
	got, err := api.Filter(ctx, eventlog.FilterQuery{ActorIDs: []string{"actor-a"}})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ring Filter with ActorIDs = %v, want none (entries carry no emitter)", ids(got))
	}
}
