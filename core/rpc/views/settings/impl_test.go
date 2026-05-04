package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/compaction"
)

// settingsEqual compares two Settings values with map-aware semantics.
// Plain `==` fails on Settings since it embeds a map.
func settingsEqual(a, b Settings) bool { return reflect.DeepEqual(a, b) }

func TestFileStore_LoadDefaults_OnMissingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("LastRoute = %q, want /sessions", got.LastRoute)
	}
	if got.Theme != "system" {
		t.Errorf("Theme = %q, want system", got.Theme)
	}
}

func TestFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	in := Settings{
		SchemaVersion: 1,
		LastRoute:     "/audit",
		Theme:         "dark",
		Accent:        "default",
		WindowSize:    WindowSize{Width: 1600, Height: 1000},
	}
	if err := store.SaveAll(in); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// Settings now contains a map (KeyboardShortcuts) → can't use ==.
	// Use reflect.DeepEqual via a small helper.
	if !settingsEqual(got, in) {
		t.Errorf("round trip lost data: got %+v, want %+v", got, in)
	}

	// Disk format: file exists, schemaVersion field present.
	raw, err := os.ReadFile(filepath.Join(dir, "kaneaz-harness", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("disk JSON parse: %v", err)
	}
	if _, ok := disk["schemaVersion"]; !ok {
		t.Errorf("disk JSON missing schemaVersion (privacy invariant #5)")
	}
	if _, ok := disk["lastRoute"]; !ok {
		t.Errorf("disk JSON missing lastRoute")
	}
	if _, ok := disk["theme"]; !ok {
		t.Errorf("disk JSON missing theme")
	}
}

func TestFileStore_PartialFile_FillsDefaults(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "kaneaz-harness", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.Theme != "dark" {
		t.Errorf("Theme = %q, want dark", got.Theme)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("LastRoute should fall back to /sessions, got %q", got.LastRoute)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion should fall back to 1, got %d", got.SchemaVersion)
	}
}

func TestFileStore_RouteAndThemeMethods(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.SaveRoute("/bundles"); err != nil {
		t.Fatalf("SaveRoute: %v", err)
	}
	if err := store.SaveTheme("dark"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}
	r, err := store.LoadRoute()
	if err != nil {
		t.Fatalf("LoadRoute: %v", err)
	}
	if r != "/bundles" {
		t.Errorf("LoadRoute = %q, want /bundles", r)
	}
	th, err := store.LoadTheme()
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	if th != "dark" {
		t.Errorf("LoadTheme = %q, want dark", th)
	}
}

func TestAPI_GetSet(t *testing.T) {
	api := NewAPI(nil)
	got, err := api.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("default LastRoute = %q, want /sessions", got.LastRoute)
	}

	in := got
	in.LastRoute = "/audit"
	if err := api.Set(context.Background(), in); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got2, _ := api.Get(context.Background())
	if got2.LastRoute != "/audit" {
		t.Errorf("post-set LastRoute = %q, want /audit", got2.LastRoute)
	}
}

func TestFileStore_ConfirmEachDefaultsTrue(t *testing.T) {
	// A fresh install with no settings.json (and one with a partial file
	// missing the confirmEachDisabled field) must report ConfirmEach
	// enabled — the spec's default-ON requirement maps to a zero-value
	// ConfirmEachDisabled.
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadConfirmEach()
	if err != nil {
		t.Fatalf("LoadConfirmEach: %v", err)
	}
	if !got {
		t.Errorf("LoadConfirmEach() = false on fresh install, want true (default ON)")
	}
}

func TestFileStore_ConfirmEachRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.SaveConfirmEach(false); err != nil {
		t.Fatalf("SaveConfirmEach: %v", err)
	}
	got, err := store.LoadConfirmEach()
	if err != nil {
		t.Fatalf("LoadConfirmEach: %v", err)
	}
	if got {
		t.Errorf("LoadConfirmEach = true after Save(false)")
	}
	if err := store.SaveConfirmEach(true); err != nil {
		t.Fatalf("SaveConfirmEach(true): %v", err)
	}
	got, _ = store.LoadConfirmEach()
	if !got {
		t.Errorf("LoadConfirmEach = false after Save(true)")
	}
}

func TestEffectiveMaxAgentTurns_DefaultWhenZero(t *testing.T) {
	// Zero on the wire ⇒ DefaultMaxAgentTurns (25). Negative is treated
	// the same way (defensive — the store normalises negatives to zero
	// on save, but the helper covers the in-memory path too).
	for _, tc := range []struct {
		raw  int
		want int
	}{
		{0, DefaultMaxAgentTurns},
		{-1, DefaultMaxAgentTurns},
		{1, 1},
		{50, 50},
	} {
		s := Settings{MaxAgentTurns: tc.raw}
		if got := s.EffectiveMaxAgentTurns(); got != tc.want {
			t.Errorf("EffectiveMaxAgentTurns(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestFileStore_MaxAgentTurnsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// Default (zero on wire) on a fresh install.
	got, err := store.LoadMaxAgentTurns()
	if err != nil {
		t.Fatalf("LoadMaxAgentTurns: %v", err)
	}
	if got != 0 {
		t.Errorf("fresh install MaxAgentTurns = %d, want 0 (zero ⇒ default)", got)
	}
	// Round-trip a non-zero value.
	if err := store.SaveMaxAgentTurns(10); err != nil {
		t.Fatalf("SaveMaxAgentTurns: %v", err)
	}
	got, _ = store.LoadMaxAgentTurns()
	if got != 10 {
		t.Errorf("after Save(10): MaxAgentTurns = %d, want 10", got)
	}
	// Negative value normalised to zero.
	if err := store.SaveMaxAgentTurns(-5); err != nil {
		t.Fatalf("SaveMaxAgentTurns(-5): %v", err)
	}
	got, _ = store.LoadMaxAgentTurns()
	if got != 0 {
		t.Errorf("after Save(-5): MaxAgentTurns = %d, want 0 (negatives normalised)", got)
	}
}

// TestFileStore_MonthlyCostNotifyUSDRoundTrip verifies the WP06
// dial round-trips, defaults to zero (disabled), normalises negatives
// to zero, and rejects values above MaxMonthlyCostNotifyUSD with the
// typed error.
func TestFileStore_MonthlyCostNotifyUSDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Fresh install: zero (scheduler disabled).
	got, err := store.LoadMonthlyCostNotifyUSD()
	if err != nil {
		t.Fatalf("LoadMonthlyCostNotifyUSD: %v", err)
	}
	if got != 0 {
		t.Errorf("fresh install MonthlyCostNotifyUSD = %v, want 0", got)
	}

	// Round-trip a sane value.
	if err := store.SaveMonthlyCostNotifyUSD(25.0); err != nil {
		t.Fatalf("SaveMonthlyCostNotifyUSD(25): %v", err)
	}
	got, _ = store.LoadMonthlyCostNotifyUSD()
	if got != 25.0 {
		t.Errorf("after Save(25): got %v, want 25", got)
	}

	// Negative normalised to zero (matches MaxAgentTurns convention).
	if err := store.SaveMonthlyCostNotifyUSD(-5); err != nil {
		t.Fatalf("SaveMonthlyCostNotifyUSD(-5): %v", err)
	}
	got, _ = store.LoadMonthlyCostNotifyUSD()
	if got != 0 {
		t.Errorf("after Save(-5): got %v, want 0", got)
	}

	// Above-cap rejected with typed error.
	err = store.SaveMonthlyCostNotifyUSD(MaxMonthlyCostNotifyUSD + 1)
	if !errors.Is(err, ErrInvalidMonthlyCostNotifyUSD) {
		t.Errorf("SaveMonthlyCostNotifyUSD(over-cap) err = %v, want ErrInvalidMonthlyCostNotifyUSD", err)
	}
}

// TestMonthlyCostNotifyEnabled covers the dial-disabled accessor.
func TestMonthlyCostNotifyEnabled(t *testing.T) {
	for _, tc := range []struct {
		raw     float64
		enabled bool
	}{
		{0, false},
		{0.0001, true},
		{25, true},
		{-1, false}, // defensive: hand-edited file slipping in a negative
	} {
		s := Settings{MonthlyCostNotifyUSD: tc.raw}
		if got := s.MonthlyCostNotifyEnabled(); got != tc.enabled {
			t.Errorf("MonthlyCostNotifyEnabled(%v) = %v, want %v", tc.raw, got, tc.enabled)
		}
	}
}

// TestSaveAll_RejectsOverCapMonthlyCostNotify covers the SaveAll
// validation path (vs the field-level SaveMonthlyCostNotifyUSD which
// has its own clamp).
func TestSaveAll_RejectsOverCapMonthlyCostNotify(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	err = store.SaveAll(Settings{MonthlyCostNotifyUSD: MaxMonthlyCostNotifyUSD + 100})
	if !errors.Is(err, ErrInvalidMonthlyCostNotifyUSD) {
		t.Errorf("SaveAll(over-cap) err = %v, want ErrInvalidMonthlyCostNotifyUSD", err)
	}
}

// TestEffectiveCompaction_DefaultsOnEmptySettings verifies the
// documented defaults the chat runner reads on a fresh install:
// balanced tier, 90-day retention, 4-pair recent window. The defaults
// are the source-of-truth surfaces — UI tooltip copy reads through
// these accessors so a regression here would silently change behaviour.
func TestEffectiveCompaction_DefaultsOnEmptySettings(t *testing.T) {
	var s Settings
	if got := s.EffectiveCompactionAggressiveness(); got != compaction.AggressivenessBalanced {
		t.Errorf("default aggressiveness = %q, want %q", got, compaction.AggressivenessBalanced)
	}
	if got := s.EffectiveCompactionArchiveDays(); got != DefaultCompactionArchiveDays {
		t.Errorf("default archive days = %d, want %d", got, DefaultCompactionArchiveDays)
	}
	if got := s.EffectiveCompactionRecentWindow(); got != DefaultCompactionRecentWindow {
		t.Errorf("default recent window = %d, want %d", got, DefaultCompactionRecentWindow)
	}
}

// TestEffectiveCompactionAggressiveness_PassesThroughKnownTiers checks
// that valid tier strings round-trip through Effective into the typed
// constant the engine consumes.
func TestEffectiveCompactionAggressiveness_PassesThroughKnownTiers(t *testing.T) {
	for _, tier := range []compaction.CompactionAggressiveness{
		compaction.AggressivenessOff,
		compaction.AggressivenessConservative,
		compaction.AggressivenessBalanced,
		compaction.AggressivenessAggressive,
		compaction.AggressivenessMaximal,
	} {
		s := Settings{CompactionAggressiveness: string(tier)}
		if got := s.EffectiveCompactionAggressiveness(); got != tier {
			t.Errorf("tier %q round-trip = %q, want %q", tier, got, tier)
		}
	}
}

// TestEffectiveCompactionArchiveDays_ClampsOutOfRange covers the
// defensive read path — Save validation rejects out-of-range writes,
// but a hand-edited file or a stale client could still surface bad
// numerics. The Effective accessor must clamp gracefully so the sweep
// never runs against a nonsense window.
func TestEffectiveCompactionArchiveDays_ClampsOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		raw  int
		want int
	}{
		{0, DefaultCompactionArchiveDays},
		{-5, DefaultCompactionArchiveDays}, // negative collapses to default per the <=0 branch
		{1, MinCompactionArchiveDays},
		{6, MinCompactionArchiveDays},
		{7, 7},
		{90, 90},
		{365, 365},
		{1000, MaxCompactionArchiveDays},
	} {
		s := Settings{CompactionArchiveDays: tc.raw}
		if got := s.EffectiveCompactionArchiveDays(); got != tc.want {
			t.Errorf("EffectiveCompactionArchiveDays(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestEffectiveCompactionRecentWindow_DefaultsOnNonPositive covers the
// documented default-when-zero-or-negative behaviour.
func TestEffectiveCompactionRecentWindow_DefaultsOnNonPositive(t *testing.T) {
	for _, tc := range []struct {
		raw  int
		want int
	}{
		{0, DefaultCompactionRecentWindow},
		{-1, DefaultCompactionRecentWindow},
		{1, 1},
		{4, 4},
		{20, 20},
	} {
		s := Settings{CompactionRecentWindow: tc.raw}
		if got := s.EffectiveCompactionRecentWindow(); got != tc.want {
			t.Errorf("EffectiveCompactionRecentWindow(%d) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestSaveAll_RejectsInvalidAggressiveness verifies the SAVE-time
// validation rejects unknown tier strings with a typed error. WP06
// acceptance: bad values fail at save, not silently at Effective time.
func TestSaveAll_RejectsInvalidAggressiveness(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bad := Settings{
		CompactionAggressiveness: "ludicrous",
	}
	err = store.SaveAll(bad)
	if !errors.Is(err, ErrInvalidCompactionAggressiveness) {
		t.Errorf("SaveAll(bad tier) error = %v, want ErrInvalidCompactionAggressiveness", err)
	}
}

// TestSaveAll_AcceptsEmptyAggressiveness verifies the empty-string
// tier is treated as "use default" — saved without error and resolved
// to balanced via the Effective accessor on read-back.
func TestSaveAll_AcceptsEmptyAggressiveness(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.SaveAll(Settings{}); err != nil {
		t.Fatalf("SaveAll empty: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.EffectiveCompactionAggressiveness() != compaction.AggressivenessBalanced {
		t.Errorf("after Save(empty): effective tier = %q, want balanced",
			got.EffectiveCompactionAggressiveness())
	}
}

// TestSaveAll_RejectsArchiveDaysOutOfRange verifies the WP06 acceptance
// criterion: clamp rejects values outside [7, 365] at SAVE.
func TestSaveAll_RejectsArchiveDaysOutOfRange(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	for _, bad := range []int{1, 6, 366, 1000} {
		err := store.SaveAll(Settings{CompactionArchiveDays: bad})
		if !errors.Is(err, ErrInvalidCompactionArchiveDays) {
			t.Errorf("SaveAll(archiveDays=%d) error = %v, want ErrInvalidCompactionArchiveDays",
				bad, err)
		}
	}
	// 0 and the boundary values are accepted.
	for _, ok := range []int{0, 7, 90, 365} {
		if err := store.SaveAll(Settings{CompactionArchiveDays: ok}); err != nil {
			t.Errorf("SaveAll(archiveDays=%d) unexpected error: %v", ok, err)
		}
	}
}

// TestSaveAll_RejectsNegativeRecentWindow covers the negative-input
// rejection branch on the recent-window field.
func TestSaveAll_RejectsNegativeRecentWindow(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	err = store.SaveAll(Settings{CompactionRecentWindow: -1})
	if !errors.Is(err, ErrInvalidCompactionRecentWindow) {
		t.Errorf("SaveAll(recentWindow=-1) error = %v, want ErrInvalidCompactionRecentWindow", err)
	}
	if err := store.SaveAll(Settings{CompactionRecentWindow: 0}); err != nil {
		t.Errorf("SaveAll(recentWindow=0) unexpected: %v", err)
	}
	if err := store.SaveAll(Settings{CompactionRecentWindow: 4}); err != nil {
		t.Errorf("SaveAll(recentWindow=4) unexpected: %v", err)
	}
}

// TestFileStore_CompactionFields_RoundTrip verifies the persisted JSON
// shape preserves every compaction field across a Save → Load cycle,
// including the nested ProviderProfileRef wire shape.
func TestFileStore_CompactionFields_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	in := Settings{
		CompactionAggressiveness: string(compaction.AggressivenessAggressive),
		CompactionModel:          ProviderProfileRef{ProviderID: "anthropic", ModelID: "claude-haiku-3-5"},
		CompactionArchiveDays:    30,
		CompactionRecentWindow:   8,
	}
	if err := store.SaveAll(in); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.CompactionAggressiveness != in.CompactionAggressiveness {
		t.Errorf("aggressiveness = %q, want %q",
			got.CompactionAggressiveness, in.CompactionAggressiveness)
	}
	if got.CompactionModel != in.CompactionModel {
		t.Errorf("model = %+v, want %+v", got.CompactionModel, in.CompactionModel)
	}
	if got.CompactionArchiveDays != in.CompactionArchiveDays {
		t.Errorf("archive days = %d, want %d",
			got.CompactionArchiveDays, in.CompactionArchiveDays)
	}
	if got.CompactionRecentWindow != in.CompactionRecentWindow {
		t.Errorf("recent window = %d, want %d",
			got.CompactionRecentWindow, in.CompactionRecentWindow)
	}

	// Disk JSON shape: confirm the camelCase keys are present so the
	// frontend's TypeScript interface matches the wire format.
	raw, err := os.ReadFile(filepath.Join(dir, "kaneaz-harness", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("disk JSON parse: %v", err)
	}
	for _, key := range []string{
		"compactionAggressiveness",
		"compactionModel",
		"compactionArchiveDays",
		"compactionRecentWindow",
	} {
		if _, ok := disk[key]; !ok {
			t.Errorf("disk JSON missing %q", key)
		}
	}
	// Nested ProviderProfileRef keys.
	model, _ := disk["compactionModel"].(map[string]any)
	if _, ok := model["providerId"]; !ok {
		t.Errorf("compactionModel missing providerId")
	}
	if _, ok := model["modelId"]; !ok {
		t.Errorf("compactionModel missing modelId")
	}
}

func TestAPI_StoreAccessor(t *testing.T) {
	api := NewAPI(nil)
	store := api.Store()
	if store == nil {
		t.Fatalf("Store() returned nil")
	}
	if err := store.SaveRoute("/tools"); err != nil {
		t.Fatalf("Store.SaveRoute: %v", err)
	}
	got, _ := api.Get(context.Background())
	if got.LastRoute != "/tools" {
		t.Errorf("Store and API should share state; got %q", got.LastRoute)
	}
}
