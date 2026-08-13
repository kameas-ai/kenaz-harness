package rpc

import (
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// api_agentic_routing_gate_test.go — the launch flag's reader
// (agentgraph-total-convergence-01PMGX01 WP11b; design in
// agentic-turn-routing-01PMAG01 §3.6).
//
// The flag rewrites the graph every chat turn runs, so what it does on
// the UNHAPPY paths matters as much as the happy one. Every one of them
// must resolve to the classic topology.

// faultySettingsStore is a SettingsStore whose LoadAll always fails.
// Embedding the real store means only the one method under test is
// overridden — a hand-written stub would drift as the interface grows.
type faultySettingsStore struct {
	settings.SettingsStore
}

func (faultySettingsStore) LoadAll() (settings.Settings, error) {
	return settings.Settings{}, errors.New("simulated settings read failure")
}

// TestAgenticTurnRoutingEnabled_DegradesToOff is review finding N11.
// api.go promises the flag never degrades to ON; these are the ways it
// could have.
func TestAgenticTurnRoutingEnabled_DegradesToOff(t *testing.T) {
	t.Run("nil settings API", func(t *testing.T) {
		if agenticTurnRoutingEnabledFromSettings(nil) {
			t.Error("a nil settings API resolved the routing flag to ON")
		}
	})

	t.Run("store read failure", func(t *testing.T) {
		base := settings.NewAPI(nil)
		api := settings.NewAPI(faultySettingsStore{SettingsStore: base.Store()})
		if agenticTurnRoutingEnabledFromSettings(api) {
			t.Error("a settings read failure resolved the routing flag to ON — a storage fault must never rewrite the chat graph")
		}
	})

	t.Run("fresh install", func(t *testing.T) {
		// The zero value is the shipped default and it must be off:
		// this is the one deliberate default-off switch in the
		// campaign, and a fresh install must land on the classic
		// topology.
		if agenticTurnRoutingEnabledFromSettings(settings.NewAPI(nil)) {
			t.Error("a fresh install resolved the routing flag to ON")
		}
	})
}

// TestAgenticTurnRoutingEnabled_HonoursThePersistedFlag is the positive
// half: the lever must actually move. A test that only pinned the
// safe direction would pass on a reader hardwired to false.
func TestAgenticTurnRoutingEnabled_HonoursThePersistedFlag(t *testing.T) {
	api := settings.NewAPI(nil)
	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.AgenticTurnRouting = true
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if !agenticTurnRoutingEnabledFromSettings(api) {
		t.Error("the persisted routing flag was not honoured — the lever does not move")
	}
}
