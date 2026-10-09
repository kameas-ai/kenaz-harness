// Package fleet — tool_exposure.go
//
// The bundle's tool_exposure section (tool-context-budget-01TCBUD01 WP07,
// spec §2.1 step 1, FR-K4) and the store that holds the organisation's
// tool-exposure policy decoded from the last verified bundle.
//
// Apply runs on every verified bundle. An absent section clears the
// policy, so tiers drop back to the user's settings on the next poll that
// no longer carries it. Entries this build cannot honour (an unknown tier,
// an empty name, a malformed hot_set_extra name, a budget out of range)
// are refused one by one and reported as apply errors for the ACK while
// the rest of the section applies — the mandated_items posture: a named
// refusal, never a silent drop, never the whole bundle refused.
//
// The applied section is persisted at <dataDir>/fleet/tool_exposure_applied.json
// and reloaded at boot: a restart answers the next poll with a 304, so
// without it the policy would vanish until the org published a new bundle.
package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// BundleToolExposure is the signed tool_exposure section.
//
// Wire shape (every key and omitempty is part of the signed bytes):
//
//	"tool_exposure": {
//	  "servers": {
//	    "<server>": {"tier": "full|summary|off", "pinned": true,
//	                 "tools": {"<bare tool>": {"tier": "...", "pinned": false}}}
//	  },
//	  "budget_tokens": 16000,
//	  "hot_set_extra": ["<server>__<tool>", ...]
//	}
//
// Maps marshal in sorted-key order (encoding/json), so the section's bytes
// are deterministic; hot_set_extra is re-marshalled in received order and
// never re-sorted.
type BundleToolExposure struct {
	Servers      map[string]BundleToolExposureServer `json:"servers,omitempty"`
	BudgetTokens int                                 `json:"budget_tokens,omitempty"`
	HotSetExtra  []string                            `json:"hot_set_extra,omitempty"`
}

// BundleToolExposureServer is one server's entry. Tier is the server-wide
// tier; empty means the org sets no server-wide tier (an entry carrying
// only tool entries). Pinned applies to Tier and requires it. Pinned is
// always on the wire.
type BundleToolExposureServer struct {
	Tier   string                            `json:"tier,omitempty"`
	Pinned bool                              `json:"pinned"`
	Tools  map[string]BundleToolExposureTool `json:"tools,omitempty"`
}

// BundleToolExposureTool is one tool's entry, keyed by the bare tool name.
// Both fields are always on the wire.
type BundleToolExposureTool struct {
	Tier   string `json:"tier"`
	Pinned bool   `json:"pinned"`
}

// ErrToolExposureEntryRefused is wrapped by every per-entry refusal.
var ErrToolExposureEntryRefused = errors.New("fleet: tool_exposure entry refused")

// Policy decodes the section into the organisation's policy. Every entry
// this build cannot honour is left out and named in errs.
func (s *BundleToolExposure) Policy(bundleID int64) (toolexposure.OrgPolicy, []error) {
	p := toolexposure.OrgPolicy{BundleID: bundleID}
	if s == nil {
		return p, nil
	}
	var errs []error
	refuse := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrToolExposureEntryRefused}, args...)...))
	}
	put := func(e *toolexposure.Exposure, server string, tier toolexposure.Tier, tool string) {
		if e.Servers == nil {
			e.Servers = map[string]toolexposure.ServerExposure{}
		}
		se := e.Servers[server]
		if tool == "" {
			se.Tier = tier
		} else {
			if se.Tools == nil {
				se.Tools = map[string]toolexposure.Tier{}
			}
			se.Tools[tool] = tier
		}
		e.Servers[server] = se
	}
	layer := func(pinned bool) *toolexposure.Exposure {
		if pinned {
			return &p.Pins
		}
		return &p.Defaults
	}
	for _, server := range sortedKeysOf(s.Servers) {
		se := s.Servers[server]
		if server == "" {
			refuse("empty server name")
			continue
		}
		switch {
		case se.Tier == "" && se.Pinned:
			refuse("server %q: pinned without a tier", server)
		case se.Tier != "" && !toolexposure.Tier(se.Tier).Valid():
			refuse("server %q: unknown tier %q", server, se.Tier)
		case se.Tier != "":
			put(layer(se.Pinned), server, toolexposure.Tier(se.Tier), "")
		}
		for _, tool := range sortedKeysOf(se.Tools) {
			te := se.Tools[tool]
			switch {
			case tool == "":
				refuse("server %q: empty tool name", server)
			case strings.HasPrefix(tool, server+toolexposure.NameSeparator):
				refuse("tool %q under server %q: use the bare tool name", tool, server)
			case !toolexposure.Tier(te.Tier).Valid():
				refuse("tool %s%s%s: unknown tier %q", server, toolexposure.NameSeparator, tool, te.Tier)
			default:
				put(layer(te.Pinned), server, toolexposure.Tier(te.Tier), tool)
			}
		}
	}
	switch {
	case s.BudgetTokens < 0 || s.BudgetTokens > toolexposure.MaxSchemaBudgetTokens:
		refuse("budget_tokens %d out of range [0, %d] (0 = not pinned)", s.BudgetTokens, toolexposure.MaxSchemaBudgetTokens)
	default:
		p.SchemaBudgetTokens = s.BudgetTokens
	}
	seen := map[string]bool{}
	for _, n := range s.HotSetExtra {
		if _, _, ok := toolexposure.SplitName(n); !ok {
			refuse("hot_set_extra %q: want <server>__<tool>", n)
			continue
		}
		if !seen[n] {
			seen[n] = true
			p.HotSetExtra = append(p.HotSetExtra, n)
		}
	}
	return p, errs
}

// toolExposureState is the persisted applied section.
type toolExposureState struct {
	BundleID     int64               `json:"bundle_id"`
	ToolExposure *BundleToolExposure `json:"tool_exposure"`
}

func toolExposureStatePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "tool_exposure_applied.json")
}

// ToolExposurePins holds the organisation's tool-exposure policy. It
// implements toolexposure.PinSource through ToolExposurePolicy. Safe for
// concurrent use.
type ToolExposurePins struct {
	dataDir string

	mu     sync.RWMutex
	policy toolexposure.OrgPolicy
}

// LoadToolExposurePins returns a store holding the policy persisted under
// dataDir (empty dataDir: in memory only). A state file that cannot be
// read or decoded leaves the policy empty, is logged, and invalidates the
// bundle apply record so the config poller re-fetches and re-applies the
// current bundle once instead of answering 304 to a policy it lost.
func LoadToolExposurePins(dataDir string) *ToolExposurePins {
	t := &ToolExposurePins{dataDir: dataDir}
	if dataDir == "" {
		return t
	}
	raw, err := os.ReadFile(toolExposureStatePath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return t
	}
	var st toolExposureState
	if err == nil {
		err = json.Unmarshal(raw, &st)
	}
	if err != nil {
		logging.L().Warn("fleet.tool_exposure.state_unreadable", "err", err.Error())
		_ = os.Remove(bundleApplyMetaPath(dataDir))
		return t
	}
	p, errs := st.ToolExposure.Policy(st.BundleID)
	for _, e := range errs {
		logging.L().Warn("fleet.tool_exposure.state_entry_refused", "err", e.Error())
	}
	t.policy = p
	return t
}

// Apply installs the section from a verified bundle; nil clears the
// policy. It returns one error per refused entry and any persistence
// failure.
func (t *ToolExposurePins) Apply(bundleID int64, s *BundleToolExposure) []error {
	p, errs := s.Policy(bundleID)
	if s == nil {
		p = toolexposure.OrgPolicy{}
	}
	t.mu.Lock()
	t.policy = p
	t.mu.Unlock()
	if err := t.persist(bundleID, s); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// Clear drops the policy and its state file (explicit sign-out).
func (t *ToolExposurePins) Clear() error {
	t.mu.Lock()
	t.policy = toolexposure.OrgPolicy{}
	t.mu.Unlock()
	return t.persist(0, nil)
}

// Policy returns a copy of the current policy.
func (t *ToolExposurePins) Policy() toolexposure.OrgPolicy {
	if t == nil {
		return toolexposure.OrgPolicy{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	p := t.policy
	p.Pins = p.Pins.Clone()
	p.Defaults = p.Defaults.Clone()
	p.HotSetExtra = append([]string(nil), p.HotSetExtra...)
	return p
}

// ToolExposurePolicy implements toolexposure.PinSource.
func (t *ToolExposurePins) ToolExposurePolicy(_ context.Context) (toolexposure.OrgPolicy, error) {
	return t.Policy(), nil
}

var _ toolexposure.PinSource = (*ToolExposurePins)(nil)

func (t *ToolExposurePins) persist(bundleID int64, s *BundleToolExposure) error {
	if t.dataDir == "" {
		return nil
	}
	path := toolExposureStatePath(t.dataDir)
	if s == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("fleet: tool_exposure: remove state: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("fleet: tool_exposure: mkdir: %w", err)
	}
	raw, err := json.Marshal(toolExposureState{BundleID: bundleID, ToolExposure: s})
	if err != nil {
		return fmt.Errorf("fleet: tool_exposure: marshal state: %w", err)
	}
	return atomicWriteFile(path, string(raw)+"\n")
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
