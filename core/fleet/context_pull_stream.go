package fleet

// context_pull_stream.go — the parts of GET /api/v1/context/pull both
// context lanes share (kenaz-fleet spec 018 H-2, docs/contract-context.md §2).
//
//   - The opaque cursor: every response carries `next_cursor`, bound to the
//     filter set and the member. It is sent back as `cursor=`. A server
//     that sends no `next_cursor` (older Fleet, test fakes) still gets the
//     legacy `since=` built from the response's `cursor`, so both lanes keep
//     working against either.
//   - Cursor refusals: `400 cursor_filter_mismatch` and `400 invalid_cursor`.
//     The lane drops its cursor and starts one clean full pull; a pull with
//     no cursor can never be refused for its cursor, so this cannot loop.
//   - Loss of access: `reset: true` with `reset_reason` and
//     `reset_team_ids`. The lane drops the copies of the listed teams, then
//     re-snapshots from the start of the stream (no cursor) and, once the
//     snapshot is complete, drops every remaining copy the snapshot did not
//     serve. That is "drop everything and re-snapshot" without deleting
//     content that is still readable in between. A pull without a cursor
//     never resets, so a reset cannot loop either (Fleet also caps it at two
//     consecutive resets per cause per device).
//
// Spec 018 rule: nothing here puts a team on telemetry. Team ids appear in
// local state only; logs carry counts and the reset reason.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Audience values Fleet serves on pull (spec 018 FR-6) and takes on push.
const (
	AudienceTeam     = "team"
	AudienceOrg      = "org"
	AudiencePersonal = "personal"
)

// Cursor kinds a lane may hold.
const (
	// pullCursorOpaque is a Fleet `next_cursor`, sent as `cursor=`.
	pullCursorOpaque = "opaque"
	// pullCursorSince is a legacy RFC3339Nano `cursor`, sent as `since=`.
	pullCursorSince = "since"
)

// pullCursor is one lane's position in its pull stream.
type pullCursor struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (c pullCursor) empty() bool { return c.Value == "" }

// query returns the cursor's query parameter ("" when empty).
func (c pullCursor) query() string {
	if c.Value == "" {
		return ""
	}
	if c.Kind == pullCursorOpaque {
		return "cursor=" + url.QueryEscape(c.Value)
	}
	return "since=" + url.QueryEscape(c.Value)
}

// laneStreamPath builds a pull path for a lane's kinds at cursor c.
func laneStreamPath(kinds []string, c pullCursor) string {
	return laneStreamPathLimit(kinds, c, pullPageSize)
}

// laneStreamPathLimit is laneStreamPath with an explicit page size. limit
// is always sent: a page shorter than it ends a full pull (and lets a
// reset sweep run), so Fleet's default must never decide that. limit is
// not part of the cursor's filter set.
func laneStreamPathLimit(kinds []string, c pullCursor, limit int) string {
	if limit <= 0 {
		limit = pullPageSize
	}
	p := "/api/v1/context/pull?" + laneKindQuery(kinds) + "&limit=" + strconv.Itoa(limit)
	if q := c.query(); q != "" {
		p += "&" + q
	}
	return p
}

// Reset reasons (contract §2). Anything else Fleet sends is kept as served.
const (
	ResetLeftTeam        = "left_team"
	ResetTeamReparented  = "team_reparented"
	ResetAncestorDeleted = "ancestor_deleted"
	ResetInheritanceOff  = "inheritance_off"
	// resetCursorRefused is the local reason for a cursor refusal
	// (cursor_filter_mismatch / invalid_cursor).
	resetCursorRefused = "cursor_refused"
	// resetAccessEpoch is the local reason for a changed access_epoch on a
	// legacy since-stream (Fleet sends reset only to cursor clients).
	resetAccessEpoch = "access_epoch_changed"
)

// Cursor refusal codes (400).
const (
	codeCursorFilterMismatch = "cursor_filter_mismatch"
	codeInvalidCursor        = "invalid_cursor"
)

// isCursorRefusal reads a non-200 pull body (bounded) and reports whether
// it is a cursor refusal.
func isCursorRefusal(resp *http.Response) bool {
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var env struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(body, &env) != nil {
		return false
	}
	return env.Code == codeCursorFilterMismatch || env.Code == codeInvalidCursor
}

// nextCursorFrom returns the cursor to store after a page. An opaque
// `next_cursor` wins; else the legacy `cursor` (empty = keep prev).
func nextCursorFrom(r contextPullResponse, prev pullCursor) pullCursor {
	if r.NextCursor != "" {
		return pullCursor{Kind: pullCursorOpaque, Value: r.NextCursor}
	}
	if r.Cursor != "" {
		return pullCursor{Kind: pullCursorSince, Value: r.Cursor}
	}
	return prev
}

// pullPageSize is Fleet's default page size (the harness sends no limit).
// A page shorter than this ends a full pull.
const pullPageSize = curatedFullPullPage

// maxSnapshotPages bounds one in-call re-snapshot (200 × 1000 nodes).
const maxSnapshotPages = 200

// teamSet makes a lookup set of team ids (nil for none).
func teamSet(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	return out
}
