package slashcmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
)

// ErrCommandNotFound is returned when a user command lookup yields no row.
var ErrCommandNotFound = errors.New("slashcmd: user command not found")

// Store is the read/write gateway for user-defined slash commands.
// It writes markdown files to disk and keeps the slash_commands_user
// SQLite table in sync.
//
// Two-surface invariant: SaveUser writes the markdown file first, then
// the DB row (inside a transaction). If the file write fails the row is
// never touched. If the DB transaction fails the file is removed.
// LoadUser re-syncs a stale row when the file's mtime is newer than the
// row's updated_at.
type Store struct {
	db      storage.DB
	dataDir string
}

// NewStore constructs a Store. dataDir is the harness data directory
// (the same value passed to storage.Open).
func NewStore(db storage.DB, dataDir string) *Store {
	return &Store{db: db, dataDir: dataDir}
}

// globalDir returns the path to the global slash_commands directory.
func (s *Store) globalDir() string {
	return filepath.Join(s.dataDir, "slash_commands")
}

// projectDir returns the path to the project-scoped slash_commands directory.
func (s *Store) projectDir(projectID string) string {
	return filepath.Join(s.dataDir, "projects", projectID, "slash_commands")
}

// payloadPath returns the on-disk path for the given command's markdown file.
func (s *Store) payloadPath(cmd UserCommand) string {
	if cmd.Scope == ScopeProject {
		return filepath.Join(s.projectDir(cmd.ProjectID), cmd.Name+".md")
	}
	return filepath.Join(s.globalDir(), cmd.Name+".md")
}

// relativePayloadPath returns the PayloadPath stored in the DB row —
// relative to dataDir.
func (s *Store) relativePayloadPath(cmd UserCommand) string {
	full := s.payloadPath(cmd)
	rel, err := filepath.Rel(s.dataDir, full)
	if err != nil {
		return full
	}
	return rel
}

// LoadUser returns all user commands visible to the given projectID.
// Global commands are always included; project commands are included
// only when their project_id matches. Project commands shadow global
// commands of the same name.
//
// If a command's markdown file mtime is newer than the row's updated_at,
// the row is refreshed from disk before being returned.
func (s *Store) LoadUser(ctx context.Context, projectID string) ([]UserCommand, error) {
	rows, err := s.db.Reader().Query(ctx, `
		SELECT name, scope, project_id, kind, description, when_to_use, does_not_handle,
		       model_invokable, payload_path, icon, hidden_from_panel, created_at, updated_at
		FROM slash_commands_user
		WHERE scope = 'global' OR (scope = 'project' AND project_id = ?)
		ORDER BY scope DESC, name ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("slashcmd: LoadUser query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Collect rows; project-scoped commands shadow global ones.
	byName := make(map[string]UserCommand)
	for rows.Next() {
		var cmd UserCommand
		var projID *string
		var icon *string
		var modelInvokable int
		var hiddenFromPanel int
		if err := rows.Scan(
			&cmd.Name, &cmd.Scope, &projID, &cmd.Kind,
			&cmd.Description, &cmd.WhenToUse, &cmd.DoesNotHandle,
			&modelInvokable, &cmd.PayloadPath, &icon,
			&hiddenFromPanel, &cmd.CreatedAt, &cmd.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("slashcmd: LoadUser scan: %w", err)
		}
		if projID != nil {
			cmd.ProjectID = *projID
		}
		if icon != nil {
			cmd.Icon = *icon
		}
		cmd.ModelInvokable = modelInvokable != 0
		cmd.HiddenFromPanel = hiddenFromPanel != 0

		// Project-scoped overrides global on name collision.
		existing, hasExisting := byName[cmd.Name]
		if hasExisting && existing.Scope == ScopeProject {
			// Already have a project command; keep it.
			continue
		}
		byName[cmd.Name] = cmd
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("slashcmd: LoadUser rows: %w", err)
	}

	// Load body from disk; refresh stale rows.
	out := make([]UserCommand, 0, len(byName))
	for _, cmd := range byName {
		fullPath := filepath.Join(s.dataDir, cmd.PayloadPath)
		info, statErr := os.Stat(fullPath)
		if statErr == nil {
			fileMtime := info.ModTime().Unix()
			if fileMtime > cmd.UpdatedAt {
				// Re-parse and refresh the DB row.
				refreshed, parseErr := s.refreshFromDisk(ctx, fullPath, cmd)
				if parseErr == nil {
					cmd = refreshed
				}
			} else {
				// Parse body + tool fields without refreshing.
				data, readErr := os.ReadFile(fullPath)
				if readErr == nil {
					parsed, parseErr := ParseMarkdown(data)
					if parseErr == nil {
						cmd.Body = parsed.Body
						cmd.Tool = parsed.Tool
						cmd.ToolArgsTemplate = parsed.ToolArgsTemplate
						cmd.Inputs = parsed.Inputs
					}
				}
			}
		}
		out = append(out, cmd)
	}
	return out, nil
}

// LoadUserOne loads a single user command by name, scoped to the given
// projectID (which may be empty for global-only lookup).
func (s *Store) LoadUserOne(ctx context.Context, name, projectID string) (UserCommand, error) {
	row := s.db.Reader().QueryRow(ctx, `
		SELECT name, scope, project_id, kind, description, when_to_use, does_not_handle,
		       model_invokable, payload_path, icon, hidden_from_panel, created_at, updated_at
		FROM slash_commands_user
		WHERE name = ?
		  AND (scope = 'global' OR (scope = 'project' AND project_id = ?))
		ORDER BY CASE WHEN scope = 'project' THEN 0 ELSE 1 END
		LIMIT 1
	`, name, projectID)

	var cmd UserCommand
	var projID *string
	var icon *string
	var modelInvokable int
	var hiddenFromPanel int
	err := row.Scan(
		&cmd.Name, &cmd.Scope, &projID, &cmd.Kind,
		&cmd.Description, &cmd.WhenToUse, &cmd.DoesNotHandle,
		&modelInvokable, &cmd.PayloadPath, &icon,
		&hiddenFromPanel, &cmd.CreatedAt, &cmd.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserCommand{}, fmt.Errorf("%w: %q", ErrCommandNotFound, name)
		}
		return UserCommand{}, fmt.Errorf("slashcmd: LoadUserOne scan: %w", err)
	}
	if projID != nil {
		cmd.ProjectID = *projID
	}
	if icon != nil {
		cmd.Icon = *icon
	}
	cmd.ModelInvokable = modelInvokable != 0
	cmd.HiddenFromPanel = hiddenFromPanel != 0

	// Load body + tool fields from disk (tool/tool_args_template/inputs
	// are only stored in the markdown frontmatter, not in the DB row).
	fullPath := filepath.Join(s.dataDir, cmd.PayloadPath)
	data, readErr := os.ReadFile(fullPath)
	if readErr == nil {
		parsed, parseErr := ParseMarkdown(data)
		if parseErr == nil {
			cmd.Body = parsed.Body
			cmd.Tool = parsed.Tool
			cmd.ToolArgsTemplate = parsed.ToolArgsTemplate
			cmd.Inputs = parsed.Inputs
			cmd.WhenToUse = parsed.WhenToUse
			cmd.DoesNotHandle = parsed.DoesNotHandle
		}
	}
	return cmd, nil
}

// SaveUser persists cmd atomically: the markdown file is written first,
// then the DB row is upserted in a transaction. If either step fails
// both surfaces are left consistent.
func (s *Store) SaveUser(ctx context.Context, cmd UserCommand) error {
	if err := Validate(cmd); err != nil {
		return fmt.Errorf("slashcmd: SaveUser validate: %w", err)
	}

	now := time.Now().Unix()
	if cmd.CreatedAt == 0 {
		cmd.CreatedAt = now
	}
	cmd.UpdatedAt = now
	cmd.PayloadPath = s.relativePayloadPath(cmd)

	// Write the markdown file first.
	fullPath := filepath.Join(s.dataDir, cmd.PayloadPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("slashcmd: SaveUser mkdir: %w", err)
	}
	data, err := MarshalMarkdown(cmd)
	if err != nil {
		return fmt.Errorf("slashcmd: SaveUser marshal: %w", err)
	}

	// Write to a temp file then rename for atomicity.
	tmp := fullPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("slashcmd: SaveUser write tmp: %w", err)
	}

	// Upsert DB row inside a transaction.
	var projID *string
	if cmd.ProjectID != "" {
		projID = &cmd.ProjectID
	}
	var icon *string
	if cmd.Icon != "" {
		icon = &cmd.Icon
	}
	txErr := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, execErr := tx.Exec(ctx, `
			INSERT INTO slash_commands_user
				(name, scope, project_id, kind, description, when_to_use, does_not_handle,
				 model_invokable, payload_path, icon, hidden_from_panel, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				scope            = excluded.scope,
				project_id       = excluded.project_id,
				kind             = excluded.kind,
				description      = excluded.description,
				when_to_use      = excluded.when_to_use,
				does_not_handle  = excluded.does_not_handle,
				model_invokable  = excluded.model_invokable,
				payload_path     = excluded.payload_path,
				icon             = excluded.icon,
				hidden_from_panel= excluded.hidden_from_panel,
				updated_at       = excluded.updated_at
		`,
			cmd.Name, string(cmd.Scope), projID, string(cmd.Kind),
			cmd.Description, cmd.WhenToUse, cmd.DoesNotHandle,
			boolToInt(cmd.ModelInvokable), cmd.PayloadPath, icon,
			boolToInt(cmd.HiddenFromPanel), cmd.CreatedAt, cmd.UpdatedAt,
		)
		return execErr
	})
	if txErr != nil {
		// DB write failed; remove tmp file.
		_ = os.Remove(tmp)
		return fmt.Errorf("slashcmd: SaveUser db upsert: %w", txErr)
	}

	// DB succeeded — commit the file rename.
	if err := os.Rename(tmp, fullPath); err != nil {
		// File rename failed after DB commit. The row now points to a
		// path that doesn't exist. Try to roll back the DB row.
		_ = s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
			_, delErr := tx.Exec(ctx, `DELETE FROM slash_commands_user WHERE name = ?`, cmd.Name)
			return delErr
		})
		return fmt.Errorf("slashcmd: SaveUser rename: %w", err)
	}
	return nil
}

// DeleteUser removes the user command with the given name + projectID
// (empty projectID means global).
func (s *Store) DeleteUser(ctx context.Context, name, projectID string) error {
	// Load current record to get the payload path.
	cmd, err := s.LoadUserOne(ctx, name, projectID)
	if err != nil {
		return fmt.Errorf("slashcmd: DeleteUser lookup: %w", err)
	}

	fullPath := filepath.Join(s.dataDir, cmd.PayloadPath)

	// Remove the DB row first.
	txErr := s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, execErr := tx.Exec(ctx, `
			DELETE FROM slash_commands_user
			WHERE name = ? AND (scope = 'global' OR (scope = 'project' AND project_id = ?))
		`, name, projectID)
		return execErr
	})
	if txErr != nil {
		return fmt.Errorf("slashcmd: DeleteUser db delete: %w", txErr)
	}

	// Remove the markdown file. Best-effort: log if it fails but don't
	// error — the DB row is already gone so the command is effectively
	// deleted from the user's perspective.
	_ = os.Remove(fullPath)
	return nil
}

// ListModelInvokable returns all user commands with model_invokable = true,
// visible across all projects (used by the skills catalog).
func (s *Store) ListModelInvokable(ctx context.Context) ([]UserCommand, error) {
	rows, err := s.db.Reader().Query(ctx, `
		SELECT name, scope, project_id, kind, description, when_to_use, does_not_handle,
		       model_invokable, payload_path, icon, hidden_from_panel, created_at, updated_at
		FROM slash_commands_user
		WHERE model_invokable = 1
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("slashcmd: ListModelInvokable query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []UserCommand
	for rows.Next() {
		var cmd UserCommand
		var projID *string
		var icon *string
		var modelInvokable int
		var hiddenFromPanel int
		if err := rows.Scan(
			&cmd.Name, &cmd.Scope, &projID, &cmd.Kind,
			&cmd.Description, &cmd.WhenToUse, &cmd.DoesNotHandle,
			&modelInvokable, &cmd.PayloadPath, &icon,
			&hiddenFromPanel, &cmd.CreatedAt, &cmd.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("slashcmd: ListModelInvokable scan: %w", err)
		}
		if projID != nil {
			cmd.ProjectID = *projID
		}
		if icon != nil {
			cmd.Icon = *icon
		}
		cmd.ModelInvokable = modelInvokable != 0
		cmd.HiddenFromPanel = hiddenFromPanel != 0
		out = append(out, cmd)
	}
	return out, rows.Err()
}

// refreshFromDisk re-parses the markdown file at fullPath, updates the
// DB row with freshened metadata, and returns the updated UserCommand.
func (s *Store) refreshFromDisk(ctx context.Context, fullPath string, existing UserCommand) (UserCommand, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return existing, err
	}
	parsed, err := ParseMarkdown(data)
	if err != nil {
		return existing, err
	}
	// Merge: prefer disk frontmatter over stale DB row, but keep
	// payload_path / created_at / updated_at from the row.
	parsed.PayloadPath = existing.PayloadPath
	parsed.CreatedAt = existing.CreatedAt
	info, statErr := os.Stat(fullPath)
	if statErr == nil {
		parsed.UpdatedAt = info.ModTime().Unix()
	} else {
		parsed.UpdatedAt = existing.UpdatedAt
	}

	// Update the DB row.
	var projID *string
	if parsed.ProjectID != "" {
		projID = &parsed.ProjectID
	}
	var icon *string
	if parsed.Icon != "" {
		icon = &parsed.Icon
	}
	_ = s.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, execErr := tx.Exec(ctx, `
			UPDATE slash_commands_user SET
				scope            = ?,
				project_id       = ?,
				kind             = ?,
				description      = ?,
				when_to_use      = ?,
				does_not_handle  = ?,
				model_invokable  = ?,
				icon             = ?,
				hidden_from_panel= ?,
				updated_at       = ?
			WHERE name = ?
		`,
			string(parsed.Scope), projID, string(parsed.Kind),
			parsed.Description, parsed.WhenToUse, parsed.DoesNotHandle,
			boolToInt(parsed.ModelInvokable), icon,
			boolToInt(parsed.HiddenFromPanel), parsed.UpdatedAt,
			parsed.Name,
		)
		return execErr
	})
	return parsed, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
