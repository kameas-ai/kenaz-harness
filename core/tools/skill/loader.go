// loader.go — installs the bundled skill templates into the user
// command store on first launch (or when missing).
//
// The installer is idempotent: it checks whether a command with the
// given name already exists before writing so user customisations are
// preserved across harness upgrades.
//
// model-invoked-skills-catalog-01KZNP3E WP04.
package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// SkillStore is the narrow write surface the loader needs. *coreslashcmd.Store
// satisfies this interface.
type SkillStore interface {
	// LoadUserOne returns the command if it exists, ErrCommandNotFound otherwise.
	LoadUserOne(ctx context.Context, name, projectID string) (coreslashcmd.UserCommand, error)
	// SaveUser persists the command atomically.
	SaveUser(ctx context.Context, cmd coreslashcmd.UserCommand) error
}

// InstallBundledSkills reads every *.md file from EmbeddedSkills and
// saves it to the store when no command with that name already exists.
// Existing commands are left untouched so user edits survive upgrades.
//
// Errors from individual skill installs are collected and returned as a
// combined error so a single bad template does not abort the whole batch.
func InstallBundledSkills(ctx context.Context, store SkillStore) error {
	if store == nil {
		return nil
	}

	entries, err := fs.Glob(EmbeddedSkills, "skills/*.md")
	if err != nil {
		return fmt.Errorf("skill.loader: glob embedded: %w", err)
	}

	var errs []error
	for _, path := range entries {
		data, readErr := EmbeddedSkills.ReadFile(path)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("skill.loader: read %s: %w", path, readErr))
			continue
		}

		cmd, parseErr := coreslashcmd.ParseMarkdown(data)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("skill.loader: parse %s: %w", path, parseErr))
			continue
		}

		// Ensure model_invokable=true on every bundled skill regardless of
		// what the frontmatter declares (belt-and-suspenders).
		cmd.ModelInvokable = true
		if cmd.Scope == "" {
			cmd.Scope = coreslashcmd.ScopeGlobal
		}

		// Skip if already installed.
		_, lookupErr := store.LoadUserOne(ctx, cmd.Name, "")
		if lookupErr == nil {
			// Already exists — leave user's version untouched.
			continue
		}
		if !errors.Is(lookupErr, coreslashcmd.ErrCommandNotFound) {
			errs = append(errs, fmt.Errorf("skill.loader: check %s: %w", cmd.Name, lookupErr))
			continue
		}

		if saveErr := store.SaveUser(ctx, cmd); saveErr != nil {
			errs = append(errs, fmt.Errorf("skill.loader: save %s: %w", cmd.Name, saveErr))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
