package fleet

// handoff_send_test.go — device-keys-handoff-01DEVKH01 WP04 (FR-4, AC-3,
// AC-4, AC-5) against the in-memory fake of fleet's handoff routes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

func sendRig(t *testing.T) (*fakeV2Fleet, *HandoffHandler, *fakeEmitter) {
	t.Helper()
	f := newFakeV2Fleet(t)
	withExternalToken(t, "tok-send")
	c := makeTestClient(t, f.srv.URL)
	c.dataDir = t.TempDir()
	em := &fakeEmitter{}
	return f, NewHandoffHandler(c, em, nil), em
}

func plainEvents(n int) []SessionEventRecord {
	out := make([]SessionEventRecord, n)
	for i := range out {
		out[i] = SessionEventRecord{Seq: uint64(i + 1), Bytes: []byte(`{"v":1,"role":"user","content":"secret turn ` + string(rune('A'+i)) + `"}`)}
	}
	return out
}

// devicePriv re-derives a fixture recipient device's private key.
func devicePriv(t *testing.T, node string) []byte {
	t.Helper()
	p, err := DeriveHandoffPrivKey(fxSeed(), node)
	if err != nil {
		t.Fatal(err)
	}
	return p.Bytes()
}

func TestShareSession_V2_WrapsToEveryDevice(t *testing.T) {
	f, h, em := sendRig(t)
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys)
	evs := plainEvents(3)

	res, err := h.ShareSession(context.Background(), "sess_share1", fxRecipientUser, evs)
	if err != nil {
		t.Fatalf("ShareSession: %v", err)
	}
	if res.InboxItemID == "" || res.RecipientDevices != 2 || res.ExpiresAt.Before(time.Now()) {
		t.Fatalf("result = %+v", res)
	}
	var body struct {
		ClientHandoffID string `json:"client_handoff_id"`
		Ephemeral       []byte `json:"ephemeral_public_key"`
	}
	_ = json.Unmarshal(f.lastSendBody(), &body)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(body.ClientHandoffID) {
		t.Fatalf("client_handoff_id %q does not match fleet's pattern", body.ClientHandoffID)
	}
	if body.Ephemeral != nil {
		t.Fatal("v2 send must not carry the v1 top-level ephemeral_public_key")
	}
	it := f.item(res.InboxItemID)
	if it == nil || len(it.Recipients) != 2 {
		t.Fatalf("stored item = %+v", it)
	}
	var stored []fleetHandoffEventWire
	if err := json.Unmarshal(it.Events, &stored); err != nil || len(stored) != 3 {
		t.Fatalf("stored events: %v (%d)", err, len(stored))
	}
	// Each device independently unwraps the SAME content key and opens
	// every event at its own (session, seq).
	var contentKeys [][]byte
	for _, node := range []string{fxNodeA, fxNodeB} {
		priv, _ := DeriveHandoffPrivKey(fxSeed(), node)
		fp := KeyFingerprint(priv.PublicKey().Bytes())
		var mine *fleetHandoffRecipientOut
		for i := range it.Recipients {
			if it.Recipients[i].Fingerprint == fp {
				mine = &it.Recipients[i]
			}
		}
		if mine == nil {
			t.Fatalf("no wrap for device %s", node)
		}
		ck, err := unwrapContentKey(priv, mine.EphemeralPublicKey, mine.WrappedKey, mine.WrapNonce, mine.KeyID)
		if err != nil {
			t.Fatalf("device %s unwrap: %v", node, err)
		}
		contentKeys = append(contentKeys, ck)
		for i, ev := range stored {
			pt, err := openHandoffEvent(ck, "sess_share1", ev.Seq, ev.EncryptedPayload, ev.Nonce)
			if err != nil || !bytes.Equal(pt, evs[i].Bytes) {
				t.Fatalf("device %s event %d: %v", node, ev.Seq, err)
			}
		}
	}
	if !bytes.Equal(contentKeys[0], contentKeys[1]) {
		t.Fatal("one content key per share, wrapped per device")
	}
	if bytes.Equal(it.Recipients[0].EphemeralPublicKey, it.Recipients[1].EphemeralPublicKey) {
		t.Fatal("fresh ephemeral per recipient key")
	}
	// Audit carries the inbox item id and never the plaintext.
	var found bool
	for _, e := range em.snapshot() {
		raw, _ := json.Marshal(e)
		if bytes.Contains(raw, []byte("secret turn")) {
			t.Fatal("plaintext in audit payload")
		}
		if e.Kind == contextaudit.KindFleetSessionSharedOutbound && bytes.Contains(e.Payload, []byte(res.InboxItemID)) {
			found = true
		}
	}
	if !found {
		t.Fatal("outbound audit with inbox_item_id missing")
	}
}

// AC-5: a rotation between lookup and send → 409 recipient_keys_stale with
// details.public_keys → re-wrap to that set, ONE retry, same
// client_handoff_id, success.
func TestShareSession_StaleKeys_RewrapsOnce(t *testing.T) {
	f, h, _ := sendRig(t)
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys[:1])
	f.rotateBeforeNextSend(keys) // a second device appears mid-share

	res, err := h.ShareSession(context.Background(), "sess_stale", fxRecipientUser, plainEvents(2))
	if err != nil {
		t.Fatalf("stale-key retry should succeed: %v", err)
	}
	if f.sendCount() != 2 || res.RecipientDevices != 2 {
		t.Fatalf("sends = %d devices = %d, want 2/2", f.sendCount(), res.RecipientDevices)
	}
	if got := sendBodyKeyIDs(f.lastSendBody()); len(got) != 2 {
		t.Fatalf("retry addressed %v", got)
	}
}

func TestShareSession_StaleTwice_ReadableError(t *testing.T) {
	f, h, _ := sendRig(t)
	keys := fxKeySet(t)
	f.setKeys(fxRecipientUser, keys[:1])
	stale := `{"code":"recipient_keys_stale","message":"recipient key set changed; re-wrap to the current keys","details":{"public_keys":[]}}`
	f.forceNext(http.StatusConflict, stale, "")
	_, err := h.ShareSession(context.Background(), "s", fxRecipientUser, plainEvents(1))
	var he *HandoffError
	if !errors.As(err, &he) || he.Code != "recipient_keys_stale" || !strings.Contains(err.Error(), "devices changed") {
		t.Fatalf("err = %v", err)
	}
	if f.sendCount() != 1 {
		t.Fatalf("sends = %d: a stale answer without usable keys must not loop", f.sendCount())
	}
}

// AC-4: an empty session errors readably WITHOUT any request.
func TestShareSession_Empty_NoNetwork(t *testing.T) {
	f, h, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	if _, err := h.ShareSession(context.Background(), "s", fxRecipientUser, nil); !errors.Is(err, ErrHandoffSessionEmpty) {
		t.Fatalf("err = %v", err)
	}
	if f.sendCount() != 0 {
		t.Fatal("no POST for an empty session")
	}
}

func TestShareSession_OversizeEvent_NoNetwork(t *testing.T) {
	f, h, _ := sendRig(t)
	f.setKeys(fxRecipientUser, fxKeySet(t))
	big := []SessionEventRecord{{Seq: 1, Bytes: bytes.Repeat([]byte("x"), handoffMaxEventBytes)}}
	_, err := h.ShareSession(context.Background(), "s", fxRecipientUser, big)
	if err == nil || !strings.Contains(err.Error(), "2 MiB") || f.sendCount() != 0 {
		t.Fatalf("err = %v sends = %d", err, f.sendCount())
	}
}

func TestShareSession_RecipientWithoutKeys_ReadableError(t *testing.T) {
	_, h, _ := sendRig(t)
	_, err := h.ShareSession(context.Background(), "s", fxRecipientUser, plainEvents(1))
	if !errors.Is(err, ErrHandoffRecipientNotFound) || !strings.Contains(err.Error(), "doesn't have a device") {
		t.Fatalf("err = %v", err)
	}
}

// Ledger 2026-10-06 item 4: every fleet error renders human copy — never
// "status NNN".
func TestShareSession_ErrorCopy(t *testing.T) {
	cases := []struct {
		status     int
		body       string
		retryAfter string
		want       string
	}{
		{422, `{"code":"handoff_empty","message":"a handoff must contain at least one event"}`, "", "no messages"},
		{409, `{"code":"recipient_no_key","message":"recipient has no active handoff key"}`, "", "doesn't have a device"},
		{409, `{"code":"sender_pending_limit","message":"x"}`, "", "10 shared sessions waiting"},
		{409, `{"code":"recipient_inbox_full","message":"x"}`, "", "inbox is full"},
		{409, `{"code":"client_handoff_id_conflict","message":"x"}`, "", "collided"},
		{404, `{"code":"recipient_not_found","message":"recipient not found"}`, "", "couldn't be found"},
		{400, `{"code":"invalid_recipient","message":"x"}`, "", "with yourself"},
		{413, `{"code":"handoff_too_large","message":"handoff exceeds the 16 MiB limit","details":{"limit_bytes":16777216}}`, "", "too large to share"},
		{429, `{"code":"rate_limited","message":"too many handoffs; try again later","details":{"retry_after_seconds":120}}`, "120", "2 minutes"},
		{403, `{"code":"capability_not_in_tier","message":"x"}`, "", "plan"},
		{403, `{"code":"permission_denied:handoff:use","message":"x"}`, "", "role"},
		{500, `{"code":"internal_error","message":"internal error"}`, "", "try again later"},
	}
	for _, tc := range cases {
		f, h, _ := sendRig(t)
		f.setKeys(fxRecipientUser, fxKeySet(t))
		// 5xx is retried by the client's backoff; force it on every attempt.
		f.forceN(tc.status, tc.body, tc.retryAfter, 3)
		_, err := h.ShareSession(context.Background(), "s", fxRecipientUser, plainEvents(1))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d %s: err = %v, want copy containing %q", tc.status, tc.body, err, tc.want)
			continue
		}
		if strings.Contains(strings.ToLower(err.Error()), "status ") {
			t.Errorf("%d: raw status leaked into copy: %v", tc.status, err)
		}
		var he *HandoffError
		if errors.As(err, &he) && tc.status == 429 && he.RetryAfter != 2*time.Minute {
			t.Errorf("Retry-After = %v", he.RetryAfter)
		}
	}
}
