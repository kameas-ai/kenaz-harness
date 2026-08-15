package rpc

// model_history_binding_test.go — the BINDING half of
// model-moves-transcript-01PMCH01 WP03.
//
// model_history_test.go pins composeModelHistory: given rows and a
// fidelity, what messages come out. That is the projection, and it is
// well covered. This file pins the layer ABOVE it — the code that
// decides WHICH fidelity the projection is called with, and reads the
// rows it is called on:
//
//	moveFidelityHistoryEnabledFromSettings   the live dial's reader
//	chatSessionMessageReader.moveFidelityHistoryEnabled
//	chatSessionMessageReader.sessionMoveMode the durable half's reader
//	chatSessionMessageReader.History         the two composed, over SQL
//
// It exists because a review mutation sweep found that layer entirely
// unpinned: five separate mutations — nil dial fails OPEN, settings read
// failure fails OPEN, session lookup failure fails OPEN, an unknown move
// kind fails OPEN, and the whole sessions.move_history_mode column
// round trip — all SURVIVED the WP03 suite. Every one of them either
// disables the feature silently or opts a user into a provider-visible
// request shape they did not choose, and neither shows up as a test
// failure. The composition tests could not catch any of them: they call
// composeModelHistory directly with a fidelity already resolved, and
// they build their fixtures on the memory store, which never executes
// the SQL that carries the durable half.

import (
	"context"
	"errors"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// faultyMoveSettingsStore is a SettingsStore whose LoadAll always fails.
// Embedding the real store means only the method under test is
// overridden, so this stub cannot drift as the interface grows.
type faultyMoveSettingsStore struct {
	settings.SettingsStore
}

func (faultyMoveSettingsStore) LoadAll() (settings.Settings, error) {
	return settings.Settings{}, errors.New("simulated settings read failure")
}

// ---------------------------------------------------------------------------
// The live half's reader.
// ---------------------------------------------------------------------------

// TestMoveFidelityHistoryEnabledFromSettings pins the asymmetry the
// api.go comment claims and nothing verified: the dial DEFAULTS to on,
// but every UNHEALTHY read resolves off.
//
// That asymmetry is the whole point. Defaulting a storage fault to the
// provider-visible composition would change every subsequent request's
// message array because a file was briefly locked.
//
// Mutations killed:
//   - return true instead of false on the LoadAll error path;
//   - return true instead of false for a nil settings API;
//   - hardwire the reader to false (the "safe direction only" test smell —
//     the fresh-install and persisted-off subtests are what stop that).
func TestMoveFidelityHistoryEnabledFromSettings(t *testing.T) {
	t.Run("nil settings API fails closed", func(t *testing.T) {
		if moveFidelityHistoryEnabledFromSettings(nil) {
			t.Error("a nil settings API resolved the move-fidelity dial to ON — " +
				"a chassis with no settings surface must never opt sessions into " +
				"the provider-visible composition")
		}
	})

	t.Run("store read failure fails closed", func(t *testing.T) {
		base := settings.NewAPI(nil)
		api := settings.NewAPI(faultyMoveSettingsStore{SettingsStore: base.Store()})
		if moveFidelityHistoryEnabledFromSettings(api) {
			t.Error("a settings read failure resolved the move-fidelity dial to ON — " +
				"a storage fault must not reshape every subsequent request")
		}
	})

	t.Run("fresh install defaults ON", func(t *testing.T) {
		// spec §4's default. This subtest is what stops the two above
		// from being satisfiable by a reader hardwired to false.
		if !moveFidelityHistoryEnabledFromSettings(settings.NewAPI(nil)) {
			t.Error("a fresh install resolved the move-fidelity dial to OFF — " +
				"the spec default is ON for new sessions")
		}
	})

	t.Run("the persisted off-bit is honoured", func(t *testing.T) {
		api := settings.NewAPI(nil)
		s, err := api.Store().LoadAll()
		if err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		s.MoveFidelityHistoryDisabled = true
		if err := api.Store().SaveAll(s); err != nil {
			t.Fatalf("SaveAll: %v", err)
		}
		if moveFidelityHistoryEnabledFromSettings(api) {
			t.Error("the persisted disable bit was ignored — the revert lever does not move")
		}
	})
}

// TestMoveFidelityHistoryEnabledFromSettings_ReReadsTheStore pins
// READ-AT-CONSUMPTION at the level a unit test can reach: the reader
// must consult the store on EVERY call, so that flipping the dial
// reverts the next request rather than requiring a restart.
//
// Mutation: memoize the LoadAll result inside the reader (a sync.Once,
// a package-level cache) and this fails on the second call.
//
// RESIDUAL GAP, stated plainly: this pins the reader. It cannot reach
// buildChatRunner's line
//
//	moveFidelityHistory := func() bool { return moveFidelityHistoryEnabledFromSettings(settingsImpl) }
//
// where hoisting the call out of the closure would latch the dial at
// chassis-construction time. buildChatRunner takes nineteen collaborators
// and returns nil without a live kernel, so no unit test constructs it.
// The closure is one line and carries a comment saying why it is a
// closure; this test is what makes the function it wraps trustworthy.
func TestMoveFidelityHistoryEnabledFromSettings_ReReadsTheStore(t *testing.T) {
	api := settings.NewAPI(nil)
	if !moveFidelityHistoryEnabledFromSettings(api) {
		t.Fatal("precondition: a fresh install should read ON")
	}

	s, err := api.Store().LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.MoveFidelityHistoryDisabled = true
	if err := api.Store().SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	if moveFidelityHistoryEnabledFromSettings(api) {
		t.Fatal("the dial was latched: a second read after the setting changed still " +
			"reported ON, so turning the lever off would not take effect until relaunch")
	}
}

// ---------------------------------------------------------------------------
// The reader's two fail-closed helpers.
// ---------------------------------------------------------------------------

// TestChatSessionMessageReader_FailsClosed pins the two unhappy paths
// inside the reader itself. Both resolve to today's composition.
//
// Mutations killed:
//   - moveFidelityHistoryEnabled returns true for a nil dial;
//   - sessionMoveMode returns MoveHistoryModeMoves when Get fails.
func TestChatSessionMessageReader_FailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("a nil dial is OFF", func(t *testing.T) {
		t.Parallel()
		var r chatSessionMessageReader
		if r.moveFidelityHistoryEnabled() {
			t.Error("an unwired dial reported ON — a chassis built without settings " +
				"must not opt sessions into the provider-visible composition")
		}
	})

	t.Run("the dial is otherwise honoured in both directions", func(t *testing.T) {
		t.Parallel()
		on := chatSessionMessageReader{moveFidelityDial: func() bool { return true }}
		off := chatSessionMessageReader{moveFidelityDial: func() bool { return false }}
		if !on.moveFidelityHistoryEnabled() || off.moveFidelityHistoryEnabled() {
			t.Error("moveFidelityHistoryEnabled does not track its dial")
		}
	})

	t.Run("an unresolvable session is classic", func(t *testing.T) {
		t.Parallel()
		mgr, _ := newBindingSQLManager(t)
		r := chatSessionMessageReader{
			inner:            &sessionHistoryReader{mgr: mgr},
			moveFidelityDial: func() bool { return true },
		}
		if mode := r.sessionMoveMode(context.Background(), "no-such-session"); mode != "" {
			t.Errorf("sessionMoveMode for an unknown session = %q, want \"\" — a lookup "+
				"failure must read as the pre-migration default, not as an opt-in", mode)
		}
	})
}

// ---------------------------------------------------------------------------
// The whole binding, over real SQL.
// ---------------------------------------------------------------------------

// TestChatSessionMessageReader_HistoryOverSQL is the end-to-end pin: a
// move-bearing session persisted through the real SQLite store and read
// back through the production reader, at both dial positions.
//
// IT MUST BE SQL. sessions.move_history_mode — the DURABLE half of the
// gate — is written by sqlStore.Create's INSERT and read by scanRecord.
// The memory store executes neither. Mutations that dropped the INSERT
// bind, or dropped the scan decode, left every session reading back ""
// — which resolves classic, silently disabling the feature for every
// real user — and the entire WP03 suite still passed, because every
// fixture in it is memory-backed.
//
// Mutations killed:
//   - drop r.MoveHistoryMode from sqlStore.Create's bind list;
//   - drop the moveHistoryModeCol decode from scanRecord;
//   - drop move_history_mode from sqlSelectSession;
//   - any of the projection mutations model_history_test.go already
//     covers, now also proven through the persistence path.
func TestChatSessionMessageReader_HistoryOverSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		dial     bool
		wantMsgs int
		wantPair bool
	}{
		{"dial on: the move chain reaches the model", true, 5, true},
		{"dial off: the classic single-message composition", false, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr, _ := newBindingSQLManager(t)
			dial := func() bool { return tc.dial }
			mgr.SetMoveFidelityDial(dial)

			rec, err := mgr.Create(ctx, "binding")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			sid := rec.ID

			// The durable half must survive the round trip through SQL,
			// not merely the in-memory Record the Create call returned.
			reloaded, err := mgr.Get(ctx, sid)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			wantMode := string(session.MoveHistoryModeClassic)
			if tc.dial {
				wantMode = string(session.MoveHistoryModeMoves)
			}
			if reloaded.MoveHistoryMode != wantMode {
				t.Fatalf("sessions.move_history_mode reloaded as %q, want %q — the "+
					"durable half of the gate did not survive persistence",
					reloaded.MoveHistoryMode, wantMode)
			}

			seedMoveTurnSQL(t, mgr, sid)

			r := chatSessionMessageReader{
				inner:            &sessionHistoryReader{mgr: mgr},
				moveFidelityDial: dial,
			}
			got, err := r.History(ctx, sid, 0)
			if err != nil {
				t.Fatalf("History: %v", err)
			}
			if len(got) != tc.wantMsgs {
				t.Fatalf("History returned %d messages, want %d: %#v", len(got), tc.wantMsgs, got)
			}
			assertNoOrphans(t, got, "dial=%v over SQL", tc.dial)

			var sawCall, sawResult bool
			for _, m := range got {
				for _, tcall := range m.ToolCalls {
					if tcall.ID == "toolu_01" && tcall.Arguments == `{"path":"foo.txt"}` {
						sawCall = true
					}
				}
				if m.Role == "tool" && m.ToolCallID == "toolu_01" {
					sawResult = true
				}
			}
			if sawCall != tc.wantPair || sawResult != tc.wantPair {
				t.Errorf("tool pair present = (call %v, result %v), want %v for both — "+
					"the model-layer arguments did not survive the SQL round trip into "+
					"the composition", sawCall, sawResult, tc.wantPair)
			}
		})
	}
}

// TestModelHistory_UnknownMoveKindFailsClosed pins projectRow's default
// branch, which is the forward-compatibility contract: a row written by
// a NEWER build, carrying a move kind this one does not understand, must
// not be reshaped into a request.
//
// Mutation: make the default branch `return textMessage(r), true` — the
// unknown row then reaches the model as assistant prose, at BOTH gate
// positions, which also silently breaks the gate-off byte-identity
// guarantee for any session a newer build has touched.
func TestModelHistory_UnknownMoveKindFailsClosed(t *testing.T) {
	t.Parallel()
	rows := []modelHistoryRow{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "a kind from the future", MoveKind: "reasoning_move"},
		{Role: "assistant", Content: "done", MoveKind: string(session.MoveKindFinal)},
	}
	for _, fidelity := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
		got := composeModelHistory(rows, fidelity, 0)
		if len(got) != 2 {
			t.Fatalf("fidelity %v: got %d messages, want 2 — an unrecognised move kind "+
				"was projected into the request: %#v", fidelity, len(got), got)
		}
		for _, m := range got {
			if m.Content == "a kind from the future" {
				t.Fatalf("fidelity %v: an unrecognised move kind reached the model", fidelity)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newBindingSQLManager(t *testing.T) (*session.Manager, storage.DB) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return session.NewManager(session.NewSQLStore(session.NewStorageDB(db))), db
}

// seedMoveTurnSQL writes the canonical three-move turn through the
// production seam (llmHistoryWriter → AppendTranscriptEntry), so every
// row lands in SQLite exactly as WP02's runner would have written it.
func seedMoveTurnSQL(t *testing.T, mgr *session.Manager, sid string) {
	t.Helper()
	ctx := context.Background()
	w := &llmHistoryWriter{inner: &sessionHistoryReader{mgr: mgr}}

	span, err := w.AppendEntry(ctx, sid, coreag.HistoryEntry{
		Role: "user", Content: "read foo.txt and tell me what it says",
	})
	if err != nil {
		t.Fatalf("user turn: %v", err)
	}
	for _, e := range []coreag.HistoryEntry{
		{Role: "assistant", Content: "Let me read the file.",
			MoveKind: string(session.MoveKindAssistantMove), MoveIndex: 0, TurnSpanID: span},
		{Role: "tool", Content: "kenaz__read_file(path=<string>)",
			MoveKind: string(session.MoveKindToolCall), MoveIndex: 1, TurnSpanID: span,
			ToolCalls:     []coreag.ToolCallRequest{{ID: "toolu_01", Name: "kenaz__read_file"}},
			ModelToolArgs: map[string]string{"toolu_01": `{"path":"foo.txt"}`}},
		{Role: "tool", Content: "hello world",
			MoveKind: string(session.MoveKindToolResult), MoveIndex: 2, TurnSpanID: span,
			ToolCalls: []coreag.ToolCallRequest{{ID: "toolu_01", Name: "kenaz__read_file"}}},
		{Role: "assistant", Content: "The file says hello world.",
			MoveKind: string(session.MoveKindFinal), MoveIndex: 3, TurnSpanID: span},
	} {
		if _, err := w.AppendEntry(ctx, sid, e); err != nil {
			t.Fatalf("AppendEntry(%s): %v", e.MoveKind, err)
		}
	}
}

// TestModelHistory_ByteIdentityHoldsAtEveryWindow widens the gate-OFF
// byte-identity claim across the truncation windows.
//
// TestModelHistory_GateOff_MatchesPreMissionOracle pins the claim at
// n=0 only. n=0 is what the shipped chat graph's history_read passes,
// but it is NOT the only production caller: agentgraph's
// exec_control.go calls History with a hardcoded n=10, so the window is
// live and the golden did not cover it.
//
// The window matters here because composeModelHistory deliberately
// INVERTS the pre-mission order — it projects then truncates, where the
// old code truncated then projected. For classic rows the projection is
// 1:1 so the two orders coincide, which is what makes byte identity
// survive; this test is what proves that rather than assuming it. (The
// inversion is required: truncating raw rows first would spend the
// window on move rows that classic fidelity then drops, and would put
// the pair-integrity sweep before the cut it exists to clean up after.)
//
// Mutation: swap composeModelHistory back to truncate-then-project and
// this still passes (classic rows are 1:1) while
// TestModelHistory_TruncationNeverOrphansAPair fails — the two tests
// cover the two halves and neither is redundant.
func TestModelHistory_ByteIdentityHoldsAtEveryWindow(t *testing.T) {
	t.Parallel()
	rows := make([]session.Message, 0, 14)
	for i := range 14 {
		role := session.RoleUser
		if i%2 == 1 {
			role = session.RoleAssistant
		}
		rows = append(rows, session.Message{Role: role, Content: string(rune('a' + i))})
	}
	for n := range 17 {
		want := mustJSON(t, preMissionHistoryProjection(rows, n))
		for _, fidelity := range []moveFidelity{moveFidelityClassic, moveFidelityMoves} {
			got := mustJSON(t, composeModelHistory(modelHistoryRowsFrom(rows), fidelity, n))
			if got != want {
				t.Fatalf("n=%d fidelity=%v diverges from the pre-mission oracle.\n got: %s\nwant: %s",
					n, fidelity, got, want)
			}
		}
	}
}
