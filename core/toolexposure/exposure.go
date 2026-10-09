// Package toolexposure decides, per tool, how much of it the model is
// shown on a call: the full schema, a line in the capability digest, or
// nothing (spec tool-context-budget-01TCBUD01 §2.1).
//
// The package owns the data model every layer stores (Exposure,
// Activation), its storage codec, the harness defaults (hot set, schema
// budget, activation TTL) and the resolver that folds the layers into
// one tier per tool. It imports nothing from the rest of the harness so
// settings, projects and sessions can all store its types without an
// import cycle.
package toolexposure

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Tier is how much of a tool the model sees on a call.
type Tier string

const (
	// TierFull sends the complete ToolSpec in the request's tools array.
	TierFull Tier = "full"
	// TierSummary lists the tool's server in the capability digest; the
	// schema is sent only once the tool is activated.
	TierSummary Tier = "summary"
	// TierOff hides the tool entirely: not sent, not in the digest, not
	// loadable.
	TierOff Tier = "off"
)

// Valid reports whether t is one of the three tiers. The empty Tier is
// not valid: a layer that has no opinion omits the entry instead.
func (t Tier) Valid() bool {
	switch t {
	case TierFull, TierSummary, TierOff:
		return true
	}
	return false
}

// BuiltinServer is the reserved server name built-in tools publish
// under ("kenaz__<tool>"); equal to toolloop.BuiltinServerName.
const BuiltinServer = "kenaz"

// NameSeparator joins server and tool into the name the model sees.
const NameSeparator = "__"

// LoadToolsName is the built-in that loads summary tools on demand. It
// resolves full whenever any other tool resolves summary, whatever any
// layer says, because otherwise summary tools are unreachable.
const LoadToolsName = BuiltinServer + NameSeparator + "load_tools"

// DefaultSchemaBudgetTokens is the schema budget when the user has not
// set one (spec §2.3, Q-E).
const DefaultSchemaBudgetTokens = 24000

// MaxSchemaBudgetTokens bounds the budget setting.
const MaxSchemaBudgetTokens = 2_000_000

// DefaultActivationTTLTurns is how many unused turns a non-sticky
// activation survives when the user has not set a TTL (spec §2.3, Q-B).
const DefaultActivationTTLTurns = 6

// MaxActivationTTLTurns bounds the TTL setting.
const MaxActivationTTLTurns = 1000

// hotSet is the built-in set that resolves full by default (spec §2.1
// step 5): the smallest set that lets the model do the basics and load
// the rest.
var hotSet = map[string]struct{}{
	LoadToolsName:              {},
	"kenaz__read_file":         {},
	"kenaz__list_dir":          {},
	"kenaz__glob":              {},
	"kenaz__grep":              {},
	"kenaz__bash":              {},
	"kenaz__write_file":        {},
	"kenaz__edit_file":         {},
	"kenaz__web_fetch":         {},
	"kenaz__web_search":        {},
	"kenaz__ask_user_question": {},
	"kenaz__todo_write":        {},
	"kenaz__save_artifact":     {},
	"kenaz__skill":             {},
	"kenaz__fork_conversation": {},
}

// HotSet returns the default-full built-in names, sorted.
func HotSet() []string {
	out := make([]string, 0, len(hotSet))
	for n := range hotSet {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// InHotSet reports whether name (the namespaced "kenaz__<tool>" form)
// is in the default-full built-in set.
func InHotSet(name string) bool {
	_, ok := hotSet[name]
	return ok
}

// ServerExposure is one server's setting at one layer. Tier applies to
// every tool of the server that has no entry in Tools; an empty Tier
// means the layer has no server-wide opinion. Tools is keyed by the
// bare tool name (without the "<server>__" prefix).
type ServerExposure struct {
	Tier  Tier            `json:"tier,omitempty"`
	Tools map[string]Tier `json:"tools,omitempty"`
}

// Exposure is one layer's tier settings: org pins, session override,
// project override or the user's settings. The zero value has no
// opinion about any tool.
type Exposure struct {
	Servers map[string]ServerExposure `json:"servers,omitempty"`
}

// IsZero reports whether the layer has no opinion about any tool.
func (e Exposure) IsZero() bool {
	for _, s := range e.Servers {
		if s.Tier != "" || len(s.Tools) > 0 {
			return false
		}
	}
	return true
}

// Validate rejects unknown tiers, empty server or tool names, and tool
// keys in namespaced form ("outlook__send-mail" under "outlook"), so a
// typo is refused at the write instead of silently ignored at resolve.
//
// It checks the layer alone. Refusing a layer that would turn
// LoadToolsName off while summary tools exist needs the resolved
// catalog, so that check is a WriteGuard the writers consult after
// Validate.
func (e Exposure) Validate() error {
	for server, s := range e.Servers {
		if strings.TrimSpace(server) == "" {
			return fmt.Errorf("toolexposure: empty server name")
		}
		if s.Tier != "" && !s.Tier.Valid() {
			return fmt.Errorf("toolexposure: server %q: unknown tier %q (want full, summary or off)", server, s.Tier)
		}
		for tool, t := range s.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("toolexposure: server %q: empty tool name", server)
			}
			if strings.HasPrefix(tool, server+NameSeparator) {
				return fmt.Errorf("toolexposure: server %q: tool key %q must be the bare name %q",
					server, tool, strings.TrimPrefix(tool, server+NameSeparator))
			}
			if !t.Valid() {
				return fmt.Errorf("toolexposure: tool %s%s%s: unknown tier %q (want full, summary or off)", server, NameSeparator, tool, t)
			}
		}
	}
	return nil
}

// Clone returns a deep copy, so a stored layer never aliases a
// caller's maps.
func (e Exposure) Clone() Exposure {
	if e.Servers == nil {
		return Exposure{}
	}
	out := Exposure{Servers: make(map[string]ServerExposure, len(e.Servers))}
	for name, s := range e.Servers {
		cs := ServerExposure{Tier: s.Tier}
		if s.Tools != nil {
			cs.Tools = make(map[string]Tier, len(s.Tools))
			for k, v := range s.Tools {
				cs.Tools[k] = v
			}
		}
		out.Servers[name] = cs
	}
	return out
}

// TierFor returns this layer's own opinion for one tool, by its bare
// name under server: the tool entry, else the server-wide tier, else ""
// (no opinion).
func (e Exposure) TierFor(server, tool string) Tier {
	return e.lookup(server, tool)
}

// lookup returns this layer's tier for (server, tool): the tool entry
// if one exists, else the server-wide tier, else "" (no opinion). A
// value that is not one of the three tiers (a hand-edited file, a row
// written before validation) is no opinion, never a fourth tier.
func (e Exposure) lookup(server, tool string) Tier {
	s, ok := e.Servers[server]
	if !ok {
		return ""
	}
	if t, ok := s.Tools[tool]; ok && t.Valid() {
		return t
	}
	if s.Tier.Valid() {
		return s.Tier
	}
	return ""
}

// Activation is one tool the session has loaded on demand. Name is the
// namespaced name the model sees ("<server>__<tool>"); LastUsedTurn is
// the session turn ordinal the tool was last loaded or called on;
// Sticky activations survive TTL expiry.
type Activation struct {
	Name         string `json:"name"`
	Server       string `json:"server"`
	LastUsedTurn int    `json:"lastUsedTurn"`
	Sticky       bool   `json:"sticky"`
}

// ValidateActivations rejects activations whose name is not
// "<server>__<tool>", negative turns, and duplicate names.
func ValidateActivations(as []Activation) error {
	seen := make(map[string]struct{}, len(as))
	for _, a := range as {
		if strings.TrimSpace(a.Server) == "" {
			return fmt.Errorf("toolexposure: activation %q has no server", a.Name)
		}
		if p := a.Server + NameSeparator; !strings.HasPrefix(a.Name, p) || len(a.Name) == len(p) {
			return fmt.Errorf("toolexposure: activation %q: name must be %s<tool>", a.Name, p)
		}
		if a.LastUsedTurn < 0 {
			return fmt.Errorf("toolexposure: activation %q: negative lastUsedTurn", a.Name)
		}
		if _, dup := seen[a.Name]; dup {
			return fmt.Errorf("toolexposure: activation %q listed twice", a.Name)
		}
		seen[a.Name] = struct{}{}
	}
	return nil
}

// Settings is the user-level configuration: the per-server / per-tool
// tiers plus the schema budget and activation TTL as stored (0 = the
// harness default). The Effective* fields and Org are read-only: filled on
// read so a surface can show what applies (and which entries the
// organisation pinned), ignored on write so a read-edit-write round trip
// never pins today's default into the user's file.
type Settings struct {
	Exposure                    Exposure    `json:"exposure"`
	SchemaBudgetTokens          int         `json:"schemaBudgetTokens"`
	ActivationTTLTurns          int         `json:"activationTtlTurns"`
	EffectiveSchemaBudgetTokens int         `json:"effectiveSchemaBudgetTokens"`
	EffectiveActivationTTLTurns int         `json:"effectiveActivationTtlTurns"`
	Org                         OrgExposure `json:"org"`
}

// Validate bounds the budget and TTL and validates the exposure layer.
// The Effective* fields are not inputs and are not checked.
func (s Settings) Validate() error {
	if s.SchemaBudgetTokens < 0 || s.SchemaBudgetTokens > MaxSchemaBudgetTokens {
		return fmt.Errorf("toolexposure: schema budget %d out of range [0, %d] (0 = default %d)",
			s.SchemaBudgetTokens, MaxSchemaBudgetTokens, DefaultSchemaBudgetTokens)
	}
	if s.ActivationTTLTurns < 0 || s.ActivationTTLTurns > MaxActivationTTLTurns {
		return fmt.Errorf("toolexposure: activation TTL %d out of range [0, %d] (0 = default %d)",
			s.ActivationTTLTurns, MaxActivationTTLTurns, DefaultActivationTTLTurns)
	}
	return s.Exposure.Validate()
}

// WithEffective returns s with the Effective* fields filled from the
// stored values and the harness defaults.
func (s Settings) WithEffective() Settings {
	s.EffectiveSchemaBudgetTokens = s.SchemaBudgetTokens
	if s.EffectiveSchemaBudgetTokens <= 0 {
		s.EffectiveSchemaBudgetTokens = DefaultSchemaBudgetTokens
	}
	s.EffectiveActivationTTLTurns = s.ActivationTTLTurns
	if s.EffectiveActivationTTLTurns <= 0 {
		s.EffectiveActivationTTLTurns = DefaultActivationTTLTurns
	}
	return s
}

// MarshalExposureColumn encodes a layer for a nullable TEXT column: nil
// (SQL NULL) for a zero layer, else its JSON. It does not validate;
// writers validate once, at the manager.
func MarshalExposureColumn(e Exposure) (any, error) {
	if e.IsZero() {
		return nil, nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("toolexposure: marshal exposure: %w", err)
	}
	return string(b), nil
}

// ParseExposureColumn decodes a nullable TEXT column; NULL or empty is
// the zero layer.
func ParseExposureColumn(raw sql.NullString) (Exposure, error) {
	if !raw.Valid || raw.String == "" {
		return Exposure{}, nil
	}
	var e Exposure
	if err := json.Unmarshal([]byte(raw.String), &e); err != nil {
		return Exposure{}, fmt.Errorf("toolexposure: decode exposure column: %w", err)
	}
	return e, nil
}

// MarshalActivationsColumn encodes an activated set for a nullable TEXT
// column: nil (SQL NULL) when empty, else its JSON.
func MarshalActivationsColumn(as []Activation) (any, error) {
	if len(as) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(as)
	if err != nil {
		return nil, fmt.Errorf("toolexposure: marshal activations: %w", err)
	}
	return string(b), nil
}

// ParseActivationsColumn decodes a nullable TEXT column; NULL or empty
// is nil.
func ParseActivationsColumn(raw sql.NullString) ([]Activation, error) {
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var as []Activation
	if err := json.Unmarshal([]byte(raw.String), &as); err != nil {
		return nil, fmt.Errorf("toolexposure: decode activations column: %w", err)
	}
	return as, nil
}
