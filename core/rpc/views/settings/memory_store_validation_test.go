package settings

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/modelcalls"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/activity"
)

// The in-memory store (the test fixture most settings tests build on) must
// reject every save the real settings.json store rejects, through both
// SaveAll and Update. Otherwise a test on the memory store can pass a value
// production would refuse (CLAUDE.md blind spot #2: fixtures that bypass
// the layer under test).
func TestMemoryStore_ValidatesLikeFileStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		set  func(*Settings)
	}{
		{"model-call retention", func(s *Settings) { s.ModelCallRetentionDays = modelcalls.MaxRetentionDays + 1 }},
		{"ML local activity retention", func(s *Settings) { s.MLLocalActivityRetentionDays = activity.MaxRetentionDays + 1 }},
		{"branch advisor confidence", func(s *Settings) { s.BranchAdvisorMinConfidence = 2 }},
		{"keyboard shortcut value", func(s *Settings) {
			s.KeyboardShortcuts = map[string]string{"x": strings.Repeat("a", MaxShortcutValueLen+1)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs, err := NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			stores := map[string]SettingsStore{"file": fs, "memory": newMemoryStore()}
			for kind, st := range stores {
				bad := defaultSettings()
				tc.set(&bad)
				if err := st.SaveAll(bad); err == nil {
					t.Errorf("%s store SaveAll accepted an invalid %s", kind, tc.name)
				}
				if err := Update(st, func(s *Settings) error { tc.set(s); return nil }); err == nil {
					t.Errorf("%s store Update accepted an invalid %s", kind, tc.name)
				}
			}
		})
	}
}
