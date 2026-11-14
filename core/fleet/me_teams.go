package fleet

// me_teams.go — the caller's teams from GET /api/v1/me, as a tree
// (kenaz-fleet spec 018, harness package H-1).
//
// Fleet's /me `teams[]` lists only the caller's own memberships, default
// team first. Spec 018 FR-2 adds the stored team tree to each entry:
//
//	is_default      the org's default team, which is the root of the tree
//	role            the caller's role on the team ("member" | "lead")
//	parent_team_id  the stored parent, or null for a team directly under
//	                the root (the root itself also has null)
//	depth           0 for the root, 1 for a team directly under it
//	path            ancestor ids, topmost first, without the team itself
//	                and without the root; the caller need not be a member
//	slug, membership  announced, not yet served — decoded when present
//
// Every field is additive. An older Fleet that sends only {id, name}
// decodes to a flat list in served order, which is exactly today's view.
//
// Teams are keyed by id. Name and slug are labels: a rename or re-parent
// never changes the id, and two teams may display alike.
//
// Spec 018 rule: a team never goes onto any telemetry event or resource
// attribute. Nothing here feeds telemetry.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// MeTeam is one entry of GET /api/v1/me `teams[]`.
type MeTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// IsDefault marks the org's default team: the root of the tree.
	IsDefault bool `json:"is_default,omitempty"`
	// Role is the caller's role on the team ("member", "lead"); empty on
	// an older Fleet.
	Role string `json:"role,omitempty"`
	// ParentTeamID is the stored parent; nil for the root, for a team
	// directly under the root, and on an older Fleet.
	ParentTeamID *string `json:"parent_team_id,omitempty"`
	// Depth is Fleet's depth (root 0, top-level 1); nil on an older Fleet.
	Depth *int `json:"depth,omitempty"`
	// Path lists ancestor ids topmost first, excluding the team and the
	// root. Ancestors may be teams the caller is not a member of.
	Path []string `json:"path,omitempty"`
	// Slug is a stable filesystem-safe label (spec 018 D-5). Not served yet.
	Slug string `json:"slug,omitempty"`
	// Membership is "direct" or "inherited" (spec 018 model). Not served yet.
	Membership string `json:"membership,omitempty"`
}

// meTeamsResponse is the slice of GET /api/v1/me this file reads. The rest
// of the body is ignored.
type meTeamsResponse struct {
	Teams []MeTeam `json:"teams"`
}

// DecodeMeTeams decodes the `teams` array of a GET /api/v1/me body. An
// absent or null `teams` decodes to an empty (non-nil) slice: Fleet omits
// it for a caller in no team.
func DecodeMeTeams(r io.Reader) ([]MeTeam, error) {
	var body meTeamsResponse
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, fmt.Errorf("fleet/me: decode: %w", err)
	}
	if body.Teams == nil {
		return []MeTeam{}, nil
	}
	return body.Teams, nil
}

// GetMeTeams reads the caller's teams from GET /api/v1/me. The list is as
// served; use OrderTeamTree to lay it out.
//
// Returns ErrFleetDisabled on a nop client and ErrNotSignedIn on 401.
func (c *Client) GetMeTeams(ctx context.Context) ([]MeTeam, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me")
	if err != nil {
		return nil, fmt.Errorf("fleet/me: get: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrNotSignedIn
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fleet/me: get status %d", resp.StatusCode)
	}
	return DecodeMeTeams(resp.Body)
}

// TeamTreeEntry is one team placed in the displayed tree.
type TeamTreeEntry struct {
	MeTeam
	// TreeParentID is the id this entry is drawn under: the stored parent
	// when that team is in the list, otherwise the root's id, or "" for the
	// root itself and for the top level of a list with no root.
	TreeParentID string
	// TreeDepth is the indent level as drawn: 0 for the root, 1 for a team
	// drawn directly under it, parent+1 below that. In a list with no root
	// (an older Fleet, or a caller not in the default team) top-level teams
	// are 0.
	TreeDepth int
}

// OrderTeamTree lays the caller's teams out as a tree, parent before
// children, keyed by id:
//
//   - The default team (is_default) is the root and comes first. If
//     several entries claim it, the first wins and the rest are ordinary
//     teams.
//   - A team is drawn under its stored parent only when that parent is in
//     the list (the caller need not be a member of their team's ancestors;
//     such a team is drawn directly under the root). A parent naming the
//     team itself or the root is treated as "directly under the root".
//   - Siblings keep served order.
//   - Cycles cannot hang or drop anything: a parent cycle is cut at its
//     member that comes first in served order, which is drawn as a
//     top-level team with the rest of the cycle (and anything hanging off
//     it) below.
//   - Entries with an empty id are dropped; a repeated id keeps its first
//     occurrence.
//
// A list with no tree fields at all (an older Fleet) comes back flat, in
// served order, every TreeDepth 0.
func OrderTeamTree(teams []MeTeam) []TeamTreeEntry {
	// Dedupe by id, keeping served order.
	seen := make(map[string]bool, len(teams))
	list := make([]MeTeam, 0, len(teams))
	for _, t := range teams {
		if t.ID == "" || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		list = append(list, t)
	}

	rootID := ""
	for _, t := range list {
		if t.IsDefault {
			rootID = t.ID
			break
		}
	}

	// Resolved parent per team: in the list, not itself, not the root.
	parent := make(map[string]string, len(list))
	for _, t := range list {
		if t.ID == rootID || t.ParentTeamID == nil {
			continue
		}
		p := *t.ParentTeamID
		if p == "" || p == t.ID || p == rootID || !seen[p] {
			continue
		}
		parent[t.ID] = p
	}

	children := make(map[string][]string, len(list))
	byID := make(map[string]MeTeam, len(list))
	for _, t := range list {
		byID[t.ID] = t
		if p, ok := parent[t.ID]; ok {
			children[p] = append(children[p], t.ID)
		}
	}

	out := make([]TeamTreeEntry, 0, len(list))
	placed := make(map[string]bool, len(list))
	// walk places id and its subtree depth-first. The placed set stops a
	// cycle from recursing forever: a child already placed is skipped.
	var walk func(id, treeParent string, depth int)
	walk = func(id, treeParent string, depth int) {
		if placed[id] {
			return
		}
		placed[id] = true
		out = append(out, TeamTreeEntry{MeTeam: byID[id], TreeParentID: treeParent, TreeDepth: depth})
		for _, c := range children[id] {
			walk(c, id, depth+1)
		}
	}

	top := 0
	if rootID != "" {
		walk(rootID, "", 0)
		top = 1
	}
	// Top-level teams: no resolved parent, in served order.
	for _, t := range list {
		if t.ID == rootID {
			continue
		}
		if _, has := parent[t.ID]; has {
			continue
		}
		walk(t.ID, rootID, top)
	}
	// Anything still unplaced sits on, or hangs below, a parent cycle with
	// no way in from the top. Cut each cycle at its member that comes first
	// in served order and draw it at the top level; the rest of the cycle
	// and anything hanging off it then fall below it.
	order := make(map[string]int, len(list))
	for i, t := range list {
		order[t.ID] = i
	}
	for _, t := range list {
		if placed[t.ID] {
			continue
		}
		walk(cycleEntry(t.ID, parent, order), rootID, top)
	}
	return out
}

// cycleEntry follows id's resolved parents until one repeats and returns
// the member of that cycle that comes first in served order. Only called
// for an unplaced team, every one of which reaches a cycle: a chain that
// ended at a top-level team would have been placed from the top.
func cycleEntry(id string, parent map[string]string, order map[string]int) string {
	visited := make(map[string]bool)
	cur := id
	for !visited[cur] {
		visited[cur] = true
		p, ok := parent[cur]
		if !ok {
			return cur // defensive: no parent means no cycle; draw it as is
		}
		cur = p
	}
	// cur is on the cycle; walk it once to pick the earliest-served member.
	best := cur
	for n := parent[cur]; n != cur; n = parent[n] {
		if order[n] < order[best] {
			best = n
		}
	}
	return best
}
