package toolexposure

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// OrgPolicy is the organisation's tool-exposure layer, decoded from the
// last verified, applied config bundle (spec §2.1 step 1, FR-K4).
//
//   - Pins hold the entries the bundle marks pinned:true. They win over
//     every other layer and no writer may change them.
//   - Defaults hold the entries marked pinned:false. They sit below the
//     user's settings and above the harness default: every lower-numbered
//     layer (session, project, user) may override them.
//   - SchemaBudgetTokens, when > 0, replaces the user's schema budget.
//   - HotSetExtra names ("<server>__<tool>") resolve full whatever any
//     layer, pin included, says: they join the hot set and cannot be
//     lowered.
//
// The zero value pins nothing.
type OrgPolicy struct {
	Pins               Exposure
	Defaults           Exposure
	SchemaBudgetTokens int
	HotSetExtra        []string
	// BundleID is the bundle the policy came from (0 when none).
	BundleID int64
}

// IsZero reports whether the policy has no opinion about anything.
func (p OrgPolicy) IsZero() bool {
	return p.Pins.IsZero() && p.Defaults.IsZero() && p.SchemaBudgetTokens <= 0 && len(p.HotSetExtra) == 0
}

// inHotSetExtra reports whether name is one of the policy's extra hot
// tools.
func (p OrgPolicy) inHotSetExtra(name string) bool {
	for _, n := range p.HotSetExtra {
		if n == name {
			return true
		}
	}
	return false
}

// PinnedByOrg is the value surfaces show for a pinned entry's origin.
const PinnedByOrg = "org"

// OrgSetting is one organisation entry as surfaces render it. Tool is the
// bare tool name; empty means the server-wide tier. Pinned entries are
// read-only ("set by your organisation"); unpinned ones are org defaults
// the user may override.
type OrgSetting struct {
	Server   string `json:"server"`
	Tool     string `json:"tool,omitempty"`
	Tier     Tier   `json:"tier"`
	Pinned   bool   `json:"pinned"`
	PinnedBy string `json:"pinnedBy,omitempty"`
}

// OrgExposure is the read-only projection of an OrgPolicy carried on the
// settings and session exposure wire types. Settings is sorted by server,
// then tool (server-wide first); hot_set_extra names appear as pinned
// full tool entries. SchemaBudgetTokens is 0 when the budget is not
// pinned.
type OrgExposure struct {
	Settings           []OrgSetting `json:"settings"`
	SchemaBudgetTokens int          `json:"schemaBudgetTokens"`
	BundleID           int64        `json:"bundleId"`
}

// View projects the policy for surfaces.
func (p OrgPolicy) View() OrgExposure {
	out := OrgExposure{Settings: []OrgSetting{}, SchemaBudgetTokens: p.SchemaBudgetTokens, BundleID: p.BundleID}
	if out.SchemaBudgetTokens < 0 {
		out.SchemaBudgetTokens = 0
	}
	add := func(e Exposure, pinned bool) {
		for server, s := range e.Servers {
			by := ""
			if pinned {
				by = PinnedByOrg
			}
			if s.Tier.Valid() {
				out.Settings = append(out.Settings, OrgSetting{Server: server, Tier: s.Tier, Pinned: pinned, PinnedBy: by})
			}
			for tool, t := range s.Tools {
				if t.Valid() {
					out.Settings = append(out.Settings, OrgSetting{Server: server, Tool: tool, Tier: t, Pinned: pinned, PinnedBy: by})
				}
			}
		}
	}
	add(p.Pins, true)
	add(p.Defaults, false)
	for _, n := range p.HotSetExtra {
		server, tool, ok := SplitName(n)
		if !ok {
			continue
		}
		out.Settings = append(out.Settings, OrgSetting{Server: server, Tool: tool, Tier: TierFull, Pinned: true, PinnedBy: PinnedByOrg})
	}
	sort.SliceStable(out.Settings, func(i, j int) bool {
		a, b := out.Settings[i], out.Settings[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.Tool != b.Tool {
			return a.Tool < b.Tool
		}
		return a.Pinned && !b.Pinned
	})
	return out
}

// SplitName splits a namespaced "<server>__<tool>" name. ok is false when
// either half is empty or the separator is missing.
func SplitName(name string) (server, tool string, ok bool) {
	i := strings.Index(name, NameSeparator)
	if i <= 0 || i+len(NameSeparator) >= len(name) {
		return "", "", false
	}
	return name[:i], name[i+len(NameSeparator):], true
}

// ErrPinnedByOrg is wrapped by every refusal to change an entry the
// organisation pinned.
var ErrPinnedByOrg = errors.New("toolexposure: set by your organisation")

// PinnedError names the pinned entry a write or load was refused on.
// Server and Tool (bare) name the entry; both are empty for the schema
// budget. Tier is the pinned value. BundleID is the bundle the pin came
// from.
type PinnedError struct {
	Server   string
	Tool     string
	Tier     Tier
	Budget   int
	BundleID int64
}

func (e *PinnedError) Error() string {
	origin := "your organisation"
	if e.BundleID > 0 {
		origin = fmt.Sprintf("your organisation (fleet config bundle %d)", e.BundleID)
	}
	switch {
	case e.Server == "" && e.Tool == "":
		return fmt.Sprintf("the tool schema budget is set by %s to %d tokens and cannot be changed here", origin, e.Budget)
	case e.Tool == "":
		return fmt.Sprintf("server %s is set by %s to %s and cannot be changed here", e.Server, origin, e.Tier)
	default:
		return fmt.Sprintf("tool %s%s%s is set by %s to %s and cannot be changed here", e.Server, NameSeparator, e.Tool, origin, e.Tier)
	}
}

func (e *PinnedError) Unwrap() error { return ErrPinnedByOrg }

// CheckOrgPins refuses a write of one override layer that changes an
// entry the organisation pinned. A proposed entry is a change when it
// holds a tier and that tier differs from the stored layer's entry;
// dropping an entry is allowed (the pin still applies). Pinned keys are:
// every entry under a server whose server-wide tier is pinned, each
// pinned tool entry, and each hot_set_extra tool.
func CheckOrgPins(org OrgPolicy, stored, proposed Exposure) error {
	servers := make([]string, 0, len(proposed.Servers))
	for s := range proposed.Servers {
		servers = append(servers, s)
	}
	sort.Strings(servers)
	for _, server := range servers {
		ps := proposed.Servers[server]
		ss := stored.Servers[server]
		pin := org.Pins.Servers[server]
		serverPinned := pin.Tier.Valid()
		if ps.Tier != "" && ps.Tier != ss.Tier && serverPinned {
			return &PinnedError{Server: server, Tier: pin.Tier, BundleID: org.BundleID}
		}
		tools := make([]string, 0, len(ps.Tools))
		for t := range ps.Tools {
			tools = append(tools, t)
		}
		sort.Strings(tools)
		for _, tool := range tools {
			v := ps.Tools[tool]
			if v == "" || v == ss.Tools[tool] {
				continue
			}
			switch {
			case org.inHotSetExtra(server + NameSeparator + tool):
				return &PinnedError{Server: server, Tool: tool, Tier: TierFull, BundleID: org.BundleID}
			case pin.Tools[tool].Valid():
				return &PinnedError{Server: server, Tool: tool, Tier: pin.Tools[tool], BundleID: org.BundleID}
			case serverPinned:
				return &PinnedError{Server: server, Tool: tool, Tier: pin.Tier, BundleID: org.BundleID}
			}
		}
	}
	return nil
}

// CheckOrgBudget refuses changing the stored schema budget while the
// organisation pins one. Writing the stored value back is allowed, so a
// read-edit-write of the other settings still succeeds.
func CheckOrgBudget(org OrgPolicy, stored, proposed int) error {
	if org.SchemaBudgetTokens > 0 && proposed != stored {
		return &PinnedError{Budget: org.SchemaBudgetTokens, BundleID: org.BundleID}
	}
	return nil
}
