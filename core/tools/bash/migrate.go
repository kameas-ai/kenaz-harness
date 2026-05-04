// Package bash — migrate.go provides the first-boot bash-allowlist →
// Cedar policy migration (WP10 of mission cedar-credential-policy-01KQ8TDE).
//
// # Background
//
// Prior to cedar WP03 the harness gated every bash invocation through a
// simple string-slice allowlist (DefaultAllowlist) that was deleted in
// commit f1aba9a when routing moved to Cedar.  WP10 ensures that a user
// who had implicitly relied on those defaults still has equivalent Cedar
// "permit" snippets in their <DataDir>/policy/ directory the first time
// they run the cedar-enabled harness.
//
// # Idempotency
//
// The migration is guarded by Settings.BashAllowlistMigrated (WP08).
// Once the flag is set, subsequent boots skip the migration without
// touching the filesystem.
//
// # Failure model
//
// Per-pattern WritePolicySnippet failures abort the migration and leave
// BashAllowlistMigrated false so the next boot retries.  The caller
// (Core.Start) logs at warn and continues; boot is never blocked.
package bash

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// historicalDefaultAllowlist is a snapshot of the DefaultAllowlist slice
// that existed in core/tools/bash/allowlist.go before it was removed in
// cedar WP03 (commit f1aba9a).  Embedded verbatim here so the migration
// runs even after the source file is gone.
//
// 26 patterns: read-only inspection tools, common interpreters, and
// language-level build tooling.  No destructive tools (rm, dd, kill, mv).
var historicalDefaultAllowlist = []string{
	"ls", "cat", "head", "tail", "grep", "find", "wc", "file", "stat",
	"du", "df", "which", "type", "echo", "pwd", "env", "date", "uname",
	"git", "python", "python3", "node", "go", "cargo", "npm", "npx",
	"make", "gcc", "clang", "ruby", "rustc",
}

// nonAlnum matches any character that is not a lowercase ASCII letter or
// ASCII digit.  Used by sanitizeName to build a Cedar-safe filename stem.
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeName converts a bash command pattern to a Cedar snippet filename
// that satisfies the CedarPolicyAPI filename regex:
//
//	^[a-z][a-z0-9_]{0,127}\.cedar$
//
// Transformation:
//  1. Lower-case (patterns are already lowercase but be defensive).
//  2. Replace runs of non-alphanumeric characters with "_".
//  3. Prefix with "bash_allow_" so the stem starts with a letter and
//     the filenames are grouped lexicographically.
//  4. Append ".cedar".
func sanitizeName(pattern string) string {
	lower := strings.ToLower(pattern)
	safe := nonAlnum.ReplaceAllString(lower, "_")
	// Trim leading/trailing underscores that may have been introduced.
	safe = strings.Trim(safe, "_")
	if safe == "" {
		safe = "unknown"
	}
	stem := "bash_allow_" + safe
	// Clamp to 128 chars maximum for the stem (1 start char + 127 tail).
	if len(stem) > 128 {
		stem = stem[:128]
	}
	return stem + ".cedar"
}

// SnippetWriter is the subset of cedarpolicy.CedarPolicyAPI that the
// migration needs.  Defined here (instead of importing the cedarpolicy
// package) so the bash package does not take a circular dependency on
// the rpc/views layer.  The real cedarpolicy.API satisfies this interface.
type SnippetWriter interface {
	WritePolicySnippet(ctx context.Context, name string, body string) error
}

// MigrationStore is the subset of settings.SettingsStore that the
// migration reads and writes.  Same anti-circularity rationale as
// SnippetWriter above.
type MigrationStore interface {
	LoadBashAllowlistMigrated() (bool, error)
	SaveBashAllowlistMigrated(migrated bool) error
}

// MigrateBashAllowlist converts every entry in historicalDefaultAllowlist
// into a per-pattern Cedar permit snippet and marks the migration complete.
//
// Idempotency: if store.LoadBashAllowlistMigrated() returns true, the
// function returns nil immediately without writing any files.
//
// Failure model: if any WritePolicySnippet call fails, the error is
// returned immediately (partial snippets are left on disk; Cedar will
// pick them up and any missing ones will be retried next boot because
// BashAllowlistMigrated remains false).
//
// NFR: ~26 patterns complete well within the 1 s wall-time budget.
func MigrateBashAllowlist(ctx context.Context, snippets SnippetWriter, store MigrationStore) error {
	done, err := store.LoadBashAllowlistMigrated()
	if err != nil {
		return fmt.Errorf("bash.migrate: check migrated flag: %w", err)
	}
	if done {
		return nil // idempotent — already ran
	}

	for _, pattern := range historicalDefaultAllowlist {
		name := sanitizeName(pattern)
		body := fmt.Sprintf(
			"permit(principal, action == Action::\"run_bash_command\", resource == BashCommand::\"%s\");\n",
			pattern,
		)
		if err := snippets.WritePolicySnippet(ctx, name, body); err != nil {
			return fmt.Errorf("bash.migrate: write snippet for %q: %w", pattern, err)
		}
	}

	if err := store.SaveBashAllowlistMigrated(true); err != nil {
		return fmt.Errorf("bash.migrate: save migrated flag: %w", err)
	}

	slog.Info("bash.migrate: allowlist migrated to Cedar snippets",
		"patterns", len(historicalDefaultAllowlist),
	)
	return nil
}
