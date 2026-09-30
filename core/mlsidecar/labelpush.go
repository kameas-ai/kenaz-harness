package mlsidecar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice/labels"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// labelPushSink names this lane's cursors in advice_label_push_cursor.
const labelPushSink = "sidecar"

// labelPushClientID is the first component of the ingest idempotency key
// (client, kind, features_hash, ts) — Amendment A3.3.
const labelPushClientID = "harness"

// defaultLabelPushBatch bounds one POST. Small enough that a single
// request stays far below the client's 5s timeout on a slow disk-bound
// engine, large enough that a from-zero re-push is a handful of calls.
const defaultLabelPushBatch = 200

// ErrNotLoopback is returned when a LabelPusher is pointed at anything
// but a loopback address. The label lane is loopback-only BY
// CONSTRUCTION (spec §4: same machine, no egress, no consent gate); a
// non-loopback base URL is refused before any byte is sent, so there is
// no configuration in which this lane reaches a network.
var ErrNotLoopback = errors.New("mlsidecar: label push refused: base URL is not loopback")

// checkLoopbackURL reports whether rawURL's host is a literal loopback IP
// or "localhost". Anything else — a public hostname, a LAN address, an
// unparseable value — is refused. It deliberately does NOT resolve DNS
// (a hostname that resolves to loopback today may not tomorrow).
func checkLoopbackURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%w: %q", ErrNotLoopback, rawURL)
	}
	host := u.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNotLoopback, rawURL)
}

// LabelPusher is the WP14 push lane: it batches advice_labels rows —
// new rows AND rows whose user_action changed since they were last pushed
// — to POST /v1/labels/{kind}, advancing a durable per-kind ack cursor
// only to what the engine acknowledges.
//
// Guarantees (each has a named proof in labelpush_test.go):
//   - resumable: the cursor lives in the harness DB, so a restart resumes
//     where the last ack left off, and ResetCursor is a full re-push from
//     zero (the harness DB is the source of truth; the engine's copy is a
//     rebuildable mirror — design §5.2);
//   - idempotent: a replayed batch is harmless (the engine upserts on
//     (client, kind, features_hash, ts), higher revision replaces);
//   - never lossy: an unhealthy sidecar, a failed POST, or a capture-off
//     toggle leaves the cursor untouched, so nothing is skipped — it is
//     simply still pending next time;
//   - gated: Enabled (the capture toggle) is consulted BEFORE any store
//     read or HTTP call — capture off means zero calls, not zero rows;
//     Healthy is consulted next, so a down engine costs no traffic;
//   - loopback-only: see ErrNotLoopback. No consent gate — this is the
//     same machine, not egress.
type LabelPusher struct {
	Client *Client
	Source labels.PushSource
	// Enabled reports whether label capture is on (the same toggle
	// CaptureAdvisor honors). nil means "on".
	Enabled func() bool
	// Healthy reports whether the sidecar is healthy right now — a cheap
	// cached read (core/advice.SidecarProbe.Healthy shape), never a probe.
	// nil means "not healthy": a pusher with no health signal pushes
	// nothing rather than guessing.
	Healthy func() bool
	// BatchSize overrides defaultLabelPushBatch when > 0.
	BatchSize int
	// Now overrides the wall clock for the pause backoff (tests). nil
	// means time.Now.
	Now func() time.Time

	mu      sync.Mutex // serializes PushOnce: one ack cursor, one writer
	running atomic.Bool
	dirty   atomic.Bool

	pauseMu sync.Mutex
	paused  map[string]LanePause
}

// Pause reasons a kind's lane can be parked for (LanePause.Reason).
const (
	// PauseContractMismatch: the engine refused the whole batch with 409
	// (feature contract / names / retained-header mismatch). Version skew
	// must be LOUD and must not hot-retry: nothing was written, the
	// cursor is kept, and the kind is parked with backoff.
	PauseContractMismatch = "contract_mismatch"
	// PauseUnknownKind: the engine answered 404 — it has no contract for
	// this kind at all.
	PauseUnknownKind = "unknown_kind"
	// PauseRowsRefused: DEFENSIVE, pre-Amendment-A4 engines only — the
	// engine acked nothing because the batch's first pending row was
	// refused (a poison row), so re-sending it on every label write would
	// be a hot retry of a deterministic refusal. An A4 engine acks past
	// every permanently-refused row (logged, see logRowRefusals), so it
	// never produces this pause.
	PauseRowsRefused = "rows_refused"
)

// Pause backoff bounds: the first pause parks a kind for
// labelPauseBaseBackoff, each consecutive one doubles it, capped at
// labelPauseMaxBackoff. A successful push of that kind clears it.
const (
	labelPauseBaseBackoff = time.Minute
	labelPauseMaxBackoff  = time.Hour
)

// LanePause is one kind's parked state — the "label lane paused" signal a
// status surface reads (LaneStatus). The cursor is untouched while a kind
// is paused, so nothing is lost; the kind is simply not re-sent until
// Until.
type LanePause struct {
	Reason  string // PauseContractMismatch | PauseUnknownKind | PauseRowsRefused
	Code    string // the engine's typed error code, when it sent one
	Detail  string
	Until   time.Time
	Backoff time.Duration
}

// LaneStatus returns a snapshot of every currently-paused kind. An empty
// map means the lane is flowing (or idle).
func (p *LabelPusher) LaneStatus() map[string]LanePause {
	out := map[string]LanePause{}
	if p == nil {
		return out
	}
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	for k, v := range p.paused {
		out[k] = v
	}
	return out
}

func (p *LabelPusher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// pausedNow reports whether kind is parked right now.
func (p *LabelPusher) pausedNow(kind string) (LanePause, bool) {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	lp, ok := p.paused[kind]
	if !ok || !p.now().Before(lp.Until) {
		return lp, false
	}
	return lp, true
}

// pause parks kind with doubling backoff and logs the distinct
// "label lane paused" line once per pause.
func (p *LabelPusher) pause(kind, reason, code, detail string) {
	p.pauseMu.Lock()
	if p.paused == nil {
		p.paused = map[string]LanePause{}
	}
	backoff := labelPauseBaseBackoff
	if prev, ok := p.paused[kind]; ok && prev.Backoff > 0 {
		backoff = prev.Backoff * 2
		if backoff > labelPauseMaxBackoff {
			backoff = labelPauseMaxBackoff
		}
	}
	lp := LanePause{Reason: reason, Code: code, Detail: detail, Until: p.now().Add(backoff), Backoff: backoff}
	p.paused[kind] = lp
	p.pauseMu.Unlock()
	logging.L().Warn("mlsidecar.labelpush.lane_paused",
		"msg", "label lane paused: "+reason,
		"kind", kind, "reason", reason, "code", code, "detail", detail,
		"backoff", backoff.String())
}

func (p *LabelPusher) clearPause(kind string) {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	delete(p.paused, kind)
}

// errLanePaused marks a pushKind error that parked the kind.
var errLanePaused = errors.New("mlsidecar: label lane paused")

// PushResult summarizes one PushOnce.
type PushResult struct {
	// Pushed counts rows the engine acknowledged this call.
	Pushed int
	// Batches counts successful POSTs.
	Batches int
	// Skipped is non-empty when the call did nothing by design:
	// "capture_disabled" or "sidecar_unhealthy".
	Skipped string
	// Paused lists kinds skipped this call because they are parked (see
	// LanePause) — no HTTP was sent for them.
	Paused []string
	// RowsRefused counts rows the engine PERMANENTLY refused this call
	// (design Amendment A4: client/kind mismatch, unknown user_action,
	// invalid features, row too large). The engine acks past them and the
	// cursor moves on — each is logged (mlsidecar.labelpush.row_refused);
	// their presence is not an error.
	RowsRefused int
}

// PushOnce drains every kind's pending rows to the engine. It returns the
// first error encountered (other kinds are still attempted, so one
// misbehaving kind cannot starve the rest) — the cursor of a failed kind
// is untouched, so the next call retries it.
func (p *LabelPusher) PushOnce(ctx context.Context) (PushResult, error) {
	var res PushResult
	if p == nil || p.Client == nil || p.Source == nil {
		return res, errors.New("mlsidecar: label pusher not configured")
	}
	// Gate 1: the capture toggle. Checked before ANY store or network
	// call (AC-06's call-count discipline, applied to the push lane).
	if p.Enabled != nil && !p.Enabled() {
		res.Skipped = "capture_disabled"
		return res, nil
	}
	// Gate 2: health. An unhealthy (or unknown) sidecar receives nothing
	// and loses nothing — the cursor stays put.
	if p.Healthy == nil || !p.Healthy() {
		res.Skipped = "sidecar_unhealthy"
		return res, nil
	}
	// Gate 3: loopback-only, re-checked every call (BaseURL is a public
	// field; a mutation after construction must not open an egress path).
	if err := checkLoopbackURL(p.Client.BaseURL); err != nil {
		return res, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	kinds, err := p.Source.PushKinds(ctx)
	if err != nil {
		return res, err
	}
	var firstErr error
	for _, kind := range kinds {
		if _, parked := p.pausedNow(kind); parked {
			res.Paused = append(res.Paused, kind)
			continue
		}
		n, batches, refused, kerr := p.pushKind(ctx, kind)
		if kerr == nil {
			p.clearPause(kind)
		}
		res.Pushed += n
		res.Batches += batches
		res.RowsRefused += refused
		if kerr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mlsidecar: push labels for kind %q: %w", kind, kerr)
			}
			logging.L().Debug("mlsidecar.labelpush.kind_failed", "kind", kind, "err", kerr.Error())
		}
		if ctx.Err() != nil {
			break
		}
	}
	return res, firstErr
}

func (p *LabelPusher) pushKind(ctx context.Context, kind string) (pushed, batches, refused int, err error) {
	batchSize := p.BatchSize
	if batchSize <= 0 {
		batchSize = defaultLabelPushBatch
	}
	for {
		if err := ctx.Err(); err != nil {
			return pushed, batches, refused, err
		}
		cur, err := p.Source.LoadCursor(ctx, labelPushSink, kind)
		if err != nil {
			return pushed, batches, refused, err
		}
		rows, err := p.Source.PendingSince(ctx, kind, cur.Revision, batchSize)
		if err != nil {
			return pushed, batches, refused, err
		}
		if len(rows) == 0 {
			return pushed, batches, refused, nil
		}
		wire := make([]LabelWireRow, 0, len(rows))
		for _, r := range rows {
			wire = append(wire, toWireRow(r))
		}
		resp, err := p.Client.PushLabels(ctx, kind, LabelPushRequest{Client: labelPushClientID, Rows: wire})
		if err != nil {
			var se *StatusError
			if errors.As(err, &se) && (se.Status == 409 || se.Status == 404) {
				// Whole-batch refusal (409 contract/names/header mismatch,
				// 404 unknown kind): nothing was written, and re-sending
				// the same rows can only be refused again. Park the kind
				// (cursor kept) instead of hot-retrying on every label
				// write. 503 / transport errors stay plain retryable
				// errors — those are transient by nature.
				reason := PauseContractMismatch
				if se.Status == 404 {
					reason = PauseUnknownKind
				}
				p.pause(kind, reason, se.Code, err.Error())
				return pushed, batches, refused, fmt.Errorf("%w (%s): %v", errLanePaused, reason, err)
			}
			return pushed, batches, refused, err
		}
		batches++
		logRowRefusals(kind, resp.Refusals)
		if resp.Acked.Revision == 0 && resp.Refused > 0 {
			// Defensive: a pre-A4 engine answered acked:null when the
			// batch's FIRST row was refused (an A4 engine acks past every
			// permanent refusal, so this never fires against it).
			// The first pending row was refused, so nothing is acked and
			// the next attempt would send the very same row first again.
			detail := fmt.Sprintf("%d row(s) refused", resp.Refused)
			if len(resp.Refusals) > 0 {
				r0 := resp.Refusals[0]
				detail = fmt.Sprintf("%s; first: revision %d (%s)", detail, r0.Revision, r0.Reason)
			}
			p.pause(kind, PauseRowsRefused, "", detail)
			return pushed, batches, refused, fmt.Errorf("%w (%s): %s", errLanePaused, PauseRowsRefused, detail)
		}
		// The cursor advances ONLY to what the engine acknowledged, and
		// never backwards, and never past the batch we actually sent (a
		// bogus ack must not skip unsent rows).
		last := rows[len(rows)-1].Revision
		if resp.Acked.Revision <= cur.Revision || resp.Acked.Revision > last {
			return pushed, batches, refused, fmt.Errorf("engine ack revision %d outside the pushed window (%d, %d]", resp.Acked.Revision, cur.Revision, last)
		}
		if err := p.Source.SaveCursor(ctx, labelPushSink, kind, labels.PushCursor{TS: resp.Acked.TS, Revision: resp.Acked.Revision}); err != nil {
			return pushed, batches, refused, err
		}
		refusedRev := make(map[int64]bool, len(resp.Refusals))
		for _, rf := range resp.Refusals {
			refusedRev[rf.Revision] = true
		}
		for _, r := range rows {
			if r.Revision > resp.Acked.Revision {
				continue
			}
			if refusedRev[r.Revision] {
				refused++
				continue
			}
			pushed++
		}
		if resp.Acked.Revision >= last && len(rows) < batchSize {
			return pushed, batches, refused, nil
		}
	}
}

// logRowRefusals logs each PERMANENTLY refused row distinctly (design
// Amendment A4): the engine has already acked past it, so it will never be
// re-sent — this line is the only trace the harness keeps that the engine
// holds no copy of that label.
func logRowRefusals(kind string, refusals []LabelRowRefusal) {
	for _, rf := range refusals {
		logging.L().Warn("mlsidecar.labelpush.row_refused",
			"kind", kind, "features_hash", rf.FeaturesHash, "ts", rf.TS,
			"revision", rf.Revision, "reason", rf.Reason)
	}
}

func toWireRow(r labels.PushRow) LabelWireRow {
	features := json.RawMessage(r.FeaturesJSON)
	if !json.Valid(features) {
		features = json.RawMessage("{}")
	}
	return LabelWireRow{
		Kind:             r.KindID,
		PromptVersion:    r.PromptVersion,
		FeaturesHash:     r.FeaturesHash,
		TS:               r.TS,
		Revision:         r.Revision,
		Features:         features,
		FeaturesComplete: r.FeaturesComplete,
		Model:            r.ModelID,
		Rung:             r.Rung,
		Decision:         r.Decision,
		Confidence:       r.Confidence,
		Shown:            r.Shown,
		UserAction:       string(r.UserAction),
		LatencyMS:        r.LatencyMS,
	}
}

// ResetCursor forgets this lane's cursors so the next PushOnce re-sends
// every label from zero — the mirror-loss recovery path. Idempotent on
// the engine side, so a needless reset costs only traffic.
func (p *LabelPusher) ResetCursor(ctx context.Context) error {
	if p == nil || p.Source == nil {
		return errors.New("mlsidecar: label pusher not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Source.ResetCursors(ctx, labelPushSink)
}

// Run pushes every interval until ctx is cancelled. Kept for a caller
// that owns a lifecycle (the WP13 Manager wiring); the event-driven
// Nudge below is what newLLMStack uses today.
func (p *LabelPusher) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := p.PushOnce(ctx); err != nil {
			logging.L().Debug("mlsidecar.labelpush.tick_failed", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// nudgeTimeout bounds one background drain started by Nudge.
const nudgeTimeout = 15 * time.Second

// Nudge is the event-driven trigger: called after every label write
// (labels.WithAfterWrite), it starts ONE coalesced background drain.
// Cheap and non-blocking by construction — with capture off or the
// sidecar unhealthy it returns before spawning anything, and a drain
// already in flight just gets flagged to loop once more. Rows a nudge
// could not push (unhealthy at the time) stay pending; the next nudge
// (or Run tick) picks them up, so nothing is lost by a missed nudge.
func (p *LabelPusher) Nudge() {
	if p == nil || p.Client == nil || p.Source == nil {
		return
	}
	if p.Enabled != nil && !p.Enabled() {
		return
	}
	if p.Healthy == nil || !p.Healthy() {
		return
	}
	p.dirty.Store(true)
	if !p.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for {
			for p.dirty.Swap(false) {
				ctx, cancel := context.WithTimeout(context.Background(), nudgeTimeout)
				if _, err := p.PushOnce(ctx); err != nil {
					logging.L().Debug("mlsidecar.labelpush.nudge_failed", "err", err.Error())
				}
				cancel()
			}
			p.running.Store(false)
			// A Nudge that landed between the last dirty check and the
			// running=false store saw running==true and returned; catch it
			// here rather than dropping it.
			if !p.dirty.Load() || !p.running.CompareAndSwap(false, true) {
				return
			}
		}
	}()
}
