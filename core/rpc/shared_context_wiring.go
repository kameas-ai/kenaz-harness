package rpc

// shared_context_wiring.go — teammates' shared context, made usable by the
// model and framed as data.
//
// Context pulled from Kameas Fleet that someone else wrote (Knowledge ›
// Curated entries pulled by the ContextGraphSyncer; team and org units
// pulled by the UnitSyncer) reaches the model on two paths, both rendered by
// core/contexts/sharedctx (trust notice, per-item labels, indented content):
//
//  1. On demand: kenaz__read_context_file's "shared" argument lists and
//     reads them (sharedContextSource implements readcontextfile.
//     SharedSource). Read-only; refused unless signed in to Fleet.
//  2. Eagerly: team / org units whose sync sidecar records that Fleet holds
//     load_policy "always" (units.SyncState.ServerLoadPolicy, refreshed on
//     every pull and push) are prepended to the session's system
//     attachments (alwaysBlock, via both attachment resolver adapters).
//     Fleet lets only org admins / owners set "always" (403
//     load_policy_requires_admin; the mapper sends everyone else's as
//     on_demand, and the sidecar records that). The local unit's LoadPolicy
//     never qualifies on its own, so a demotion on Fleet, a non-admin's own
//     unit, or a unit never synced stays on demand. Personal units are
//     untouched.
//
// Pulled entries are never attachments (core/attachments refuses
// "<team|org>/_fleet" sources): an attachment would be unframed.
//
// The curated source is late-bound: the ContextGraphSyncer is built in the
// fleet wiring block, after newLLMStack registers the tool.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/contexts/sharedctx"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// Shared item id prefixes (one per lane).
const (
	sharedEntryPrefix = "entry:"
	sharedUnitPrefix  = "unit:"
)

// Eager-block caps: the always-set rides on every turn's prompt.
const (
	maxAlwaysItems     = 16
	maxAlwaysItemBytes = 8 << 10
	maxAlwaysBytes     = 32 << 10
)

// alwaysSubtitle follows the header on the eager block.
const alwaysSubtitle = "loaded for every conversation because it is marked always-load in Kameas Fleet, which only org admins can do; " +
	"list and read more with kenaz__read_context_file shared=\"list\""

// sharedContextSource serves teammates' shared context to the model.
// Safe for concurrent use.
type sharedContextSource struct {
	// signedIn reports a live Fleet session. nil = never signed in.
	signedIn func(ctx context.Context) bool

	mu sync.RWMutex
	// curated returns the pulled Curated entries (tombstones excluded).
	curated func() []corefleet.ContextNodeEntry
	// selfUserID returns the enrolled user id ("" when unknown).
	selfUserID func() string
	// units is the local Unit store holding pulled team / org units.
	units *units.Manager
}

// setCurated wires the Curated lane (late: the syncer is built after the
// LLM stack).
func (s *sharedContextSource) setCurated(curated func() []corefleet.ContextNodeEntry, selfUserID func() string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.curated, s.selfUserID = curated, selfUserID
	s.mu.Unlock()
}

// setUnits wires the Unit store.
func (s *sharedContextSource) setUnits(m *units.Manager) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.units = m
	s.mu.Unlock()
}

func (s *sharedContextSource) snapshot() (func() []corefleet.ContextNodeEntry, func() string, *units.Manager) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.curated, s.selfUserID, s.units
}

// SignedIn implements readcontextfile.SharedSource.
func (s *sharedContextSource) SignedIn(ctx context.Context) bool {
	return s != nil && s.signedIn != nil && s.signedIn(ctx)
}

// ListShared implements readcontextfile.SharedSource: Curated entries
// first, then team and org units. Bodies are included.
func (s *sharedContextSource) ListShared(ctx context.Context) ([]sharedctx.Item, error) {
	if s == nil {
		return nil, nil
	}
	curated, self, mgr := s.snapshot()
	var out []sharedctx.Item
	if curated != nil {
		selfID := ""
		if self != nil {
			selfID = self()
		}
		for _, e := range curated() {
			if e.DeletedAt != nil {
				continue
			}
			out = append(out, curatedItem(e, selfID))
		}
	}
	if mgr != nil {
		shared, err := sharedUnits(ctx, mgr, "")
		if err != nil {
			return out, err
		}
		for _, u := range shared {
			out = append(out, unitItem(u))
		}
	}
	return out, nil
}

// GetShared implements readcontextfile.SharedSource.
func (s *sharedContextSource) GetShared(ctx context.Context, id string) (sharedctx.Item, bool, error) {
	if s == nil {
		return sharedctx.Item{}, false, nil
	}
	curated, self, mgr := s.snapshot()
	switch {
	case strings.HasPrefix(id, sharedEntryPrefix):
		if curated == nil {
			return sharedctx.Item{}, false, nil
		}
		want := strings.TrimPrefix(id, sharedEntryPrefix)
		selfID := ""
		if self != nil {
			selfID = self()
		}
		for _, e := range curated() {
			if e.ID == want && e.DeletedAt == nil {
				return curatedItem(e, selfID), true, nil
			}
		}
		return sharedctx.Item{}, false, nil
	case strings.HasPrefix(id, sharedUnitPrefix):
		if mgr == nil {
			return sharedctx.Item{}, false, nil
		}
		u, err := mgr.Get(ctx, strings.TrimPrefix(id, sharedUnitPrefix))
		if err != nil {
			if errors.Is(err, units.ErrUnitNotFound) {
				return sharedctx.Item{}, false, nil
			}
			return sharedctx.Item{}, false, err
		}
		// Only shared units are reachable here: a personal unit or an
		// artifact id resolves as "not found", never as content.
		if !isSharedUnit(u) {
			return sharedctx.Item{}, false, nil
		}
		return unitItem(u), true, nil
	}
	return sharedctx.Item{}, false, nil
}

// alwaysBlock renders the eager always-load block for a session, or "" when
// there is nothing to load, the user is signed out, or anything fails (the
// turn must never fail on this layer). projectID may be nil.
//
// A unit qualifies ONLY by its sync sidecar's ServerLoadPolicy == "always":
// the load_policy Fleet holds for the node as last pulled or pushed. Fleet
// lets only org admins / owners set it, and a demotion (or an edit pushed
// by a non-admin, which goes out as on_demand) is recorded on the next pull
// or push. The local unit's LoadPolicy never qualifies on its own.
func (s *sharedContextSource) alwaysBlock(ctx context.Context, sessionID string, projectID *string) string {
	if s == nil {
		return ""
	}
	_, _, mgr := s.snapshot()
	if mgr == nil {
		return ""
	}
	states, err := mgr.ListSyncState(ctx)
	if err != nil {
		logging.L().Warn("rpc.shared_context.always_list_failed", "err", err.Error())
		return ""
	}
	var cands []units.Unit
	for _, st := range states {
		if st.ServerLoadPolicy != string(units.LoadAlways) {
			continue
		}
		u, gerr := mgr.Get(ctx, st.UnitID)
		if gerr != nil || !isSharedUnit(u) || !scopeMatches(u, sessionID, projectID) {
			continue
		}
		cands = append(cands, u)
	}
	if len(cands) == 0 {
		return ""
	}
	// Checked after the cheap SQL reads: the sign-in check reads the keychain.
	if !s.SignedIn(ctx) {
		return ""
	}
	// Org (base layer) before team, then global → project → session, then
	// oldest first: stable across turns.
	sort.SliceStable(cands, func(i, j int) bool {
		if a, b := sharedClassRank(cands[i].Classification), sharedClassRank(cands[j].Classification); a != b {
			return a < b
		}
		if a, b := sharedScopeRank(cands[i].Scope), sharedScopeRank(cands[j].Scope); a != b {
			return a < b
		}
		if !cands[i].CreatedAt.Equal(cands[j].CreatedAt) {
			return cands[i].CreatedAt.Before(cands[j].CreatedAt)
		}
		return cands[i].ID < cands[j].ID
	})
	return formatAlways(cands)
}

// formatAlways applies the eager caps (maxAlwaysItems items, maxAlwaysBytes
// total, maxAlwaysItemBytes per item; a cut item is marked truncated) and
// renders the framed block.
func formatAlways(cands []units.Unit) string {
	var items []sharedctx.Item
	total := 0
	for _, u := range cands {
		if len(items) >= maxAlwaysItems || total >= maxAlwaysBytes {
			break
		}
		it := unitItem(u)
		limit := maxAlwaysItemBytes
		if rest := maxAlwaysBytes - total; rest < limit {
			limit = rest
		}
		if len(it.Body) > limit {
			it.Body = truncateUTF8(it.Body, limit)
			it.Truncated = true
		}
		total += len(it.Body)
		items = append(items, it)
	}
	return sharedctx.Format(alwaysSubtitle, items)
}

func sharedClassRank(c units.Classification) int {
	if c == units.ClassOrg {
		return 0
	}
	return 1
}

func sharedScopeRank(sc units.Scope) int {
	switch sc {
	case units.ScopeGlobal:
		return 0
	case units.ScopeProject:
		return 1
	}
	return 2
}

// scopeMatches reports whether a unit's scope applies to the session.
func scopeMatches(u units.Unit, sessionID string, projectID *string) bool {
	switch u.Scope {
	case units.ScopeGlobal:
		return true
	case units.ScopeProject:
		return projectID != nil && *projectID != "" && u.ScopeID == *projectID
	case units.ScopeSession:
		return sessionID != "" && u.ScopeID == sessionID
	}
	return false
}

// sharedUnits lists org then team units (optionally one load policy),
// artifacts excluded.
func sharedUnits(ctx context.Context, mgr *units.Manager, lp units.LoadPolicy) ([]units.Unit, error) {
	var out []units.Unit
	for _, c := range []units.Classification{units.ClassOrg, units.ClassTeam} {
		rows, err := mgr.List(ctx, units.UnitFilter{Classification: c, LoadPolicy: lp})
		if err != nil {
			return nil, err
		}
		for _, u := range rows {
			if isSharedUnit(u) {
				out = append(out, u)
			}
		}
	}
	return out, nil
}

// isSharedUnit: a team or org unit that is not a device-local artifact.
func isSharedUnit(u units.Unit) bool {
	if u.Kind == units.KindArtifact {
		return false
	}
	return u.Classification == units.ClassTeam || u.Classification == units.ClassOrg
}

// curatedItem maps a pulled Curated entry onto a shared item.
func curatedItem(e corefleet.ContextNodeEntry, selfID string) sharedctx.Item {
	author := sharedctx.AuthorTeammate
	switch {
	case e.OwnerUserID == "":
		author = sharedctx.AuthorNotRecorded
	case selfID != "" && strings.EqualFold(e.OwnerUserID, selfID):
		author = sharedctx.AuthorYou
	}
	return sharedctx.Item{
		ID:       sharedEntryPrefix + e.ID,
		Title:    e.Title,
		Kind:     corefleet.DisplayKind(e.Kind),
		Audience: string(e.Layer),
		Author:   author,
		Body:     e.Body,
	}
}

// unitItem maps a team / org unit onto a shared item. The local store
// keeps no author for units, so it says so. The kind label is the wire
// kind kept for a bootstrap / legacy node (stored as a doc), else the
// unit's own kind.
func unitItem(u units.Unit) sharedctx.Item {
	kind := string(u.Kind)
	if len(u.Metadata) > 0 {
		var md map[string]json.RawMessage
		if json.Unmarshal(u.Metadata, &md) == nil {
			var fk string
			if raw, ok := md[corefleet.FleetKindMetaKey]; ok && json.Unmarshal(raw, &fk) == nil && fk != "" {
				kind = fk
			}
		}
	}
	return sharedctx.Item{
		ID:       sharedUnitPrefix + u.ID,
		Title:    u.Title,
		Kind:     corefleet.DisplayKind(kind),
		Audience: string(u.Classification),
		Author:   sharedctx.AuthorNotRecorded,
		Body:     u.Body,
	}
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// sharedAlwaysBlock resolves the session's project and renders the
// always-load block (both attachment resolver adapters share it).
func sharedAlwaysBlock(ctx context.Context, s *sharedContextSource, reader interface {
	ProjectID(ctx context.Context, sessionID string) (*string, error)
}, sessionID string) string {
	if s == nil {
		return ""
	}
	var projectID *string
	if reader != nil {
		if p, err := reader.ProjectID(ctx, sessionID); err == nil {
			projectID = p
		}
	}
	return s.alwaysBlock(ctx, sessionID, projectID)
}
