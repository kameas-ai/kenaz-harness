package toolloop

// Prompt-skip set + tool-family classifier tests
// (confirm-each-enforcement-01PMAG05 WP04).

import (
	"reflect"
	"testing"
)

func TestClassifyToolFamily(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool string
		want string
	}{
		// Reads.
		{"read_file", FamilyRead},
		{"readFile", FamilyRead},
		{"list_directory", FamilyRead},
		{"get_issue", FamilyRead},
		{"search_repositories", FamilyRead},
		{"grep", FamilyRead},

		// Writes.
		{"write_file", FamilyWrite},
		{"create_issue", FamilyWrite},
		{"update_pull_request", FamilyWrite},
		{"mkdir", FamilyWrite},
		{"editFile", FamilyWrite},

		// Network.
		{"http_request", FamilyNetwork},
		{"download_url", FamilyNetwork},
		{"browse_page", FamilyNetwork},
		{"send_email", FamilyNetwork},

		// Shell-safe is a curated, whole-name allowlist of
		// inspection-only tools — NOT "anything shell-shaped".
		{"echo", FamilyShellSafe},
		{"pwd", FamilyShellSafe},
		{"whoami", FamilyShellSafe},
		{"Uname", FamilyShellSafe}, // case-insensitive whole-name match

		// General execution is deliberately UNCLASSIFIED. Review
		// finding: with a segment table these all classified
		// "shell-safe", so the bold and autonomous tiers (whose presets
		// name shell-safe) auto-approved arbitrary shell execution and
		// arbitrary SQL without a prompt. Unknown is never skippable, so
		// they now prompt at every tier.
		{"bash", FamilyUnknown},
		{"sh", FamilyUnknown},
		{"exec", FamilyUnknown},
		{"run_command", FamilyUnknown},
		{"shell_exec", FamilyUnknown},
		{"spawn_process", FamilyUnknown},
		{"execute_sql", FamilyUnknown},
		{"terminal", FamilyUnknown},
		// Whole-name only: an allowlisted word plus anything else is a
		// different tool and does not inherit the grant.
		{"echo_to_file", FamilyUnknown},
		{"my_echo_wrapper", FamilyUnknown},
		{"save_echo", FamilyWrite}, // still classified by its other segments

		// "query" is not a read verb. `run_query` accepts a statement
		// the caller supplies, so classifying it read let DELETE FROM
		// skip the prompt at the cautious tier (which auto-approves
		// reads).
		{"run_query", FamilyUnknown},
		{"execute_query", FamilyUnknown},
		{"db_query", FamilyUnknown},

		// Destructive.
		{"delete_file", FamilyDestructive},
		{"rm", FamilyDestructive},
		{"drop_table", FamilyDestructive},
		{"purge_cache", FamilyDestructive},

		// Unclassifiable — the fail-closed default.
		{"t", FamilyUnknown},
		{"my_tool", FamilyUnknown},
		{"", FamilyUnknown},
		{"frobnicate", FamilyUnknown},
	}
	for _, tc := range cases {
		if got := ClassifyToolFamily("srv", tc.tool); got != tc.want {
			t.Errorf("ClassifyToolFamily(%q) = %q, want %q", tc.tool, got, tc.want)
		}
	}
}

// A name matching several families resolves to the most dangerous, so a
// user who auto-approved writes does not silently auto-approve deletes.
func TestClassifyToolFamily_PriorityIsMostDangerous(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool string
		want string
	}{
		{"delete_created_file", FamilyDestructive}, // destructive > write
		{"remove_and_read", FamilyDestructive},     // destructive > read
		{"echo_delete_all", FamilyDestructive},     // destructive > the shell-safe allowlist
		{"http_get", FamilyNetwork},                // network > read
		{"create_and_list", FamilyWrite},           // write > read
	}
	for _, tc := range cases {
		if got := ClassifyToolFamily("srv", tc.tool); got != tc.want {
			t.Errorf("ClassifyToolFamily(%q) = %q, want %q", tc.tool, got, tc.want)
		}
	}
}

func TestSplitIdentifier(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want []string
	}{
		{"read_file", []string{"read", "file"}},
		{"readFileSync", []string{"read", "file", "sync"}},
		{"kenaz__save_artifact", []string{"kenaz", "save", "artifact"}},
		{"fs.readdir", []string{"fs", "readdir"}},
		{"", nil},
	}
	for _, tc := range cases {
		if got := splitIdentifier(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitIdentifier(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// The zero value skips nothing: an unconfigured session confirms every
// confirm_each verdict.
func TestPromptSkipSet_NilSkipsNothing(t *testing.T) {
	t.Parallel()

	var s PromptSkipSet
	for _, f := range []string{FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork, FamilyDestructive} {
		if s.Has(f) {
			t.Errorf("nil set has %q", f)
		}
	}
	if s.SkipsTool("srv", "read_file") {
		t.Error("nil set skipped a read tool")
	}
	if got := s.Sorted(); len(got) != 0 {
		t.Errorf("nil set Sorted() = %v, want empty", got)
	}
}

// "I could not classify this tool" must never become "so allow it".
func TestPromptSkipSet_UnknownIsNeverSkippable(t *testing.T) {
	t.Parallel()

	s := NewPromptSkipSet(FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork, FamilyDestructive, FamilyUnknown)
	if s.Has(FamilyUnknown) {
		t.Fatal("FamilyUnknown entered the skip set")
	}
	if s.SkipsTool("srv", "frobnicate") {
		t.Fatal("an unclassifiable tool skipped the prompt under an everything-approved set")
	}
	if got, want := len(s.Sorted()), 5; got != want {
		t.Fatalf("set size = %d, want %d", got, want)
	}

	// Add is equally strict.
	var empty PromptSkipSet
	if empty.Add(FamilyUnknown) != nil {
		t.Fatal("Add(FamilyUnknown) created a set")
	}
	if empty.Add("nonsense") != nil {
		t.Fatal("Add(nonsense) created a set")
	}
}

func TestPromptSkipSet_PerFamilySkip(t *testing.T) {
	t.Parallel()

	readOnly := NewPromptSkipSet(FamilyRead)
	if !readOnly.SkipsTool("fs", "read_file") {
		t.Error("read-only set did not skip a read tool")
	}
	if readOnly.SkipsTool("fs", "write_file") {
		t.Error("read-only set skipped a write tool — the skip must be per-family")
	}
	if readOnly.SkipsTool("fs", "delete_file") {
		t.Error("read-only set skipped a destructive tool")
	}

	if got, want := readOnly.Sorted(), []string{FamilyRead}; !reflect.DeepEqual(got, want) {
		t.Errorf("Sorted() = %v, want %v", got, want)
	}

	// Destructive is reachable only by explicit addition (the
	// cedar-only posture), never by naming the other four.
	all := NewPromptSkipSet(FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork)
	if all.SkipsTool("fs", "delete_file") {
		t.Error("the four canonical families implied destructive")
	}
	if !all.Add(FamilyDestructive).SkipsTool("fs", "delete_file") {
		t.Error("explicitly added destructive did not skip a destructive tool")
	}
}

func TestPromptSkipSet_Sorted(t *testing.T) {
	t.Parallel()

	s := NewPromptSkipSet(FamilyWrite, FamilyRead, FamilyDestructive)
	want := []string{FamilyDestructive, FamilyRead, FamilyWrite}
	if got := s.Sorted(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sorted() = %v, want %v", got, want)
	}
}
