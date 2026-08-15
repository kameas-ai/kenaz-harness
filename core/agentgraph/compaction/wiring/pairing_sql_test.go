package wiring_test

// pairing_sql_test.go — model-moves-transcript-01PMCH01 WP06, spec §5:
// "a long many-move session compacts without breaking move pairing
// (tool_use/tool_result orphans are the classic provider 400 — pinned)."
//
// WHY THIS FILE IS SQL-DRIVEN, AND WHY THAT IS NOT INCIDENTAL.
//
// Every other test of the compaction boundary in this repository builds
// its fixture with session.NewMemoryStore() or by handing
// compaction.SessionMessage values straight to the snap helpers. WP03's
// review found four SQL-path mutations that survived the entire suite
// for exactly that reason, and the defect THIS file exists to pin is a
// fifth of the same species: the projection at wiring/store.go that
// turns a persisted row into the engine's SessionMessage decided "this
// row opened a tool call" by testing Role == RoleAssistant. WP02 began
// persisting tool_call moves with Role = RoleTool. The helper kept
// compiling, kept returning a string, and returned the empty one — so
// snapBoundaryForToolPairs built an empty openers map and stopped
// clamping anything at all.
//
// A fixture that constructs compaction.SessionMessage directly cannot
// see that: it fills ToolUseID itself. A fixture that writes through the
// real seam and reads through the real adapter can. So the session below
// is written with session.Manager.AppendTranscriptEntry — the single
// move-writing seam — into a real sqlite file, and read back through
// wiring.NewMessageStore, which is the production path exactly.
//
// THE ASSERTION IS THE PROVIDER'S. An orphan is judged by set
// membership over tool ids among the SURVIVING rows, because that is
// what Anthropic and OpenAI reject and what composeModelHistory's sweep
// measures: a tool_use id with no tool_result of that id anywhere in the
// request, or the reverse. Both directions are checked. The suite also
// checks the opposite failure — that compaction did not throw away rows
// it had no business touching — because a clamp written too eagerly
// passes every orphan assertion by compacting nothing, and passes it
// silently.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// ---- fixture ------------------------------------------------------------

// pairingStore opens a real sqlite database and returns the session
// manager plus the raw store the compaction adapter will wrap.
func pairingStore(t *testing.T) (*session.Manager, session.Store) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	store := session.NewSQLStore(session.NewStorageDB(db))
	return session.NewManager(store), store
}

// turnShape describes one human turn's tool activity. ids are the tool
// call ids opened, in order; resultOrder lists the order their results
// are recorded (an out-of-order list is what parallel tool_dispatch
// produces). An id in ids but not in resultOrder never gets a result —
// the interrupted-turn shape.
type turnShape struct {
	prompt      string
	ids         []string
	resultOrder []string
}

// seedMoveSession writes the turns through AppendTranscriptEntry, the
// single move-writing seam, and returns the session id.
func seedMoveSession(t *testing.T, mgr *session.Manager, turns []turnShape) string {
	t.Helper()
	ctx := context.Background()
	rec, err := mgr.Create(ctx, "pairing")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for ti, turn := range turns {
		user, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
			Role: session.RoleUser, Content: turn.prompt,
		})
		if err != nil {
			t.Fatalf("user turn %d: %v", ti, err)
		}
		span := user.ID
		idx := 0
		add := func(e session.TranscriptEntry) {
			e.TurnSpanID = span
			e.MoveIndex = idx
			idx++
			if _, err := mgr.AppendTranscriptEntry(ctx, rec.ID, e); err != nil {
				t.Fatalf("turn %d move %d: %v", ti, e.MoveIndex, err)
			}
		}
		add(session.TranscriptEntry{
			Role: session.RoleAssistant, Kind: session.MoveKindAssistantMove,
			Content: fmt.Sprintf("thinking about %s", turn.prompt),
		})
		for _, id := range turn.ids {
			add(session.TranscriptEntry{
				Role: session.RoleTool, Kind: session.MoveKindToolCall,
				Content:       "bash(cmd=<string>)",
				ToolCalls:     []session.ToolCall{{ID: id, Name: "bash"}},
				ModelToolArgs: map[string]string{id: `{"cmd":"ls"}`},
			})
		}
		for _, id := range turn.resultOrder {
			add(session.TranscriptEntry{
				Role: session.RoleTool, Kind: session.MoveKindToolResult,
				Content:   strings.Repeat("tool output for "+id+" ", 8),
				ToolCalls: []session.ToolCall{{ID: id, Name: "bash", Result: "ok"}},
			})
		}
		add(session.TranscriptEntry{
			Role: session.RoleAssistant, Kind: session.MoveKindFinal,
			Content: fmt.Sprintf("answer for %s", turn.prompt),
		})
	}
	return rec.ID
}

// adversarialTurns is the shape matrix the WP brief named. Each entry
// contributes a documented hazard to one session, so every cut point in
// the sweep below crosses several of them at once.
func adversarialTurns() []turnShape {
	return []turnShape{
		// The canonical sequential pair.
		{prompt: "one", ids: []string{"a1"}, resultOrder: []string{"a1"}},
		// Parallel dispatch resolving out of order — the shape the old
		// contiguity assumption could not group.
		{prompt: "two", ids: []string{"b1", "b2"}, resultOrder: []string{"b2", "b1"}},
		// Nested: both calls open before either answers, in order.
		{prompt: "three", ids: []string{"c1", "c2"}, resultOrder: []string{"c1", "c2"}},
		// An interrupted turn: the call is written before dispatch and no
		// result ever lands.
		{prompt: "four", ids: []string{"d1"}, resultOrder: nil},
		// A provider id reused across turns.
		{prompt: "five", ids: []string{"a1"}, resultOrder: []string{"a1"}},
		{prompt: "six", ids: []string{"a1"}, resultOrder: []string{"a1"}},
		// Three calls, middle one unanswered.
		{prompt: "seven", ids: []string{"e1", "e2", "e3"}, resultOrder: []string{"e3", "e1"}},
		{prompt: "eight", ids: []string{"f1"}, resultOrder: []string{"f1"}},
	}
}

// ---- engine deps --------------------------------------------------------

type fixedCaps struct{ max int }

func (f fixedCaps) MaxContextTokens(compaction.ProviderProfileRef) (int, bool) {
	return f.max, true
}

type echoSummarizer struct{}

func (echoSummarizer) CallForSummary(context.Context, compaction.ProviderProfileRef,
	string, string) (string, int, int, error) {
	return "everything before this point, summarized", 100, 10, nil
}

// runeTokenizer counts one token per four content bytes — deterministic,
// so a percentage span lands on a predictable boundary index.
type runeTokenizer struct{}

func (runeTokenizer) Count(sys string, msgs []compaction.TokenizerMessage) int {
	n := len(sys)
	for _, m := range msgs {
		n += len(m.Content) + len(m.Role)
	}
	return (n + 3) / 4
}

func pairingEngine(t *testing.T, store session.Store, recentWindow int) compaction.SessionEngine {
	t.Helper()
	eng, err := compaction.NewSessionEngine(compaction.SessionEngineConfig{
		Store:        wiring.NewMessageStore(store),
		LLM:          echoSummarizer{},
		Capabilities: fixedCaps{max: 1 << 20},
		Tokenizer:    runeTokenizer{},
		RecentWindow: func() int { return recentWindow },
		Now:          func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewSessionEngine: %v", err)
	}
	return eng
}

// ---- assertions ---------------------------------------------------------

// orphanIDs applies the provider's own rule — set membership over tool
// ids — and returns every id present on exactly one side.
func orphanIDs(rows []session.Message) map[string]bool {
	calls, results := map[string]bool{}, map[string]bool{}
	for _, m := range rows {
		if len(m.ToolCalls) == 0 {
			continue
		}
		switch m.MoveKind() {
		case session.MoveKindToolCall:
			calls[m.ToolCalls[0].ID] = true
		case session.MoveKindToolResult:
			results[m.ToolCalls[0].ID] = true
		}
	}
	out := map[string]bool{}
	for id := range calls {
		if !results[id] {
			out[id] = true
		}
	}
	for id := range results {
		if !calls[id] {
			out[id] = true
		}
	}
	return out
}

// assertCreatedNoOrphan is the relative form of the invariant, and the
// relative form is the correct one.
//
// The fixture deliberately contains calls that never got a result — an
// interrupted turn writes its tool_call before dispatch precisely so the
// call is on the record, and WP02's synthetic-result path may or may not
// have run. Those are orphans in the ORIGINAL transcript; the wire-side
// sweep in composeModelHistory removes them and they are not compaction's
// doing. What compaction must never do is MANUFACTURE one by cutting
// through an intact pair.
//
// Asserting "no orphans survive" instead would fail on the fixture
// before compaction runs, which is how a test gets weakened into
// asserting nothing.
func assertCreatedNoOrphan(t *testing.T, label string, before, after []session.Message) {
	t.Helper()
	was := orphanIDs(before)
	for id := range orphanIDs(after) {
		if !was[id] {
			t.Errorf("[%s] compaction CREATED an orphan for tool id %q — the span "+
				"boundary cut through an intact tool_use/tool_result pair. This is the "+
				"provider 400 spec §5 pins.", label, id)
		}
	}
}

// TestSessionCompaction_NeverOrphansAPair_AtEveryCutPoint is the
// summarize half of the WP's cut-point matrix. It drives engine.Compact
// across the whole legal range of oldestPct against a session built from
// every adversarial pair shape, and asserts at each cut that the
// surviving transcript contains no half-pair.
//
// MUTATION EVIDENCE (each reverts a WP06 change; each kills this test):
//
//  1. wiring/store.go toolUseID → `if m.Role == session.RoleAssistant`:
//     every move-borne tool_call reports "", the openers map is empty,
//     and the boundary splits pairs from oldestPct 0.25 upward.
//  2. session_snap.go straddler → the old two-index test (messages[idx]
//     and messages[idx-1] only): turn "two"'s out-of-order pair b1
//     straddles with an intervening row and is missed.
//  3. session_engine.go: delete the step-4b backward clamp: the
//     recent-window clamp re-splits a pair the forward clamp had fixed.
func TestSessionCompaction_NeverOrphansAPair_AtEveryCutPoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	compactedAtLeastOnce := false
	for pctTenths := 1; pctTenths <= 10; pctTenths++ {
		for _, rw := range []int{0, 1, 2, 4} {
			pct := float64(pctTenths) / 10
			label := fmt.Sprintf("pct=%.1f recentWindow=%d", pct, rw)

			// A fresh database per cut point: Compact mutates the session.
			mgr, store := pairingStore(t)
			sid := seedMoveSession(t, mgr, adversarialTurns())
			before, err := store.ListMessagesActive(ctx, sid)
			if err != nil {
				t.Fatalf("[%s] pre-list: %v", label, err)
			}
			if _, err := pairingEngine(t, store, rw).Compact(ctx, sid,
				compaction.ProviderProfileRef{ProviderID: "p", ModelID: "m"}, pct); err != nil {
				t.Fatalf("[%s] Compact: %v", label, err)
			}

			after, err := store.ListMessagesActive(ctx, sid)
			if err != nil {
				t.Fatalf("[%s] post-list: %v", label, err)
			}
			assertCreatedNoOrphan(t, label, before, after)

			if len(after) < len(before) {
				compactedAtLeastOnce = true
			}

			// The surviving rows must be a SUFFIX of the originals (plus
			// the inserted summary). Compaction archives a prefix span; a
			// survivor from the middle with an archived row before it would
			// mean rows were removed by something other than the span, which
			// is how a clamp "passes" the orphan check by deleting evidence.
			assertSuffixOfOriginal(t, label, before, after)
		}
	}
	if !compactedAtLeastOnce {
		t.Fatal("no cut point archived a single row — the whole sweep is vacuous")
	}
}

// assertSuffixOfOriginal checks that every non-summary survivor appears
// in the original order with no gaps once the first survivor is found.
func assertSuffixOfOriginal(t *testing.T, label string, before, after []session.Message) {
	t.Helper()
	origIdx := map[string]int{}
	for i, m := range before {
		origIdx[m.ID] = i
	}
	last := -1
	started := false
	for _, m := range after {
		i, ok := origIdx[m.ID]
		if !ok {
			continue // the inserted summary row
		}
		if !started {
			started = true
			last = i
			continue
		}
		if i != last+1 {
			t.Errorf("[%s] survivor %q sits at original index %d but the previous "+
				"survivor was %d — compaction removed a row from the MIDDLE of the "+
				"kept span, which is history loss dressed as pair safety",
				label, m.ID, i, last)
		}
		last = i
	}
}

// TestSessionCompaction_RollingNeverOrphansAPair is the same sweep for
// the maximal tier's rolling path, which computes its own boundary
// (findRecentWindowStart) and then applies the same forward clamp.
func TestSessionCompaction_RollingNeverOrphansAPair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	rolledAtLeastOnce := false
	for _, rw := range []int{1, 2, 3, 4, 5, 6} {
		label := fmt.Sprintf("rolling recentWindow=%d", rw)
		mgr, store := pairingStore(t)
		sid := seedMoveSession(t, mgr, adversarialTurns())
		before, err := store.ListMessagesActive(ctx, sid)
		if err != nil {
			t.Fatalf("[%s] pre-list: %v", label, err)
		}

		if _, err := pairingEngine(t, store, rw).RollingSummarize(ctx, sid,
			compaction.ProviderProfileRef{ProviderID: "p", ModelID: "m"}, rw); err != nil {
			t.Fatalf("[%s] RollingSummarize: %v", label, err)
		}

		after, err := store.ListMessagesActive(ctx, sid)
		if err != nil {
			t.Fatalf("[%s] post-list: %v", label, err)
		}
		assertCreatedNoOrphan(t, label, before, after)
		assertSuffixOfOriginal(t, label, before, after)
		if len(after) < len(before) {
			rolledAtLeastOnce = true
		}
	}
	if !rolledAtLeastOnce {
		t.Fatal("no rolling window archived a single row — the sweep is vacuous")
	}
}

// TestMessageStore_MoveRowsReportTheirPairMarkers is the unit-level pin
// under the sweep above: the projection that WP02 silently invalidated.
//
// It asserts the three things the snap helpers depend on and the old
// role-only implementation got wrong: a tool_call move reports an
// opener id, a tool_result move reports a closer id, and NEITHER row
// reports both (a row that claims to be its own pair makes the openers
// map say a call answers itself).
//
// Mutation evidence: restore either helper's `m.Role == RoleAssistant` /
// `m.Role == RoleTool` test and the corresponding sub-assertion fails.
func TestMessageStore_MoveRowsReportTheirPairMarkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, store := pairingStore(t)
	sid := seedMoveSession(t, mgr, []turnShape{
		{prompt: "one", ids: []string{"x1"}, resultOrder: []string{"x1"}},
	})

	got, err := wiring.NewMessageStore(store).ListActiveMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListActiveMessages: %v", err)
	}

	var opens, closes int
	for _, m := range got {
		if m.ToolUseID != "" && m.ToolResultForID != "" {
			t.Errorf("row %q reports BOTH an opener (%q) and a closer (%q) — "+
				"snapBoundaryForToolPairs would treat it as a pair that answers itself",
				m.ID, m.ToolUseID, m.ToolResultForID)
		}
		if m.ToolUseID == "x1" {
			opens++
		}
		if m.ToolResultForID == "x1" {
			closes++
		}
	}
	if opens != 1 {
		t.Errorf("tool_call moves reporting ToolUseID=x1: %d, want 1. A tool_call move "+
			"persists with Role=RoleTool, so a role-only test reports nothing and the "+
			"boundary clamp silently stops clamping.", opens)
	}
	if closes != 1 {
		t.Errorf("tool_result moves reporting ToolResultForID=x1: %d, want 1", closes)
	}
}

// TestMessageStore_ClassicToolRowsStillReportMarkers pins that the
// move-aware rewrite did not drop the pre-mission vocabulary. Every row
// written before migration 0333 has a NULL kind, and its tool_use lives
// on an assistant row exactly as it always did.
func TestMessageStore_ClassicToolRowsStillReportMarkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, store := pairingStore(t)
	rec, err := mgr.Create(ctx, "classic")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role: session.RoleAssistant, Content: "calling",
		ToolCalls: []session.ToolCall{{ID: "k1", Name: "bash"}},
	}); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role: session.RoleTool, Content: "out",
		ToolCalls: []session.ToolCall{{ID: "k1", Name: "bash"}},
	}); err != nil {
		t.Fatalf("tool: %v", err)
	}

	got, err := wiring.NewMessageStore(store).ListActiveMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListActiveMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0].ToolUseID != "k1" || got[0].ToolResultForID != "" {
		t.Errorf("classic assistant row: use=%q result=%q, want use=k1 result=\"\"",
			got[0].ToolUseID, got[0].ToolResultForID)
	}
	if got[1].ToolResultForID != "k1" || got[1].ToolUseID != "" {
		t.Errorf("classic tool row: use=%q result=%q, want use=\"\" result=k1",
			got[1].ToolUseID, got[1].ToolResultForID)
	}
}
