package fleet

// fakeMemoryFleet is an in-memory model of kenaz-fleet's /api/v1/memory
// store (service/memory/store.go @ fleet main 7fb62de), faithful to the
// merge rules in docs/contract-harness-memory.md §3: alias resolution,
// absorbing forgotten/superseded tombstones, left_sync_scope revival,
// per-field HLC LWW, per-device recall G-counter, content-hash dedupe,
// narrative precedence, scope gating, clock_in_future, a credential check,
// the cursor floor and forget-all. It exists so the two-device tests
// exercise the harness client against Fleet's semantics, not against a
// stub that answers "accepted" to everything.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

type fakeMemRec struct {
	id, state, reason, supersededBy string
	content, hash, kind, turnID     string
	source, sourceTurn, toolName    string
	filesRead, filesModified        []string
	createdAt, lastAccessed         string
	title, titleHLC                 string
	pinned                          bool
	pinnedHLC, scope, scopeHLC      string
	weight                          float64
	seq                             int64
	recall                          map[string]int64 // device → counter
}

type fakeMemoryFleet struct {
	mu      sync.Mutex
	now     func() time.Time
	enabled bool
	scopes  map[string]bool
	recs    map[string]*fakeMemRec
	aliases map[string]string
	seq     int64
	floor   int64
	// erasedAt / erasedBefore mirror Fleet's memory_user_state
	// (erased_at_seq, erased_before): set by forget-all, and a pull whose
	// cursor predates erasedAt resets with reason "erased". The first
	// version of this fake decided "erased" by len(recs)==0 and stamped
	// erased_before at PULL time, which hid the F1 erase-replay defect.
	erasedAt     int64
	erasedBefore string
	// failPut forces the next N PUT /settings to answer 500; status413
	// the next N item-bearing pushes to answer 413.
	failPut   int
	status413 int
	requests  int
	// status429 / status403 force the next N requests to fail.
	status429, status403 int
	pushes               []memPushRequest
}

func newFakeMemoryFleet(now func() time.Time) *fakeMemoryFleet {
	return &fakeMemoryFleet{now: now, enabled: true,
		scopes: map[string]bool{"global": true, "long_term": true},
		recs:   map[string]*fakeMemRec{}, aliases: map[string]string{}}
}

func (f *fakeMemoryFleet) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeMemoryFleet) pushLog() []memPushRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]memPushRequest(nil), f.pushes...)
}

func (f *fakeMemoryFleet) live(id string) *fakeMemRec {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r := f.recs[id]; r != nil && r.state == "live" {
		cp := *r
		return &cp
	}
	return nil
}

func (f *fakeMemoryFleet) total(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var t int64
	if r := f.recs[id]; r != nil {
		for _, v := range r.recall {
			t += v
		}
	}
	return t
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeMemoryFleet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Date", f.now().UTC().Format(http.TimeFormat))
	if f.status429 > 0 {
		f.status429--
		w.Header().Set("Retry-After", "120")
		writeJSON(w, 429, map[string]any{"code": "rate_limited", "message": "slow down"})
		return
	}
	if f.status403 > 0 {
		f.status403--
		writeJSON(w, 403, map[string]any{"code": "capability_not_in_tier", "message": "tier"})
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/memory/settings":
		writeJSON(w, 200, f.settingsLocked())
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/memory/settings" && f.failPut > 0:
		f.failPut--
		writeJSON(w, 500, map[string]any{"code": "internal_error"})
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/memory/settings":
		var u struct {
			Enabled        *bool     `json:"enabled"`
			Scopes         *[]string `json:"scopes"`
			ConsentVersion string    `json:"consent_version"`
		}
		_ = json.Unmarshal(body, &u)
		if u.Enabled != nil && *u.Enabled && u.ConsentVersion == "" {
			writeJSON(w, 422, map[string]any{"code": "consent_version_required"})
			return
		}
		if u.Enabled != nil {
			f.enabled = *u.Enabled
		}
		if u.Scopes != nil {
			f.scopes = map[string]bool{}
			for _, s := range *u.Scopes {
				f.scopes[s] = true
			}
		}
		writeJSON(w, 200, f.settingsLocked())
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/memory/pull":
		f.pullLocked(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/memory/push":
		var req memPushRequest
		if err := json.Unmarshal(body, &req); err != nil || len(req.Items) > 100 {
			writeJSON(w, 400, map[string]any{"code": "invalid_request_body"})
			return
		}
		if f.status413 > 0 && len(req.Items) > 0 {
			f.status413--
			writeJSON(w, 413, map[string]any{"code": "payload_too_large"})
			return
		}
		f.pushes = append(f.pushes, req)
		f.pushLocked(w, req)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/memory/forget-all":
		n := 0
		for _, rec := range f.recs {
			if rec.state == "live" {
				n++
			}
		}
		f.recs, f.aliases = map[string]*fakeMemRec{}, map[string]string{}
		f.seq++
		f.floor = f.seq
		f.erasedAt = f.seq
		f.erasedBefore = memory.FormatHLC(f.now().UnixMilli(), 0, "fleet")
		writeJSON(w, 200, map[string]any{"erased": n, "erased_before": f.erasedBefore})
	default:
		writeJSON(w, 404, map[string]any{"code": "not_found"})
	}
}

func (f *fakeMemoryFleet) settingsLocked() MemorySyncSettings {
	var sc []string
	for s := range f.scopes {
		sc = append(sc, s)
	}
	sort.Strings(sc)
	return MemorySyncSettings{Enabled: f.enabled, Scopes: sc, ConsentVersion: "v"}
}

// fakeCursor mirrors fleet service/memory ParseCursorFull (#191): "" or
// "s<seq>.<floor>.<erase>" is a snapshot cursor; a plain number is a given
// (incremental) cursor. The CLIENT never parses cursors; only this fake.
type fakeCursor struct {
	seq, floor, erased int64
	given, snapshot    bool
}

func parseFakeCursor(c string) fakeCursor {
	if c == "" {
		return fakeCursor{}
	}
	if strings.HasPrefix(c, "s") {
		var fc fakeCursor
		parts := strings.Split(c[1:], ".")
		fc.seq, _ = strconv.ParseInt(parts[0], 10, 64)
		fc.floor, _ = strconv.ParseInt(parts[1], 10, 64)
		fc.erased, _ = strconv.ParseInt(parts[2], 10, 64)
		fc.snapshot = true
		return fc
	}
	n, _ := strconv.ParseInt(c, 10, 64)
	return fakeCursor{seq: n, given: true}
}

// staleLocked mirrors userState.stale (#191): a plain cursor is checked
// against the floor; a snapshot cursor only against its embedded epoch.
func (f *fakeMemoryFleet) staleLocked(c fakeCursor) string {
	switch {
	case c.given:
		if c.seq < f.floor {
			if c.seq < f.erasedAt {
				return "erased"
			}
			return "cursor_expired"
		}
	case c.snapshot:
		if c.erased != f.erasedAt {
			return "erased"
		}
		if f.floor > c.floor {
			return "cursor_expired"
		}
	}
	return ""
}

func (f *fakeMemoryFleet) pullLocked(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw := q.Get("cursor")
	if !f.enabled {
		writeJSON(w, 200, memPullResponse{Enabled: false, Records: []memPullRecord{}, Cursor: raw})
		return
	}
	cu := parseFakeCursor(raw)
	if reason := f.staleLocked(cu); reason != "" {
		erased := ""
		if reason == "erased" {
			erased = f.erasedBefore
		}
		writeJSON(w, 200, memPullResponse{Enabled: true, Reset: true, ResetReason: reason, ErasedBefore: erased, Records: []memPullRecord{}})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	dev := q.Get("device_id")
	var rows []*fakeMemRec
	for _, rec := range f.recs {
		if rec.seq > cu.seq {
			rows = append(rows, rec)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].seq < rows[j].seq })
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	out := memPullResponse{Enabled: true, Records: []memPullRecord{}, Cursor: raw, HasMore: more}
	var last int64
	for _, rec := range rows {
		last = rec.seq
		out.Cursor = strconv.FormatInt(rec.seq, 10)
		if rec.state == "tombstone" {
			out.Records = append(out.Records, memPullRecord{ID: rec.id, Seq: out.Cursor, State: "tombstone", Reason: rec.reason, SupersededBy: rec.supersededBy})
			continue
		}
		out.Records = append(out.Records, f.liveRecordLocked(rec, dev))
	}
	if !cu.given { // snapshot: from "" or an s-cursor
		if more {
			out.Cursor = fmt.Sprintf("s%d.%d.%d", last, f.floor, f.erasedAt)
		} else {
			final := max(last, f.floor, cu.seq)
			if final == 0 {
				out.Cursor = raw
			} else {
				out.Cursor = strconv.FormatInt(final, 10)
			}
		}
	}
	writeJSON(w, 200, out)
}

func (f *fakeMemoryFleet) liveRecordLocked(rec *fakeMemRec, dev string) memPullRecord {
	var total int64
	for _, v := range rec.recall {
		total += v
	}
	var al []string
	for a, c := range f.aliases {
		if c == rec.id {
			al = append(al, a)
		}
	}
	others := total
	if dev != "" {
		others = total - rec.recall[dev]
	}
	return memPullRecord{ID: rec.id, Seq: strconv.FormatInt(rec.seq, 10), State: "live", Content: rec.content,
		ContentHash: rec.hash, Kind: rec.kind, RetrievalWeight: rec.weight, TurnID: rec.turnID, Source: rec.source,
		SourceTurn: rec.sourceTurn, ToolName: rec.toolName, FilesRead: rec.filesRead, FilesModified: rec.filesModified,
		CreatedAt: rec.createdAt, Title: rec.title, TitleHLC: rec.titleHLC, Pinned: rec.pinned, PinnedHLC: rec.pinnedHLC,
		ScopeKind: rec.scope, ScopeHLC: rec.scopeHLC, RecallCount: total, RecallCountOthers: others,
		LastAccessed: rec.lastAccessed, Aliases: al}
}

func (f *fakeMemoryFleet) bump(rec *fakeMemRec) { f.seq++; rec.seq = f.seq }

func rejectedRes(id, code, field string, retry bool) memPushResult {
	return memPushResult{ID: id, Status: "rejected", Code: code, Field: field, Retry: &retry}
}

func (f *fakeMemoryFleet) pushLocked(w http.ResponseWriter, req memPushRequest) {
	resp := memPushResponse{Results: []memPushResult{}, CursorFloor: strconv.FormatInt(f.floor, 10)}
	blanket := ""
	if !f.enabled {
		blanket = "sync_disabled"
	} else if f.staleLocked(parseFakeCursor(req.BaseCursor)) != "" {
		blanket = "resync_required"
	}
	for _, it := range req.Items {
		if blanket != "" && it.Op != "forget" {
			resp.Results = append(resp.Results, rejectedRes(it.ID, blanket, "", true))
			continue
		}
		resp.Results = append(resp.Results, f.itemLocked(req.DeviceID, it))
	}
	writeJSON(w, 200, resp)
}

func (f *fakeMemoryFleet) tomb(rec *fakeMemRec, reason, by string) {
	rec.state, rec.reason, rec.supersededBy = "tombstone", reason, by
	rec.content, rec.title = "", ""
	f.bump(rec)
}

func hlcFuture(h string, now time.Time) bool {
	w, _, _, ok := memory.ParseHLC(h)
	return ok && time.UnixMilli(w).After(now.Add(memory.HLCMaxSkew))
}

func (f *fakeMemoryFleet) itemLocked(dev string, it memPushItem) memPushResult {
	target := it.ID
	if c, ok := f.aliases[it.ID]; ok {
		target = c
	}
	if it.Op == "forget" {
		rec := f.recs[target]
		if rec == nil {
			return memPushResult{ID: it.ID, Status: "forgotten"} // never seen: no row
		}
		if !(rec.state == "tombstone" && rec.reason == "forgotten") {
			f.tomb(rec, "forgotten", "")
		}
		return memPushResult{ID: it.ID, Status: "forgotten", Seq: strconv.FormatInt(rec.seq, 10)}
	}
	for name, fv := range it.Fields {
		if hlcFuture(fv.HLC, f.now()) {
			return rejectedRes(it.ID, "clock_in_future", name, false)
		}
	}
	if it.Content != nil && strings.Contains(*it.Content, "sk-ant-") {
		return rejectedRes(it.ID, "secret_detected", "content", false)
	}
	scope, scopeHLC := "", ""
	if sv, ok := it.Fields["scope_kind"]; ok {
		scope, scopeHLC = sv.V.(string), sv.HLC
	}
	if scope == "project" {
		return rejectedRes(it.ID, "scope_not_supported_yet", "scope_kind", false)
	}
	rec := f.recs[target]
	if rec != nil && rec.state == "tombstone" {
		switch rec.reason {
		case "forgotten":
			return memPushResult{ID: it.ID, Status: "forgotten"}
		case "superseded":
			return memPushResult{ID: it.ID, Status: "superseded", SupersededBy: rec.supersededBy}
		}
		if scope != "" && scopeHLC > rec.scopeHLC && f.scopes[scope] {
			if it.Content == nil {
				return rejectedRes(it.ID, "invalid_field", "content", false)
			}
			delete(f.recs, target)
			return f.createLocked(dev, it, scope, scopeHLC)
		}
		return memPushResult{ID: it.ID, Status: "unchanged"}
	}
	if rec != nil {
		if it.ContentHash != "" && it.ContentHash != rec.hash {
			return rejectedRes(it.ID, "content_mismatch", "content", false)
		}
		changed := false
		if fv, ok := it.Fields["title"]; ok && fv.HLC > rec.titleHLC {
			rec.title, rec.titleHLC, changed = fv.V.(string), fv.HLC, true
		}
		if fv, ok := it.Fields["pinned"]; ok && fv.HLC > rec.pinnedHLC {
			rec.pinned, rec.pinnedHLC, changed = fv.V.(bool), fv.HLC, true
		}
		scopeChanged := false
		if scope != "" && scopeHLC > rec.scopeHLC {
			rec.scope, rec.scopeHLC, changed, scopeChanged = scope, scopeHLC, true, true
		}
		if it.LastAccessed != "" && it.LastAccessed > rec.lastAccessed {
			rec.lastAccessed, changed = it.LastAccessed, true
		}
		if it.RecallOwn != nil && *it.RecallOwn > rec.recall[dev] {
			rec.recall[dev], changed = *it.RecallOwn, true
		}
		if scopeChanged && !f.scopes[rec.scope] {
			f.tomb(rec, "left_sync_scope", "")
			return memPushResult{ID: it.ID, Status: "accepted", Seq: strconv.FormatInt(rec.seq, 10)}
		}
		if target != it.ID {
			if changed {
				f.bump(rec)
			}
			return memPushResult{ID: it.ID, Status: "merged", CanonicalID: target, Seq: strconv.FormatInt(rec.seq, 10)}
		}
		if !changed {
			return memPushResult{ID: it.ID, Status: "unchanged"}
		}
		f.bump(rec)
		return memPushResult{ID: it.ID, Status: "accepted", Seq: strconv.FormatInt(rec.seq, 10)}
	}
	if it.Content == nil {
		return rejectedRes(it.ID, "invalid_field", "content", false)
	}
	if scope == "" {
		return rejectedRes(it.ID, "invalid_field", "fields.scope_kind", false)
	}
	return f.createLocked(dev, it, scope, scopeHLC)
}

func kindRankFake(k string) int {
	switch k {
	case "narrative_synthesised":
		return 2
	case "narrative_extractive_fallback":
		return 1
	}
	return 0
}

func (f *fakeMemoryFleet) createLocked(dev string, it memPushItem, scope, scopeHLC string) memPushResult {
	if !f.scopes[scope] {
		return rejectedRes(it.ID, "scope_not_enabled", "scope_kind", false)
	}
	hash := memory.HashContent(*it.Content)
	for _, rec := range f.recs {
		if rec.state == "live" && rec.hash == hash && rec.kind == it.Kind && rec.id != it.ID {
			f.aliases[it.ID] = rec.id
			if it.RecallOwn != nil && *it.RecallOwn > rec.recall[dev] {
				rec.recall[dev] = *it.RecallOwn
			}
			f.bump(rec)
			return memPushResult{ID: it.ID, Status: "merged", CanonicalID: rec.id, Seq: strconv.FormatInt(rec.seq, 10)}
		}
	}
	rec := &fakeMemRec{id: it.ID, state: "live", content: *it.Content, hash: hash, kind: it.Kind, turnID: it.TurnID,
		source: it.Source, sourceTurn: it.SourceTurn, toolName: it.ToolName, filesRead: it.FilesRead,
		filesModified: it.FilesModified, createdAt: it.CreatedAt, lastAccessed: it.LastAccessed,
		scope: scope, scopeHLC: scopeHLC, weight: 1, recall: map[string]int64{}}
	if it.RetrievalWeight != nil {
		rec.weight = *it.RetrievalWeight
	}
	if fv, ok := it.Fields["title"]; ok {
		rec.title, rec.titleHLC = fv.V.(string), fv.HLC
	}
	if fv, ok := it.Fields["pinned"]; ok {
		rec.pinned, rec.pinnedHLC = fv.V.(bool), fv.HLC
	}
	if it.RecallOwn != nil {
		rec.recall[dev] = *it.RecallOwn
	}
	if rk := kindRankFake(it.Kind); rk > 0 && it.TurnID != "" {
		for _, l := range f.recs {
			if l.state == "live" && l.turnID == it.TurnID && kindRankFake(l.kind) > 0 {
				if rk > kindRankFake(l.kind) {
					f.tomb(l, "superseded", it.ID)
				} else {
					rec.state, rec.reason, rec.supersededBy, rec.content = "tombstone", "superseded", l.id, ""
					f.recs[rec.id] = rec
					f.bump(rec)
					return memPushResult{ID: it.ID, Status: "superseded", SupersededBy: l.id, Seq: strconv.FormatInt(rec.seq, 10)}
				}
			}
		}
	}
	f.recs[rec.id] = rec
	f.bump(rec)
	return memPushResult{ID: it.ID, Status: "accepted", Seq: strconv.FormatInt(rec.seq, 10)}
}
