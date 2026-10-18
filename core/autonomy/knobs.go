package autonomy

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Knob is the canonical name of one of the seven independently-tunable
// autonomy knobs. Wire values are stable: the JSON field name in
// Layer.Overrides matches the Knob string exactly.
type Knob string

const (
	KnobMaxIterations            Knob = "maxIterations"
	KnobAskOnAmbiguity           Knob = "askOnAmbiguity"
	KnobAutoApproveFamilies      Knob = "autoApproveFamilies"
	KnobTokenCeilingPerTurn      Knob = "tokenCeilingPerTurn"
	KnobRecapStyle               Knob = "recapStyle"
	KnobContinueOnError          Knob = "continueOnError"
	KnobDestructiveActionPosture Knob = "destructiveActionPosture"
	// KnobRiskThreshold is risk-rated-autonomy-01PMRA01 WP03's dial: the
	// 0-100 score (spec FR-002's anchored bands) at or above which a
	// layer-3 (Cedar had no opinion) tool dispatch must ask a human
	// rather than being decided automatically. See presetTable for the
	// per-tier ladder (0/20/40/60/80) and
	// core/policy/cedar/risk_layer.go / kernel_tool_adapter.go's rung 0
	// for the consumer.
	KnobRiskThreshold Knob = "riskThreshold"
)

// AskMode controls when the loop pauses to ask the user vs. proceeds on its
// own.
type AskMode string

const (
	AskAlways  AskMode = "always"
	AskHard    AskMode = "hard"
	AskMajor   AskMode = "major"
	AskProceed AskMode = "proceed"
	AskNever   AskMode = "never"
)

// RecapMode controls how chatty the loop is when summarising what it just did.
type RecapMode string

const (
	RecapNone  RecapMode = "none"
	RecapBrief RecapMode = "brief"
	RecapFull  RecapMode = "full"
)

// ErrorMode controls what the loop does on a tool error.
type ErrorMode string

const (
	ErrorStop      ErrorMode = "stop"
	ErrorRetryOnce ErrorMode = "retry-once"
	ErrorAdapt     ErrorMode = "adapt"
)

// DestructivePosture controls the Cedar interactive prompt for destructive
// resources.
type DestructivePosture string

const (
	DestructiveConfirm   DestructivePosture = "confirm"
	DestructiveCedarOnly DestructivePosture = "cedar-only"
)

// Canonical tool-family names used by AutoApproveFamilies.
const (
	FamilyRead       = "read"
	FamilyWrite      = "write"
	FamilyShellSafe  = "shell-safe"
	FamilyNetwork    = "network"
)

// FamilySet is a set of canonical tool-family names. JSON shape is a sorted
// string array — round-trip stable, diff-friendly.
type FamilySet map[string]struct{}

// NewFamilySet returns a FamilySet containing the supplied families.
func NewFamilySet(families ...string) FamilySet {
	fs := make(FamilySet, len(families))
	for _, f := range families {
		fs[f] = struct{}{}
	}
	return fs
}

// Has reports whether the family is in the set.
func (fs FamilySet) Has(family string) bool {
	if fs == nil {
		return false
	}
	_, ok := fs[family]
	return ok
}

// Add inserts the family into the set. Returns the (possibly new) set so
// callers can chain on a nil receiver.
func (fs FamilySet) Add(family string) FamilySet {
	if fs == nil {
		fs = make(FamilySet)
	}
	fs[family] = struct{}{}
	return fs
}

// Remove deletes the family from the set. No-op if absent or set is nil.
func (fs FamilySet) Remove(family string) FamilySet {
	if fs == nil {
		return nil
	}
	delete(fs, family)
	return fs
}

// Sorted returns the family names in stable lexical order.
func (fs FamilySet) Sorted() []string {
	if len(fs) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(fs))
	for f := range fs {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Equal reports whether two FamilySets contain the same elements.
func (fs FamilySet) Equal(other FamilySet) bool {
	if len(fs) != len(other) {
		return false
	}
	for f := range fs {
		if _, ok := other[f]; !ok {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the set.
func (fs FamilySet) Clone() FamilySet {
	if fs == nil {
		return nil
	}
	out := make(FamilySet, len(fs))
	for f := range fs {
		out[f] = struct{}{}
	}
	return out
}

// MarshalJSON emits a sorted JSON array of family names. An empty (but
// non-nil) set marshals as `[]`; a nil set marshals as `null`.
func (fs FamilySet) MarshalJSON() ([]byte, error) {
	if fs == nil {
		return []byte("null"), nil
	}
	return json.Marshal(fs.Sorted())
}

// UnmarshalJSON accepts a JSON array of strings or null.
func (fs *FamilySet) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*fs = nil
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	out := make(FamilySet, len(arr))
	for _, f := range arr {
		out[f] = struct{}{}
	}
	*fs = out
	return nil
}
