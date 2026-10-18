package sqlite_test

// snapshot_generation_test.go — which committed snapshot a migration test
// should boot, chosen by the SCHEMA GENERATION its dump carries, not by
// "newest" (units-debt-01UNITD01 review H1/M1).
//
// The artifact tables have three generations:
//
//	pre-1104   CREATE TABLE "artifacts" / artifact_versions   (<= v0.86.0)
//	1104-era   CREATE TABLE "artifacts_legacy" / ..._legacy  (v0.87.0)
//	post-1105  neither                                        (v0.88.0+)
//
// A test that seeds `artifacts` must boot the newest PRE-1104 snapshot; a
// test of 1105's real input must boot the newest 1104-ERA snapshot. Picking
// "newest overall" breaks the first kind the day v0.87.0 is committed, and
// classifying by the ABSENCE of a name misfiles post-1105 snapshots as
// pre-1104. Classification is by PRESENCE of the table's CREATE TABLE.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

// upgradeSnapshotRoot is the committed snapshot chain.
var upgradeSnapshotRoot = filepath.Join("testdata", "upgrade")

// v087SnapshotTag is the first release that shipped units/1104, i.e. the
// first tag whose snapshot must carry artifacts_legacy.
const v087SnapshotTag = "v0.87.0"

// dumpCreatesTable reports whether dump has a CREATE TABLE for exactly
// name (bare, "double"-, `back`- or [bracket]-quoted; IF NOT EXISTS ok).
func dumpCreatesTable(dump, name string) bool {
	re := regexp.MustCompile(`(?mi)^\s*CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?["` + "`" + `\[]?` +
		regexp.QuoteMeta(name) + `["` + "`" + `\]]?\s*\(`)
	return re.MatchString(dump)
}

// snapshotTagsAt returns the vX.Y.Z tags under root that have a dump.sql,
// ascending.
func snapshotTagsAt(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read snapshot dir %s: %v", root, err)
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() && upgradesnap.IsSnapshotTag(e.Name()) {
			if _, err := os.Stat(filepath.Join(root, e.Name(), "dump.sql")); err == nil {
				tags = append(tags, e.Name())
			}
		}
	}
	return upgradesnap.SortedSnapshotTags(tags)
}

// snapshotsCreatingTable returns, ascending, the tags under root whose dump
// creates table.
func snapshotsCreatingTable(t *testing.T, root, table string) []string {
	t.Helper()
	var out []string
	for _, tag := range snapshotTagsAt(t, root) {
		dump, err := os.ReadFile(filepath.Join(root, tag, "dump.sql"))
		if err != nil {
			t.Fatalf("read %s dump: %v", tag, err)
		}
		if dumpCreatesTable(string(dump), table) {
			out = append(out, tag)
		}
	}
	return out
}

// oldestAndNewestPre1104Snapshot returns the oldest and newest snapshots
// under root whose dump still has the pre-1104 `artifacts` table.
func oldestAndNewestPre1104Snapshot(t *testing.T, root string) (string, string) {
	t.Helper()
	tags := snapshotsCreatingTable(t, root, "artifacts")
	if len(tags) == 0 {
		t.Fatalf("no snapshot under %s has the pre-1104 `artifacts` table", root)
	}
	return tags[0], tags[len(tags)-1]
}

// tagAtLeast reports tag >= min in snapshot (semver) order.
func tagAtLeast(tag, min string) bool {
	if tag == min {
		return true
	}
	return upgradesnap.SortedSnapshotTags([]string{tag, min})[1] == tag
}

type v087Action int

const (
	v087Run v087Action = iota
	v087Skip
	v087Fail
)

// v087SnapshotDecision decides what TestMigration1105_V087SnapshotBoots
// does against root: RUN on the newest snapshot that carries
// artifacts_legacy; SKIP only while no snapshot tag >= v0.87.0 exists yet;
// FAIL if one does but none carries artifacts_legacy (the v0.87.0 snapshot
// is missing from the chain or was generated wrong — skipping then would
// hide the gap forever).
func v087SnapshotDecision(t *testing.T, root string) (v087Action, string, string) {
	t.Helper()
	if legacy := snapshotsCreatingTable(t, root, "artifacts_legacy"); len(legacy) > 0 {
		return v087Run, legacy[len(legacy)-1], ""
	}
	tags := snapshotTagsAt(t, root)
	var atLeast []string
	for _, tag := range tags {
		if tagAtLeast(tag, v087SnapshotTag) {
			atLeast = append(atLeast, tag)
		}
	}
	if len(atLeast) == 0 {
		return v087Skip, "", "no snapshot tag >= " + v087SnapshotTag + " is committed yet (newest: " + tags[len(tags)-1] +
			"); this test activates when the v0.87.0 snapshot lands — until then P-1 rides on the reconstructed " +
			"v0.87.0 state (TestMigration1105_PopulatedV087StateDropsWithZeroUnitsDelta)"
	}
	return v087Fail, "", "snapshot tag(s) " + joinTags(atLeast) + " are >= " + v087SnapshotTag +
		" but none carries artifacts_legacy: the 1104-era snapshot is missing from the chain (or was generated " +
		"without units/1104), so 1105 has never booted a real v0.87.0 database"
}

func joinTags(tags []string) string {
	out := ""
	for i, tag := range tags {
		if i > 0 {
			out += ", "
		}
		out += tag
	}
	return out
}

// TestSnapshotGenerationSelection proves the selection against SYNTHETIC
// future chains built in a temp dir (never under testdata/): the
// reviewer's H1 scenario (a post-1105 v0.88.0 dump with neither table),
// the v0.87.0 snapshot landing, and v0.87.0 being skipped in the chain.
func TestSnapshotGenerationSelection(t *testing.T) {
	t.Parallel()
	pre := "CREATE TABLE artifact_versions (\n id INTEGER);\nCREATE TABLE \"artifacts\" (\n id TEXT);\n"
	era := "CREATE TABLE \"artifact_versions_legacy\" (\n id INTEGER);\nCREATE TABLE \"artifacts_legacy\" (\n id TEXT);\n" +
		"CREATE TABLE media_artifacts (\n id TEXT);\n"
	post := "CREATE TABLE artifacts_legacy_orphans (\n id TEXT);\nCREATE TABLE media_artifacts (\n id TEXT);\n" +
		"-- a comment mentioning artifacts_legacy and artifacts\n"
	chain := func(t *testing.T, dumps map[string]string) string {
		root := t.TempDir()
		for tag, d := range dumps {
			if err := os.MkdirAll(filepath.Join(root, tag), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tag, "dump.sql"), []byte(d), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	cases := []struct {
		name       string
		dumps      map[string]string
		wantPre    string
		wantAction v087Action
		wantV087   string
	}{
		{"today: chain ends at v0.86.0", map[string]string{"v0.63.0": pre, "v0.86.0": pre}, "v0.86.0", v087Skip, ""},
		{"v0.87.0 lands", map[string]string{"v0.63.0": pre, "v0.86.0": pre, "v0.87.0": era}, "v0.86.0", v087Run, "v0.87.0"},
		{"v0.88.0 lands too (H1)", map[string]string{"v0.63.0": pre, "v0.86.0": pre, "v0.87.0": era, "v0.88.0": post}, "v0.86.0", v087Run, "v0.87.0"},
		{"v0.87.1 era patch", map[string]string{"v0.86.0": pre, "v0.87.0": era, "v0.87.1": era, "v0.88.0": post}, "v0.86.0", v087Run, "v0.87.1"},
		{"v0.87.0 missing, v0.88.0 present", map[string]string{"v0.63.0": pre, "v0.86.0": pre, "v0.88.0": post}, "v0.86.0", v087Fail, ""},
		{"v0.87.0 generated without 1104", map[string]string{"v0.86.0": pre, "v0.87.0": pre}, "v0.87.0", v087Fail, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := chain(t, tc.dumps)
			oldest, newestPre := oldestAndNewestPre1104Snapshot(t, root)
			if newestPre != tc.wantPre {
				t.Errorf("newest pre-1104 = %s, want %s", newestPre, tc.wantPre)
			}
			if oldest == "v0.88.0" || oldest == "v0.87.0" && tc.dumps["v0.87.0"] == era {
				t.Errorf("oldest pre-1104 = %s — a post-1104 snapshot was misclassified", oldest)
			}
			action, tag, msg := v087SnapshotDecision(t, root)
			if action != tc.wantAction || tag != tc.wantV087 {
				t.Errorf("v0.87.0 decision = (%d, %q, %q), want (%d, %q)", action, tag, msg, tc.wantAction, tc.wantV087)
			}
		})
	}
}
