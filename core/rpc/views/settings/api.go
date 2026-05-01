// Package settings defines the SettingsAPI view-scoped accessor and
// the SettingsStore implementation interface backing it. Persistence
// is a single JSON file per privacy CI invariant #5 (plan §4.3).
package settings

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/compaction"
)

// Settings is the persisted UI state shape (plan §5.5). schemaVersion
// gates migrations; lastRoute drives first-paint route restoration.
//
// MemoryEnabled is the explicit opt-in for the cross-session long-term
// memory feature. Privacy default is OFF: when false the harness never
// embeds, queries, or injects memory chunks regardless of what is on disk.
type Settings struct {
	SchemaVersion int        `json:"schemaVersion"`
	LastRoute     string     `json:"lastRoute"`
	Theme         string     `json:"theme"`
	Accent        string     `json:"accent"`
	WindowSize    WindowSize `json:"windowSize"`
	MemoryEnabled bool       `json:"memoryEnabled"`
	// ConfirmEachDisabled is the inverted persisted form of the
	// WP05 confirm-each tool-call modal flag. We store the disabled
	// bit (default false → modal ENABLED) so the zero value of a
	// freshly-installed settings file matches the spec's "default
	// ON" requirement without an extra Configured marker.
	//
	// Frontend / toolloop callers should never read this directly —
	// use ConfirmEachEnabled() helper or the Settings_GetConfirmEach
	// binding which inverts on the boundary.
	ConfirmEachDisabled bool `json:"confirmEachDisabled"`

	// Artifacts capture settings (artifacts-storage FR-017). All four
	// fields use a zero-value-friendly persisted form: AutoCapture*
	// stores the inverted "Disabled" bit so a fresh install (zero
	// values across the board) matches the spec's defaults
	// (capture ON, 10 lines, 200 bytes). The MinLines / MinBytes
	// zero-values fall back to the spec defaults via the helper
	// methods on the loaded record.
	AutoCaptureCodeBlocksDisabled  bool `json:"autoCaptureCodeBlocksDisabled,omitempty"`
	CodeBlockMinLines              int  `json:"codeBlockMinLines,omitempty"`
	CodeBlockMinBytes              int  `json:"codeBlockMinBytes,omitempty"`
	AutoCaptureToolOutputsDisabled bool `json:"autoCaptureToolOutputsDisabled,omitempty"`

	// WebSearchEnabled controls the local-first web search built-in
	// (core/tools/websearch). Default OFF on a fresh install — the user
	// opts in from the Tools panel. Local-only by design: DuckDuckGo
	// HTML scrape + Wikipedia + go-readability extraction. No API key.
	WebSearchEnabled bool `json:"webSearchEnabled,omitempty"`

	// BashEnabled controls the local-first bash built-in
	// (core/tools/bash). Default OFF. Sandboxed to a configurable
	// command allowlist (BashAllowlistEditor component) regardless of
	// provider. The allowlist itself ships with a conservative default
	// (read-only commands; deny by pattern).
	BashEnabled bool `json:"bashEnabled,omitempty"`

	// SaveArtifactDisabled is the inverted-form persisted bit for the
	// kaneaz__save_artifact built-in. Default ON (zero-value Disabled
	// → tool enabled) — saving deliverables is a low-risk primitive
	// that should work on first launch without setup. Mirrors the
	// AutoCaptureCodeBlocksDisabled pattern. Read via the
	// SaveArtifactEnabled() accessor; never read directly.
	SaveArtifactDisabled bool `json:"saveArtifactDisabled,omitempty"`

	// MaxAgentTurns caps the number of LLM ↔ tool round-trips inside
	// a single chat-graph LoopNode body before the cap-hit pause-not-kill
	// UX fires (commit c760087). Zero falls back to the spec default
	// (DefaultMaxAgentTurns = 25 — agreed with the user when migrating
	// off core/toolloop's per-call DefaultMaxIter=8). The chassis reads
	// the effective value via EffectiveMaxAgentTurns on every chat run
	// start so a settings change takes effect on the next user turn
	// without restarting the harness.
	MaxAgentTurns int `json:"maxAgentTurns,omitempty"`

	// CompactionAggressiveness controls when and how aggressively the
	// harness summarises older session history when approaching the
	// model's context window. One of: "off", "conservative", "balanced",
	// "aggressive", "maximal". Empty string == "balanced" via the
	// EffectiveCompactionAggressiveness accessor.
	//
	// Plan: compaction-strategy-ui-01KQ8TDI §2.9. The chat runner reads
	// the effective tier on every send; settings changes take effect on
	// the next turn without restarting.
	CompactionAggressiveness string `json:"compactionAggressiveness,omitempty"`

	// CompactionModel is the provider+model used for the summarisation
	// call. Empty == "use the session's active model" (the chained
	// default the chat runner falls back to when this is unset). The
	// wire shape mirrors core/compaction.ProviderProfileRef but is
	// declared locally here so settings doesn't take a dependency on
	// the engine's internal types beyond the policy constants.
	CompactionModel ProviderProfileRef `json:"compactionModel,omitempty"`

	// CompactionArchiveDays is the soft-archive retention window in
	// days. Default 90, range [7, 365]. Save() rejects out-of-range
	// values; the EffectiveCompactionArchiveDays accessor clamps for
	// defensive reads (e.g. against a hand-edited settings file).
	CompactionArchiveDays int `json:"compactionArchiveDays,omitempty"`

	// CompactionRecentWindow is the count of most-recent user-assistant
	// pairs that compaction will never touch. Default 4 (locked plan
	// default §2.6). Negative is rejected at Save; zero rounds up to the
	// default via the EffectiveCompactionRecentWindow accessor.
	CompactionRecentWindow int `json:"compactionRecentWindow,omitempty"`

	// KeyboardShortcuts holds user-overridden shortcut bindings keyed by
	// stable shortcut id (e.g. "chat.send" → "Cmd+Shift+Enter"). An
	// absent/empty map means all shortcuts use their registry defaults.
	// Backend validates: ≤200 entries, ≤64 chars/value, no control chars.
	// omitempty: old clients that don't know about this field simply read
	// an empty map and use registry defaults — no migration needed.
	// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7 / C-004)
	KeyboardShortcuts map[string]string `json:"keyboardShortcuts,omitempty"`

	// KeyboardShortcutsPreset is reserved for a future preset-gallery
	// follow-up mission. v1 always persists as empty string; old clients
	// ignore the field. (keyboard-shortcuts-settings-01KQ8TDR plan Q1=C)
	KeyboardShortcutsPreset string `json:"keyboardShortcutsPreset,omitempty"`
}

// ProviderProfileRef is the wire shape that identifies a provider+model
// pair. Mirrors core/compaction.ProviderProfileRef exactly — the engine
// declares its own copy to avoid depending on this view package; if the
// llm package ever exports a canonical ProviderProfileRef, both copies
// can be replaced with a type alias.
//
// JSON casing follows the existing settings convention (camelCase) used
// elsewhere in this struct.
type ProviderProfileRef struct {
	ProviderID string `json:"providerId,omitempty"`
	ModelID    string `json:"modelId,omitempty"`
}

// IsZero reports whether the ref is the empty value (no provider, no
// model). Used by the chat runner to decide between the configured
// compaction model and the session's active model fallback.
func (r ProviderProfileRef) IsZero() bool {
	return r.ProviderID == "" && r.ModelID == ""
}

// DefaultCompactionArchiveDays is the spec-locked retention window for
// soft-archived original messages (plan §2.7). Reads through the
// EffectiveCompactionArchiveDays accessor.
const DefaultCompactionArchiveDays = 90

// MinCompactionArchiveDays is the lower clamp applied at save and at
// effective-read time. Below this, the sweep would race against
// still-useful reviewable history.
const MinCompactionArchiveDays = 7

// MaxCompactionArchiveDays is the upper clamp applied at save and at
// effective-read time. Above this, archived rows accumulate without
// bound and the sweep stops being a sweep.
const MaxCompactionArchiveDays = 365

// DefaultCompactionRecentWindow is the spec-locked count of most-recent
// user-assistant pairs that compaction never touches (plan §2.6).
const DefaultCompactionRecentWindow = 4

// DefaultMaxAgentTurns is the spec-locked iteration cap for the chat
// graph's LoopNode body when Settings.MaxAgentTurns is unset (zero).
// Set to 25 per the agent-kernel-graph-chat-migration mission brief
// (the user's stated preference replacing toolloop's old 8-call cap).
const DefaultMaxAgentTurns = 25

// AutoCaptureCodeBlocks reports whether the code-block detector is
// active. Default true on a fresh install (zero-value Disabled).
func (s Settings) AutoCaptureCodeBlocks() bool { return !s.AutoCaptureCodeBlocksDisabled }

// SaveArtifactEnabled reports whether the kaneaz__save_artifact
// built-in is enabled. Default true on a fresh install (zero-value
// Disabled). Inverted on the wire so the JSON shape matches the
// storage contract.
func (s Settings) SaveArtifactEnabled() bool { return !s.SaveArtifactDisabled }

// AutoCaptureToolOutputs reports whether the tool-output detector is
// active. Default true on a fresh install.
func (s Settings) AutoCaptureToolOutputs() bool { return !s.AutoCaptureToolOutputsDisabled }

// EffectiveCodeBlockMinLines returns the user-tuned threshold or the
// spec default (10) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinLines() int {
	if s.CodeBlockMinLines <= 0 {
		return 10
	}
	return s.CodeBlockMinLines
}

// EffectiveCodeBlockMinBytes returns the user-tuned threshold or the
// spec default (200) when the persisted value is zero.
func (s Settings) EffectiveCodeBlockMinBytes() int {
	if s.CodeBlockMinBytes <= 0 {
		return 200
	}
	return s.CodeBlockMinBytes
}

// ConfirmEachEnabled is the user-facing form of the WP05 modal flag.
// Defaults to true on a fresh install (zero-value ConfirmEachDisabled)
// and inverts the persisted bit so callers don't have to think about
// the storage shape.
func (s Settings) ConfirmEachEnabled() bool { return !s.ConfirmEachDisabled }

// EffectiveMaxAgentTurns returns the user-tuned cap or the spec
// default (DefaultMaxAgentTurns) when the persisted value is zero.
// The chat-graph runner reads this on every Run start to thread the
// cap onto the LoopNode body's max_iterations.
func (s Settings) EffectiveMaxAgentTurns() int {
	if s.MaxAgentTurns <= 0 {
		return DefaultMaxAgentTurns
	}
	return s.MaxAgentTurns
}

// EffectiveCompactionAggressiveness returns the locked tier enum the
// chat runner / engine consume. Empty / unknown persisted values
// resolve to the documented default ("balanced" via compaction.Tier's
// own fallback). Returning compaction.CompactionAggressiveness directly
// keeps the call site one type-cast lighter than going through the raw
// string and lets compaction.Tier's switch handle unknown values.
func (s Settings) EffectiveCompactionAggressiveness() compaction.CompactionAggressiveness {
	if s.CompactionAggressiveness == "" {
		return compaction.AggressivenessBalanced
	}
	return compaction.CompactionAggressiveness(s.CompactionAggressiveness)
}

// EffectiveCompactionArchiveDays returns the user-tuned retention or
// DefaultCompactionArchiveDays when zero. Out-of-range persisted values
// (a hand-edited settings file, an old client) are clamped to the
// [Min, Max] range so the sweep never queries a nonsense window. Save
// validation rejects out-of-range writes so well-behaved clients never
// hit the clamp branch.
func (s Settings) EffectiveCompactionArchiveDays() int {
	d := s.CompactionArchiveDays
	if d <= 0 {
		return DefaultCompactionArchiveDays
	}
	if d < MinCompactionArchiveDays {
		return MinCompactionArchiveDays
	}
	if d > MaxCompactionArchiveDays {
		return MaxCompactionArchiveDays
	}
	return d
}

// EffectiveCompactionRecentWindow returns the user-tuned recent-window
// (number of most-recent user-assistant pairs compaction never touches)
// or DefaultCompactionRecentWindow when zero or negative.
func (s Settings) EffectiveCompactionRecentWindow() int {
	if s.CompactionRecentWindow <= 0 {
		return DefaultCompactionRecentWindow
	}
	return s.CompactionRecentWindow
}

// WindowSize mirrors the charter's WindowSize type.
type WindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SettingsStore is the persistence interface (default impl: single
// JSON file at $USER_CONFIG_DIR/kaneaz-harness/settings.json).
type SettingsStore interface {
	LoadAll() (Settings, error)
	SaveAll(Settings) error
	LoadRoute() (string, error)
	SaveRoute(string) error
	LogRouteChange(from, to string) error
	LoadTheme() (string, error)
	SaveTheme(string) error
	// LoadMemory / SaveMemory expose the long-term-memory opt-in
	// independently of the full Settings round-trip so the rpc layer
	// can read it on the hot path (every send) without serializing
	// the whole record.
	LoadMemory() (bool, error)
	SaveMemory(enabled bool) error
	// LoadConfirmEach / SaveConfirmEach expose the WP05 confirm-each
	// tool-call modal opt-in independently of the full Settings
	// record. The toolloop reads this on every Run boundary so the
	// frontend toggle takes effect on the next chat without a
	// settings round-trip. Default true (modal ON unless the user
	// turns it off explicitly).
	LoadConfirmEach() (bool, error)
	SaveConfirmEach(enabled bool) error
	// LoadWebSearch / SaveWebSearch expose the local-first web search
	// built-in opt-in independently of the full Settings record. The
	// toolloop / kernel reads this on every Run boundary so the toggle
	// takes effect on the next chat. Default false (off).
	LoadWebSearch() (bool, error)
	SaveWebSearch(enabled bool) error
	// LoadBash / SaveBash expose the local-first bash built-in opt-in
	// in the same shape. Default false (off).
	LoadBash() (bool, error)
	SaveBash(enabled bool) error
	// LoadSaveArtifactEnabled / SaveSaveArtifactEnabled expose the
	// kaneaz__save_artifact built-in opt-in. Default true (on) — saving
	// deliverables is a low-risk primitive that should work on first
	// launch. The toolloop's EnabledFilter consults the predicate
	// (which calls LoadSaveArtifactEnabled) on every Run boundary so a
	// toggle takes effect on the next chat.
	LoadSaveArtifactEnabled() (bool, error)
	SaveSaveArtifactEnabled(enabled bool) error
	// LoadMaxAgentTurns / SaveMaxAgentTurns expose the chat-graph
	// LoopNode iteration cap independently of the full Settings record
	// so the chassis can read it on the hot path (every chat run start)
	// without serializing the whole record. Persists as the raw int;
	// zero on the wire means "use DefaultMaxAgentTurns". The frontend
	// SettingsClient surfaces this via Settings_GetMaxAgentTurns /
	// Settings_SetMaxAgentTurns.
	LoadMaxAgentTurns() (int, error)
	SaveMaxAgentTurns(turns int) error
}

// SettingsAPI is the view-scoped accessor exposed via HarnessAPI.
type SettingsAPI interface {
	Get(ctx context.Context) (Settings, error)
	Set(ctx context.Context, s Settings) error
}

// ShortcutsStore is the persistence interface for keyboard shortcut
// overrides. LoadShortcuts / SaveShortcuts are thin field-level accessors
// so callers can read/write just the shortcuts map without a full
// settings round-trip. Validation (validateShortcuts) still runs on every
// SaveShortcuts call via SaveAll under the hood.
// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7)
type ShortcutsStore interface {
	// LoadShortcuts returns the current KeyboardShortcuts map.
	// Missing settings file → empty map (no error).
	LoadShortcuts() (map[string]string, error)
	// SaveShortcuts atomically replaces the full KeyboardShortcuts map.
	SaveShortcuts(m map[string]string) error
}
