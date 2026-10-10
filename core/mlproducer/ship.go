package mlproducer

// ship.go — the ML-lane shipper (spec §6; ml-producer-01MLPRD01 WP03).
// Drains the durable outbox (mlstore) to Fleet's OTLP logs endpoint
// (kenaz-fleet docs/contract-harness-ml.md, "Ingest (phase 2C)").
//
// Encoding: OTLP **protobuf**, not JSON. Fleet's receiver decodes an
// `application/json` body with encoding/json straight into the generated
// proto structs (service/telemetry/receiver.go unmarshalOTLP), which
// matches neither the OTLP JSON field names nor the AnyValue oneof: a
// standard OTLP-JSON request decodes to ZERO resource logs and Fleet
// answers 200 with nothing routed to the ML lane. The contract allows
// "protobuf or JSON"; protobuf is the only one that arrives today. The
// spec's "body as string-encoded JSON" is kept: each record's body is the
// JSON object as an AnyValue string.
//
// Loop shape mirrors core/fleet/audit_archive.go (Start idempotent, Stop
// terminal, wake channel, 30 s → 15 m backoff, Retry-After honoured), plus
// Pause (restartable: sign-out, node_removed, org pause) and the 413 split
// audit_archive does not have (spec §12 A-8).
//
// Never logs a record body, a row id or an attribute value.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	collogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
)

// Wire constants.
const (
	// OTLPLogsPath is Fleet's OTLP logs route.
	OTLPLogsPath = "/otlp/v1/logs"
	// OTLPContentType is the request encoding (see the file comment).
	OTLPContentType = "application/x-protobuf"
	// ServiceName is the service.name resource attribute.
	ServiceName = "kenaz-harness"
	// scopeName names the instrumentation scope of every record.
	scopeName = "kenaz-harness/mlproducer"
)

// Batch caps (spec §6; Fleet's are 500 ML records, 256 KiB JSON / 1 MiB
// protobuf, 64 KiB per record body).
const (
	MaxBatchRecords = 500
	MaxBatchBytes   = 200 << 10
	MaxRecordBytes  = 64 << 10
)

// Cadence defaults.
const (
	DefaultShipInterval = 30 * time.Second
	DefaultWakePending  = 100
	defaultPollEvery    = 5 * time.Second
	shipBackoffBase     = 30 * time.Second
	shipBackoffMax      = 15 * time.Minute
	// maxRetryAfter bounds a Retry-After (the daily cap's runs to UTC
	// midnight; nothing legitimate is longer than a day).
	maxRetryAfter   = 24 * time.Hour
	reEnrollTimeout = 60 * time.Second
)

// Fleet ML-lane error codes (contract "Responses").
const (
	CodeMLNotEffective      = "ml_not_effective"
	CodeMLNodeNotEnrolled   = "ml_node_not_enrolled"
	CodeUnsupportedSchema   = "unsupported_schema_version"
	CodeMLInvalidTable      = "ml_invalid_table"
	CodeMLBatchTooLarge     = "ml_batch_too_large"
	codeOrgPaused           = "org_paused"
	rejectionRecordTooLarge = "record_too_large"
	rejectionLocalOversize  = "local_record_too_large"
)

// StopUpdateHarness is the stop reason for a batch Fleet refuses as
// unsupported_schema_version / ml_invalid_table: a newer harness is
// needed; nothing more is sent by this process.
const StopUpdateHarness = "update the harness"

// ErrOrgPaused is what a Poster returns (wrapped) for a 403 org_paused —
// fleet.Client converts that refusal into an error before the body is
// seen. A hold, not a withdrawal.
var ErrOrgPaused = errors.New("mlproducer: fleet org paused")

// PostResult is one HTTP answer to a batch.
type PostResult struct {
	Status     int
	RetryAfter string
	Body       []byte
}

// Poster sends one encoded batch (protobuf, OTLPContentType) to
// OTLPLogsPath. core/rpc implements it over fleet.Client.Post, which
// refreshes the token once on 401 and retries 5xx internally (so a 503
// usually arrives as an error, without its Retry-After).
type Poster interface {
	PostLogs(ctx context.Context, body []byte) (PostResult, error)
}

// ShipGate is the gate surface the shipper needs; *ConsentGate has it.
type ShipGate interface {
	Shipping(ctx context.Context) Decision
	NotEffective(ctx context.Context)
}

// ShipperConfig wires a Shipper.
type ShipperConfig struct {
	Store  *mlstore.Store
	Poster Poster
	Gate   ShipGate
	// ReEnroll re-registers the node after 403 ml_node_not_enrolled
	// (core/rpc: settings.API.FleetRefreshIdentity). nil = hold until
	// the enrolment generation changes on its own (a sign-in).
	ReEnroll func(ctx context.Context) error
	// EnrollGen is a counter core/rpc bumps whenever enrolment may have
	// changed (session reset, a successful re-enroll). A batch refused
	// ml_node_not_enrolled is not resent until it moves. nil = never
	// resend in this process.
	EnrollGen func() uint64
	// Interval is the idle ship cadence. 0 = 30 s.
	Interval time.Duration
	// WakePending ships early once this many records are pending. 0 = 100.
	WakePending int
	// PollEvery is how often an idle loop checks the pending count. 0 = 5 s.
	PollEvery time.Duration
	// BackoffBase / BackoffMax: the error ladder. 0 = 30 s / 15 m.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// Now is the clock. nil = time.Now.
	Now func() time.Time
}

// ShipStatus is the shipper's health (Settings → Sync → Cloud ML).
type ShipStatus struct {
	Running     bool
	LastBatchAt time.Time
	Batches     int64
	Accepted    int64
	Duplicates  int64
	Rejected    int64
	// Rejections counts rejected records by Fleet's reason, plus
	// local_record_too_large for records over MaxRecordBytes that were
	// dropped without being sent.
	Rejections map[string]int64
	// StopReason is why nothing is being shipped ("" = shipping or idle).
	StopReason string
}

// Shipper drains the outbox. Safe for concurrent use.
type Shipper struct {
	cfg ShipperConfig

	// shipMu serialises cycles (the loop and ShipNow).
	shipMu     sync.Mutex
	enrollHold *uint64 // gen at the ml_node_not_enrolled refusal; guarded by shipMu

	reEnrolling atomic.Bool
	latched     atomic.Bool
	running     atomic.Bool
	wake        chan struct{}

	lifeMu  sync.Mutex
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}

	stMu sync.Mutex
	st   ShipStatus
}

// NewShipper returns a stopped shipper; Start runs it.
func NewShipper(cfg ShipperConfig) *Shipper {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultShipInterval
	}
	if cfg.WakePending <= 0 {
		cfg.WakePending = DefaultWakePending
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = defaultPollEvery
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = shipBackoffBase
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = shipBackoffMax
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Shipper{cfg: cfg, wake: make(chan struct{}, 1), st: ShipStatus{Rejections: map[string]int64{}}}
}

// ---- lifecycle ----

// Start runs the loop on ctx. Idempotent; a no-op after Stop or after a
// permanent refusal (StopUpdateHarness). Restartable after Pause.
func (s *Shipper) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.stopped || s.latched.Load() || s.cancel != nil {
		return
	}
	lctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.running.Store(true)
	go func() {
		defer close(done)
		defer s.running.Store(false)
		s.loop(lctx)
	}()
}

// Pause stops the loop and waits for it; Start may run it again. reason
// becomes the status stop reason ("" leaves it). Must not be called from
// the shipper's own goroutine (gate OnChange runs there: core/rpc hands it
// to a goroutine).
func (s *Shipper) Pause(reason string) {
	if s == nil {
		return
	}
	s.halt()
	if reason != "" {
		s.setStop(reason)
	}
}

// Stop ends the loop for good and waits for it.
func (s *Shipper) Stop() {
	if s == nil {
		return
	}
	s.lifeMu.Lock()
	s.stopped = true
	s.lifeMu.Unlock()
	s.halt()
}

func (s *Shipper) halt() {
	s.lifeMu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.lifeMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Running reports whether the loop goroutine is alive.
func (s *Shipper) Running() bool { return s != nil && s.running.Load() }

// Nudge wakes an idle loop now.
func (s *Shipper) Nudge() {
	if s == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ShipNow ships until the outbox is empty, the gate closes, a batch fails
// or ctx ends — the shutdown drain (bounded by ctx). Safe to call with the
// loop stopped.
func (s *Shipper) ShipNow(ctx context.Context) error {
	if s == nil {
		return nil
	}
	for ctx.Err() == nil {
		if r := s.cycle(ctx); !r.progressed {
			return nil
		}
	}
	return ctx.Err()
}

// Status returns a copy of the shipping status. Cheap; never blocks on
// I/O (the settings panel reads it on every render).
func (s *Shipper) Status() ShipStatus {
	if s == nil {
		return ShipStatus{}
	}
	s.stMu.Lock()
	defer s.stMu.Unlock()
	out := s.st
	out.Rejections = make(map[string]int64, len(s.st.Rejections))
	for k, v := range s.st.Rejections {
		out.Rejections[k] = v
	}
	out.Running = s.running.Load()
	return out
}

func (s *Shipper) setStop(reason string) {
	s.stMu.Lock()
	changed := s.st.StopReason != reason
	s.st.StopReason = reason
	s.stMu.Unlock()
	if changed && reason != "" {
		logging.L().Info("mlproducer.ship.stopped", "reason", reason)
	}
}

// ---- loop ----

type cycleResult struct {
	progressed bool          // a batch settled; more may be pending
	empty      bool          // nothing pending: idle, waking early on a pile-up
	held       bool          // gate closed / enrolment hold: idle
	backoff    bool          // an error: next backoff tier
	wait       time.Duration // a Retry-After
	latched    bool          // permanent stop
}

func (s *Shipper) loop(ctx context.Context) {
	backoff := s.cfg.BackoffBase
	for {
		r := s.cycle(ctx)
		if ctx.Err() != nil || r.latched {
			return
		}
		var wait time.Duration
		pollPending := false
		switch {
		case r.progressed:
			backoff = s.cfg.BackoffBase
			continue
		case r.wait > 0:
			wait = r.wait
		case r.backoff:
			wait = backoff
			if backoff < s.cfg.BackoffMax {
				backoff *= 2
				if backoff > s.cfg.BackoffMax {
					backoff = s.cfg.BackoffMax
				}
			}
		default:
			backoff = s.cfg.BackoffBase
			wait = s.cfg.Interval
			pollPending = r.empty
		}
		if !s.sleep(ctx, wait, pollPending) {
			return
		}
	}
}

// sleep waits d, a Nudge, or (pollPending) a pending count reaching
// WakePending. false = ctx ended.
func (s *Shipper) sleep(ctx context.Context, d time.Duration, pollPending bool) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	var poll <-chan time.Time
	if pollPending && s.cfg.Store != nil {
		pt := time.NewTicker(s.cfg.PollEvery)
		defer pt.Stop()
		poll = pt.C
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		case <-s.wake:
			return true
		case <-poll:
			if n, err := s.cfg.Store.Pending(ctx); err == nil && n >= s.cfg.WakePending {
				return true
			}
		}
	}
}

// cycle ships at most one batch.
func (s *Shipper) cycle(ctx context.Context) cycleResult {
	s.shipMu.Lock()
	defer s.shipMu.Unlock()
	if s.latched.Load() {
		return cycleResult{latched: true}
	}
	if s.cfg.Store == nil || s.cfg.Poster == nil || s.cfg.Gate == nil {
		return cycleResult{held: true}
	}
	pending, err := s.cfg.Store.Pending(ctx)
	if err != nil {
		logging.L().Warn("mlproducer.ship.pending_failed", "err", err.Error())
		return cycleResult{backoff: true}
	}
	if pending == 0 {
		return cycleResult{empty: true}
	}
	if s.enrollHold != nil {
		if s.cfg.EnrollGen == nil || s.cfg.EnrollGen() == *s.enrollHold {
			return cycleResult{held: true}
		}
		s.enrollHold = nil // enrolment changed: the batch may go again
	}
	d := s.cfg.Gate.Shipping(ctx)
	if !d.Open {
		s.setStop(d.Reason)
		return cycleResult{held: true}
	}
	recs, err := s.cfg.Store.ReadBatch(ctx, 0, MaxBatchRecords)
	if err != nil {
		logging.L().Warn("mlproducer.ship.read_failed", "err", err.Error())
		return cycleResult{backoff: true}
	}
	if len(recs) == 0 {
		return cycleResult{empty: true}
	}
	return s.ship(ctx, d, recs)
}

// ship sends the longest prefix of recs that fits the caps, splitting on
// 413, and settles the outbox on a 2xx.
func (s *Shipper) ship(ctx context.Context, d Decision, recs []mlstore.Record) cycleResult {
	n := len(recs)
	for {
		window := recs[:n]
		last := window[n-1].Seq
		send, oversize := prepareBatch(window)
		if len(send) == 0 {
			// Every record in the window is over the per-record cap: drop
			// them (they can never be accepted) and move on.
			if err := s.settle(ctx, last); err != nil {
				return cycleResult{backoff: true}
			}
			s.countLocal(oversize)
			return cycleResult{progressed: true}
		}
		body, err := EncodeBatch(d.ResourceOrgID, d.NodeID, send)
		if err != nil {
			logging.L().Warn("mlproducer.ship.encode_failed", "err", err.Error())
			return cycleResult{backoff: true}
		}
		if len(body) > MaxBatchBytes && n > 1 {
			n /= 2
			continue
		}
		res, err := s.cfg.Poster.PostLogs(ctx, body)
		if err != nil {
			if errors.Is(err, ErrOrgPaused) {
				s.setStop(ReasonOrgPaused)
			} else if ctx.Err() == nil {
				logging.L().Warn("mlproducer.ship.post_failed", "err", err.Error(), "records", len(send))
			}
			return cycleResult{backoff: true}
		}
		code := errorCode(res.Body)
		switch {
		case res.Status >= 200 && res.Status < 300:
			if err := s.settle(ctx, last); err != nil {
				return cycleResult{backoff: true}
			}
			s.countLocal(oversize)
			s.countAccepted(res.Body, len(send))
			s.setStop("")
			return cycleResult{progressed: true}

		case res.Status == http.StatusForbidden && code == CodeMLNotEffective:
			// THE stop signal: purge (inside the gate), re-read later.
			s.cfg.Gate.NotEffective(ctx)
			s.setStop(ReasonNotEffective)
			return cycleResult{held: true}

		case res.Status == http.StatusForbidden && code == CodeMLNodeNotEnrolled:
			gen := uint64(0)
			if s.cfg.EnrollGen != nil {
				gen = s.cfg.EnrollGen()
			}
			s.enrollHold = &gen
			s.setStop(CodeMLNodeNotEnrolled)
			s.triggerReEnroll()
			return cycleResult{held: true}

		case res.Status == http.StatusForbidden && code == codeOrgPaused:
			s.setStop(ReasonOrgPaused)
			return cycleResult{backoff: true}

		case res.Status == http.StatusBadRequest && (code == CodeUnsupportedSchema || code == CodeMLInvalidTable):
			s.latched.Store(true)
			s.setStop(StopUpdateHarness + " (" + code + ")")
			return cycleResult{latched: true}

		case res.Status == http.StatusRequestEntityTooLarge:
			if n > 1 {
				n /= 2
				continue
			}
			// One record Fleet will not take: drop it rather than wedge.
			if err := s.settle(ctx, last); err != nil {
				return cycleResult{backoff: true}
			}
			s.countLocal(oversize)
			s.countRejected(rejectionRecordTooLarge, 1)
			return cycleResult{progressed: true}

		case res.Status == http.StatusTooManyRequests || res.Status == http.StatusServiceUnavailable:
			if w, ok := parseRetryAfter(res.RetryAfter, s.cfg.Now()); ok && w > 0 {
				return cycleResult{wait: w}
			}
			return cycleResult{backoff: true}

		default:
			logging.L().Warn("mlproducer.ship.refused", "status", res.Status, "code", code, "records", len(send))
			return cycleResult{backoff: true}
		}
	}
}

func (s *Shipper) settle(ctx context.Context, last int64) error {
	if _, err := s.cfg.Store.DeleteThrough(ctx, last); err != nil {
		logging.L().Warn("mlproducer.ship.settle_failed", "err", err.Error())
		return err
	}
	return nil
}

func (s *Shipper) triggerReEnroll() {
	if s.cfg.ReEnroll == nil || !s.reEnrolling.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.reEnrolling.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), reEnrollTimeout)
		defer cancel()
		if err := s.cfg.ReEnroll(ctx); err != nil {
			logging.L().Warn("mlproducer.ship.reenroll_failed", "err", err.Error())
			return
		}
		s.Nudge()
	}()
}

// ---- status counters ----

// ingestResponse is Fleet's 2xx body (contract "Responses").
type ingestResponse struct {
	Accepted int `json:"accepted"`
	ML       *struct {
		Accepted   int            `json:"accepted"`
		Duplicates int            `json:"duplicates"`
		Rejected   int            `json:"rejected"`
		Rejections map[string]int `json:"rejections"`
	} `json:"ml"`
}

func (s *Shipper) countAccepted(body []byte, sent int) {
	var r ingestResponse
	_ = json.Unmarshal(body, &r)
	s.stMu.Lock()
	defer s.stMu.Unlock()
	s.st.Batches++
	s.st.LastBatchAt = s.cfg.Now()
	if r.ML == nil {
		// A 2xx without the ML block: Fleet routed nothing to the lane.
		// Count it so the panel does not report success it did not see.
		s.st.Rejected += int64(sent)
		s.st.Rejections["no_ml_result"] += int64(sent)
		return
	}
	s.st.Accepted += int64(r.ML.Accepted)
	s.st.Duplicates += int64(r.ML.Duplicates)
	s.st.Rejected += int64(r.ML.Rejected)
	for reason, n := range r.ML.Rejections {
		s.st.Rejections[reason] += int64(n)
	}
}

func (s *Shipper) countRejected(reason string, n int) {
	s.stMu.Lock()
	s.st.Rejected += int64(n)
	s.st.Rejections[reason] += int64(n)
	s.stMu.Unlock()
}

func (s *Shipper) countLocal(oversize int) {
	if oversize > 0 {
		s.countRejected(rejectionLocalOversize, oversize)
	}
}

// ---- batch construction ----

// prepareBatch drops records over MaxRecordBytes (never sent) and
// coalesces task upserts to the newest per row id (task upserts are
// separate outbox rows; Fleet ignores an update older than the stored
// last_active anyway). Order is by seq.
func prepareBatch(window []mlstore.Record) (send []mlstore.Record, oversize int) {
	newestTask := map[string]int64{}
	for _, r := range window {
		if r.Table == mlstore.TableTasks {
			newestTask[r.RowID] = r.Seq
		}
	}
	for _, r := range window {
		if len(r.Body) > MaxRecordBytes {
			oversize++
			continue
		}
		if r.Table == mlstore.TableTasks && newestTask[r.RowID] != r.Seq {
			continue
		}
		send = append(send, r)
	}
	return send, oversize
}

func strKV(k, v string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: k, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: v}}}
}

// EncodeBatch builds the OTLP ExportLogsServiceRequest for recs (protobuf).
// One resource carries kameas.org.id (the token's resource-owner org),
// kameas.node.id (the enrolled node id) and service.name; every record
// carries exactly the five kameas.ml.* attributes and its JSON body as a
// string.
func EncodeBatch(resourceOrgID, nodeID string, recs []mlstore.Record) ([]byte, error) {
	if resourceOrgID == "" || nodeID == "" {
		return nil, fmt.Errorf("mlproducer: encode: missing org or node id")
	}
	lrs := make([]*logsv1.LogRecord, 0, len(recs))
	for _, r := range recs {
		ts := uint64(0)
		if r.CreatedAt > 0 {
			ts = uint64(r.CreatedAt) * uint64(time.Millisecond)
		}
		lrs = append(lrs, &logsv1.LogRecord{
			TimeUnixNano:         ts,
			ObservedTimeUnixNano: ts,
			Attributes: []*commonv1.KeyValue{
				strKV("kameas.ml.table", r.Table),
				strKV("kameas.ml.op", r.Op),
				strKV("kameas.ml.row_id", r.RowID),
				strKV("kameas.ml.source", ProducerSource),
				{Key: "kameas.ml.schema_version", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: SchemaVersion}}},
			},
			Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: string(r.Body)}},
		})
	}
	req := &collogsv1.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			strKV("service.name", ServiceName),
			strKV("kameas.org.id", resourceOrgID),
			strKV("kameas.node.id", nodeID),
		}},
		ScopeLogs: []*logsv1.ScopeLogs{{
			Scope:      &commonv1.InstrumentationScope{Name: scopeName},
			LogRecords: lrs,
		}},
	}}}
	return proto.Marshal(req)
}

// errorCode reads Fleet's {code,message} envelope ("" when absent).
func errorCode(body []byte) string {
	var env struct {
		Code string `json:"code"`
	}
	if len(body) == 0 || json.Unmarshal(body, &env) != nil {
		return ""
	}
	return env.Code
}

// parseRetryAfter reads delta-seconds or an HTTP date, capped at a day.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
		if d < 0 {
			d = 0
		}
	} else {
		return 0, false
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d, true
}
