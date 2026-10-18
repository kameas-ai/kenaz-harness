package rpc

// chat-single-writer-01DOGF0G WP03 — the regression pin for dogfood
// finding F12 ("i keep seeing messages i send in chats get duplicated").
//
// THE BLIND SPOT THIS CLOSES. Every user chat turn was persisted TWICE:
// once by the RPC the frontend calls (Sessions_AppendMessage /
// Sessions_SendMessageWithBlocks — useSession.ts send()/sendBlocks()), and
// again by the chat runner, which read that row's text back in
// LLM.StartStream and re-appended it through the HistoryWriter seam. The
// suite never saw it because frontend tests fake startStream and backend
// chat-runner tests call ChatRunner.StartStream(..., userMessage) directly,
// skipping the frontend append. Each half was tested; the composition —
// append THEN stream — never was. CLAUDE.md blind spot #2, in a new shape:
// the fixture bypasses the OTHER writer.
//
// So this test drives the production chain end to end on REAL sqlite
// (storagesqlite.Open — never session.NewMemoryStore):
//
//	sessions view AppendMessage / SendMessageWithBlocks   (what the frontend calls)
//	  -> llm view API.StartStream                           (LLM_StartStream)
//	  -> chat.ChatRunner kernel run on chat_default.yaml    (real graph, real journal)
//	  -> recordingRegistry (scripted provider)              (the GenerationRequest seam)
//
// and asserts, per parameterisation (text, text+image, image-only):
//
//	(a) exactly ONE user row exists for the turn;
//	(b) that row's id is every move's turn_span_id;
//	(c) the provider received the user's text exactly once;
//	(d) fleet context-sync saw exactly one user-turn event, naming that row;
//	(e) the answer is exactly one `final` assistant row, with no kind-less
//	    assistant twin (WP04 — the assistant-side half of F12).
//
// (d) is spec FR-1d: the runner's re-append was the ONLY path that fed
// SessionSyncer.AppendEvent for user turns (api.go llmHistoryWriter.AppendEntry
// -> sessionHistoryReader.syncHook), so removing the double write without
// re-routing the sync emit would silently stop every user turn reaching fleet
// session sync. The hook here is the production syncHook field on the
// production sessionHistoryReader, recorded instead of shipped.
//
// FALSIFIABILITY: on b8079d48 (v0.85.2) the text and text+image cases fail
// (a) and (c) — two user rows, provider sees the text twice — and the
// image-only case fails (d): the runner took the LatestUserMessageID branch,
// wrote no row, and therefore never announced the turn to sync at all.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// syncEventRecorder stands in for SessionSyncer.AppendEvent at the
// production syncHook seam. Written from the chat runner's goroutines and
// read by the test body, so it carries the mutex + snapshot pattern.
type syncEventRecorder struct {
	mu     sync.Mutex
	events []map[string]string
}

func (r *syncEventRecorder) hook(_ context.Context, _ string, _ uint64, payload []byte) {
	var ev map[string]string
	if err := json.Unmarshal(payload, &ev); err != nil {
		ev = map[string]string{"unparseable": string(payload)}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *syncEventRecorder) snapshot() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]string, len(r.events))
	copy(out, r.events)
	return out
}

// singleWriterHarness is the production-shaped chain this test drives.
type singleWriterHarness struct {
	sessMgr  *session.Manager
	sessAPI  sessions.SessionsAPI
	llmAPI   *llmview.API
	runner   *chat.ChatRunner
	registry *recordingRegistry
	broker   *recordingBroker
	sync     *syncEventRecorder
}

// newSingleWriterHarness mirrors core/rpc/api.go's chat wiring as closely
// as a package-rpc test can: the SAME unexported adapters
// (sessionHistoryReader, chatSessionMessageReader, llmHistoryWriter,
// chatTurnSpanReader), the bundled chat_default graph, and the llm view's
// History bound to the same historyAdapter production binds it to.
func newSingleWriterHarness(t *testing.T) *singleWriterHarness {
	t.Helper()
	sessMgr, attMgr := newSQLTestStores(t)
	return newSingleWriterHarnessOn(t, sessMgr, attMgr)
}

// newSingleWriterHarnessOn builds the same chain over caller-supplied
// managers — the WP-PI variant passes managers over an UPGRADED snapshot.
func newSingleWriterHarnessOn(t *testing.T, sessMgr *session.Manager, attMgr *attachments.Manager) *singleWriterHarness {
	t.Helper()

	graphMgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("graphview.NewManager: %v", err)
	}

	rec := &syncEventRecorder{}
	historyAdapter := &sessionHistoryReader{mgr: sessMgr, syncHook: rec.hook}
	historyReader := chatSessionMessageReader{
		inner:            historyAdapter,
		moveFidelityDial: func() bool { return false },
	}
	historyWriter := &llmHistoryWriter{inner: historyAdapter}

	reg := &recordingRegistry{}
	broker := &recordingBroker{}
	runner, err := chat.New(chat.Config{
		Kernel:        graphMgr.Kernel(),
		Registry:      reg,
		Broker:        broker,
		History:       historyReader,
		HistoryWriter: historyWriter,
		TurnSpan:      chatTurnSpanReader{mgr: sessMgr},
		GraphLoader: func() (coreag.Graph, error) {
			g, gerr := graphMgr.LoadGraphSpec("chat_default")
			if gerr != nil {
				return g, gerr
			}
			return coreag.GateAgenticTurnRouting(g, false), nil
		},
		MaxTurns: func() int { return 5 },
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}

	llmAPI := llmview.New(llmview.Config{
		History:    historyAdapter,
		ChatRunner: runner,
	})

	return &singleWriterHarness{
		sessMgr:  sessMgr,
		sessAPI:  sessions.NewManagerAPIWithAttachments(sessMgr, attMgr),
		llmAPI:   llmAPI,
		runner:   runner,
		registry: reg,
		broker:   broker,
		sync:     rec,
	}
}

func waitForClosedWithin(t *testing.T, b *recordingBroker, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if b.sawClosed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe llm:stream-closed within %s", d)
}

// testPNGBlock is a tiny image block in the shape the frontend sends.
func testPNGBlock() corellm.ContentBlock {
	return corellm.ContentBlock{
		Type:   "image",
		Source: &corellm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "iVBORw0KGgo="},
	}
}

func TestChatTurn_UserMessageStoredOnce_AppendThenStartStream(t *testing.T) {
	const text = "please summarise the release notes"

	cases := []struct {
		name string
		// send performs the frontend's write and returns the expected
		// user text the model should see ("" for an image-only send).
		send func(ctx context.Context, h *singleWriterHarness, sid string) (wantText string)
		// wantImage: the surviving user row must still carry its image.
		wantImage bool
	}{
		{
			name: "text/Sessions_AppendMessage",
			send: func(ctx context.Context, h *singleWriterHarness, sid string) string {
				if _, err := h.sessAPI.AppendMessage(ctx, sid, "user", text); err != nil {
					t.Fatalf("AppendMessage: %v", err)
				}
				return text
			},
		},
		{
			name: "text+image/Sessions_SendMessageWithBlocks",
			send: func(ctx context.Context, h *singleWriterHarness, sid string) string {
				if _, err := h.sessAPI.SendMessageWithBlocks(ctx, sid, []sessions.ContentBlock{
					corellm.ContentBlockFromText(text), testPNGBlock(),
				}); err != nil {
					t.Fatalf("SendMessageWithBlocks: %v", err)
				}
				return text
			},
			wantImage: true,
		},
		{
			name: "image-only/Sessions_SendMessageWithBlocks",
			send: func(ctx context.Context, h *singleWriterHarness, sid string) string {
				if _, err := h.sessAPI.SendMessageWithBlocks(ctx, sid, []sessions.ContentBlock{
					testPNGBlock(),
				}); err != nil {
					t.Fatalf("SendMessageWithBlocks: %v", err)
				}
				return ""
			},
			wantImage: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			h := newSingleWriterHarness(t)
			rec, err := h.sessMgr.Create(ctx, "single-writer probe")
			if err != nil {
				t.Fatalf("Create session: %v", err)
			}
			sid := rec.ID

			wantText := tc.send(ctx, h, sid)
			if _, err := h.llmAPI.StartStream(ctx, "test-profile", sid, ""); err != nil {
				t.Fatalf("LLM StartStream: %v", err)
			}
			waitForClosedWithin(t, h.broker, 10*time.Second)

			stored, err := h.sessMgr.ListMessages(ctx, sid)
			if err != nil {
				t.Fatalf("ListMessages: %v", err)
			}

			// (a) exactly one user row for the turn.
			var users []session.Message
			for _, m := range stored {
				if m.Role == session.RoleUser {
					users = append(users, m)
				}
			}
			if len(users) == 0 {
				t.Fatal("(a) no user row persisted at all")
			}
			if len(users) != 1 {
				var ids []string
				for _, u := range users {
					ids = append(ids, u.ID+"="+u.Content)
				}
				// Errorf, not Fatalf: keep going so (c) and (d) report what
				// the duplicate did downstream too.
				t.Errorf("(a) user rows after one send = %d %v, want exactly 1 — the turn was persisted by more than one writer", len(users), ids)
			}
			// The frontend's row is the one that must survive: it is the
			// first user row, and the only one that can carry attachments.
			userRow := users[0]
			if tc.wantImage {
				hasImage := false
				for _, b := range userRow.ContentBlocks {
					if b.Type == "image" {
						hasImage = true
					}
				}
				if !hasImage {
					t.Errorf("(a) the surviving user row lost its image block: %+v", userRow.ContentBlocks)
				}
			}

			// (b) every move of the turn is anchored on that row.
			spanned := 0
			for _, m := range stored {
				if span := m.TurnSpanID(); span != "" {
					spanned++
					if span != userRow.ID {
						t.Errorf("(b) row %s (%s/%s) carries turn_span_id %s, want the user row %s",
							m.ID, m.Role, m.MoveKind(), span, userRow.ID)
					}
				}
			}
			if spanned == 0 {
				t.Errorf("(b) no row carries a turn_span_id — the turn's moves were not anchored at all")
			}

			// (e) WP04: the turn's answer is ONE assistant row — a move of
			// this turn — with no kind-less twin (the extra assistant bubble
			// half of F12).
			finals := 0
			for _, m := range stored {
				if m.Role != session.RoleAssistant {
					continue
				}
				if m.MoveKind() == "" {
					t.Errorf("(e) kind-less assistant row %s %q in a turn whose span resolved — renders as an extra bubble", m.ID, m.Content)
				}
				if string(m.MoveKind()) == "final" {
					finals++
				}
			}
			if finals != 1 {
				t.Errorf("(e) final assistant rows = %d, want exactly 1", finals)
			}

			// (c) the provider received the user's text exactly once.
			calls := h.registry.snapshot()
			if len(calls) == 0 {
				t.Fatal("(c) the scripted provider was never called")
			}
			if wantText != "" {
				seen := 0
				for _, m := range calls[0].Messages {
					if m.Role == corellm.RoleUser && strings.Contains(m.Text(), wantText) {
						seen++
					}
				}
				if seen != 1 {
					t.Errorf("(c) provider request carried the user text %d times, want exactly 1 (messages: %d)", seen, len(calls[0].Messages))
				}
			}

			// (d) context-sync saw the user turn exactly once, by its row id.
			var userEvents []map[string]string
			for _, ev := range h.sync.snapshot() {
				if ev["role"] == "user" {
					userEvents = append(userEvents, ev)
				}
			}
			if len(userEvents) != 1 {
				t.Fatalf("(d) context-sync user-turn events = %d %v, want exactly 1", len(userEvents), userEvents)
			}
			if userEvents[0]["id"] != userRow.ID {
				t.Errorf("(d) context-sync announced user message %q, want the persisted row %q", userEvents[0]["id"], userRow.ID)
			}
		})
	}
}
