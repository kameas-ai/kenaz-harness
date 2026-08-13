// Package knobcoverage generalises the knob-coverage guard mission
// autonomy-knobs-live-01PMAG02's own WP07 builds for
// autonomy.ResolvedKnobs specifically, to any resolved-config struct
// (wiring-integrity-01PMAG04 WP07, spec §3.2 item 2 / plan.md WP07).
//
// # Why this exists
//
// autonomy.ResolvedKnobs is the second time this codebase has shipped a
// fully-plumbed dial system — resolution chain, source-trace, Settings
// UI, tests — with no runtime consumer for most of its fields (spec
// §1.1: "autonomy.ResolvedKnobs fields with a runtime consumer: 1 of
// 7", later corrected to a lower true count once the confirm-each gap
// was characterised). A resolved-config struct is exactly the shape
// that goes inert silently: every individual PR that adds a field
// compiles, the resolution logic has its own tests, and nothing forces
// anyone to also grep the whole tree for a consumer before merging.
// This package is the mechanism that makes "I wired this knob" a
// registration a reviewer can see and CI can enforce, instead of a
// claim in a PR description.
//
// # Contract between this package and its callers
//
// This package owns the GENERAL mechanism: registering a
// (struct type, field name) pair as covered (with a description of
// where it's consumed) or explicitly deferred (with a reason), and
// reflecting over a struct type's exported fields to report any that
// are neither.
//
// It does NOT know which fields of which structs exist, and it does
// NOT decide which fields are covered — that is domain knowledge only
// the struct's owning mission has. A mission wiring a dial system
// calls Register/RegisterDeferred (typically as package-level
// declarations next to the actual consumer code, so the registration
// travels with the code it describes) and writes ONE test asserting
// Uncovered[TheirStructType]() is empty. Nothing here special-cases
// autonomy.ResolvedKnobs; the mission-specific registration work
// (which knob maps to which consumer, or which is legitimately
// deferred) belongs in that mission's own commit, in whatever package
// the consumer code lives in — this package has no opinion on that and
// deliberately does not import core/autonomy.
//
// # CI wiring
//
// scripts/ci/check-knob-coverage.sh runs `go test ./core/... -run
// 'TestKnobCoverage'` — any test whose name starts with
// "TestKnobCoverage" across the whole module is picked up
// automatically, with no guard-script change required when a new
// mission adds one. Until a struct is registered (via Register or
// RegisterDeferred) it is simply not tracked, and Uncovered on an
// untracked type panics loudly (see Uncovered's doc) rather than
// silently reporting "no issues" — a coverage check that can pass by
// checking nothing would be the "gate that cannot fail" shape #281
// exists to prevent (see CLAUDE.md's "Race-safe test fakes" section's
// sibling concern about gates needing to be provably able to fail).
package knobcoverage

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// entry is one field's coverage record.
type entry struct {
	covered     bool
	description string // non-empty when covered
	deferred    bool
	deferReason string // non-empty when deferred
}

var (
	mu       sync.Mutex
	registry = map[reflect.Type]map[string]entry{}
)

// Register records that structType's named field has a real runtime
// consumer. description should point at where — a file, a function, a
// mission id — so a future auditor doesn't have to re-derive it.
//
// Panics (at registration time, which in practice means package
// init/var-decl time — a programmer error surfaces immediately, not as
// a flaky CI failure) when:
//   - T is not a struct type,
//   - field does not name an exported field on T,
//   - field was already registered for T (covered or deferred) — a
//     duplicate registration is almost always a copy-paste mistake
//     that silently shadows the real one.
func Register[T any](field, description string) {
	if description == "" {
		panic(fmt.Sprintf("knobcoverage: Register(%s): description must be non-empty", field))
	}
	register[T](field, entry{covered: true, description: description})
}

// RegisterDeferred records that structType's named field is
// deliberately not yet wired, mirroring the //wiring:deferred(<reason>)
// directive convention (docs/wiring-audit.md) for fields rather than
// Go declarations. reason must be non-empty for the same audit-value
// reason a //wiring:deferred comment requires one.
func RegisterDeferred[T any](field, reason string) {
	if reason == "" {
		panic(fmt.Sprintf("knobcoverage: RegisterDeferred(%s): reason must be non-empty", field))
	}
	register[T](field, entry{deferred: true, deferReason: reason})
}

func register[T any](field string, e entry) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("knobcoverage: Register/RegisterDeferred: %s is not a struct type", t))
	}
	if _, ok := t.FieldByName(field); !ok {
		panic(fmt.Sprintf("knobcoverage: %s has no exported field %q", t, field))
	}

	mu.Lock()
	defer mu.Unlock()
	if registry[t] == nil {
		registry[t] = map[string]entry{}
	}
	if prior, ok := registry[t][field]; ok {
		panic(fmt.Sprintf("knobcoverage: %s.%s registered twice (first: covered=%v deferred=%v)",
			t, field, prior.covered, prior.deferred))
	}
	registry[t][field] = e
}

// Uncovered returns the exported field names of T that have neither a
// Register nor a RegisterDeferred entry, in struct-declaration order.
// An empty result means every field is either covered or explicitly
// deferred — the assertion a mission's own TestKnobCoverage_* test
// should make.
//
// Panics if T has never had Register or RegisterDeferred called for
// ANY of its fields — an untracked type has no coverage claim to make,
// and a coverage check that silently reports "nothing uncovered" for a
// type nobody registered would be indistinguishable from a real pass.
// Call Register/RegisterDeferred for at least one field (even if it's
// the only field) before calling Uncovered.
func Uncovered[T any]() []string {
	t := reflect.TypeFor[T]()
	mu.Lock()
	fields, tracked := registry[t]
	// Copy while holding the lock so the caller's iteration below sees a
	// stable snapshot.
	snapshot := make(map[string]entry, len(fields))
	for k, v := range fields {
		snapshot[k] = v
	}
	mu.Unlock()

	if !tracked {
		panic(fmt.Sprintf("knobcoverage: %s is not tracked — call Register or RegisterDeferred for at least one field before checking Uncovered", t))
	}

	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if _, ok := snapshot[f.Name]; !ok {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Report describes one field's recorded status. Used by
// scripts/ci/check-knob-coverage.sh's --report mode (via a test-level
// print, since this package has no CLI of its own) and by any mission
// wanting to print its own coverage table.
type Report struct {
	Field       string
	Covered     bool
	Description string
	Deferred    bool
	DeferReason string
}

// Reports returns every field's recorded status for T, sorted by field
// name, including fields that are neither covered nor deferred (the
// same set Uncovered returns, just with full detail). Panics under the
// same "untracked type" condition as Uncovered.
func Reports[T any]() []Report {
	t := reflect.TypeFor[T]()
	mu.Lock()
	fields, tracked := registry[t]
	snapshot := make(map[string]entry, len(fields))
	for k, v := range fields {
		snapshot[k] = v
	}
	mu.Unlock()
	if !tracked {
		panic(fmt.Sprintf("knobcoverage: %s is not tracked — call Register or RegisterDeferred for at least one field before checking Reports", t))
	}

	var out []Report
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		e, ok := snapshot[f.Name]
		out = append(out, Report{
			Field:       f.Name,
			Covered:     ok && e.covered,
			Description: e.description,
			Deferred:    ok && e.deferred,
			DeferReason: e.deferReason,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}
