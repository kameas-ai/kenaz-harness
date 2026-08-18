-- Synthetic genesis seed corpus for the upgrade-path snapshot chain
-- (upgrade-path-coverage-01PMUG01, spec.md §5.4).
--
-- Applied ONCE, at the genesis tag (v0.63.0), against a freshly-migrated
-- database (every table this file references already exists). The rows
-- inserted here are then carried FORWARD by every later snapshot in the
-- chain — later migrations transform these real rows exactly as they
-- would a user's, which is the entire point of the mechanism (spec §5.4).
--
-- Every value is synthetic, fixed-ID, fixed-timestamp. NO REAL USER DATA
-- (spec §3, "Non-goals" — hard rule, not a preference).
--
-- Covers every surface FR-1 §6.2 item 5 asserts a representative read on:
-- sessions, messages (incl. FTS-searchable text), artifacts +
-- artifact_versions (two children on one artifact — the FR-2 cascade
-- canary described in spec §5.4 / §1.3), branches, workflows +
-- workflow_versions + workflow_runs_cache, scheduled_chat_runs (+
-- history), context_attachments, slash_commands_user, units + unit_edges.

INSERT INTO projects (id, name, description, created_at, updated_at)
VALUES ('seed-project-1', 'Seed Project', 'synthetic seed project', 1700000000000, 1700000000000);

INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position, draft,
    scroll_position, archived_at, system_prompt, context_kind, project_id)
VALUES
    ('seed-session-1', 'Seed Session One', 1700000000000, 1700000000000, 1700000000000, 0, '',
        0, NULL, 'You are a helpful assistant.', 'system', 'seed-project-1'),
    ('seed-session-2', 'Seed Session Two (branch child)', 1700000001000, 1700000001000, 1700000001000, 1, '',
        0, NULL, '', 'system', NULL);

INSERT INTO session_messages (id, session_id, sequence, role, content, tool_calls, created_at)
VALUES
    ('seed-msg-1', 'seed-session-1', 0, 'user', 'What is the kameas upgrade snapshot mechanism?', NULL, 1700000000100),
    ('seed-msg-2', 'seed-session-1', 1, 'assistant', 'It replays committed per-tag dumps through production Open.', NULL, 1700000000200),
    ('seed-msg-3', 'seed-session-1', 2, 'user', 'Great, thanks.', NULL, 1700000000300);

INSERT INTO artifacts (id, session_id, project_id, title, mime_type, content_hash, byte_size,
    source, source_ref_json, scope_kind, created_at)
VALUES ('seed-artifact-1', 'seed-session-1', NULL, 'Seed Artifact', 'text/plain', 'seedhash1', 42,
    'user_pin', '{}', 'session', 1700000000400);

-- Two children on the one artifact: the cascade canary. If a destructive
-- artifacts-rebuild migration (0327, 0332, ...) loses the scratch/restore
-- discipline, these rows vanish silently (spec §1.3) and both the
-- migration's own populated-table test (WP03) and this snapshot's FR-1
-- case (WP02) fail independently.
INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at)
VALUES
    ('seed-artifact-1', 1, 'seedhash1v1', 40, 'text/plain', 'seed v1', '/seed/v1', 1700000000410),
    ('seed-artifact-1', 2, 'seedhash1v2', 42, 'text/plain', 'seed v2', '/seed/v2', 1700000000420);

INSERT INTO branches (id, parent_session_id, child_session_id, kind, status, model_id, provider_id,
    title, task_hint, created_at, updated_at, merged_at, abandoned_at)
VALUES ('seed-branch-1', 'seed-session-1', 'seed-session-2', 'fork', 'active', 'seed-model', 'seed-provider',
    'Seed Branch', '', 1700000000500, 1700000000500, NULL, NULL);

INSERT INTO workflows (id, name, description, yaml_source, version, hash, created_at, updated_at)
VALUES ('seed-workflow-1', 'Seed Workflow', 'synthetic seed workflow', 'name: seed\nsteps: []\n', 1,
    'seedworkflowhash1', 1700000000600, 1700000000600);

INSERT INTO workflow_versions (workflow_id, version, yaml_source, hash, created_at)
VALUES ('seed-workflow-1', 1, 'name: seed\nsteps: []\n', 'seedworkflowhash1', 1700000000600);

INSERT INTO workflow_runs_cache (workflow_id, inputs_hash, output_json, completed_at)
VALUES ('seed-workflow-1', 'seedinputshash1', '{"ok":true}', 1700000000700);

INSERT INTO scheduled_chat_runs (id, name, prompt_template, cron, timezone, model, output_sink,
    enabled, created_at, updated_at)
VALUES ('seed-schedrun-1', 'Seed Scheduled Run', 'Say hello.', '0 9 * * *', 'UTC', 'seed-model',
    'banner', 1, 1700000000800, 1700000000800);

INSERT INTO scheduled_chat_run_history (id, chat_run_id, session_id, status, started_at, ended_at,
    output_snippet, error)
VALUES ('seed-schedrun-hist-1', 'seed-schedrun-1', 'seed-session-1', 'completed', 1700000000900,
    1700000000950, 'Hello!', '');

INSERT INTO context_attachments (id, scope_kind, scope_id, content_source, content, kind, position, created_at)
VALUES ('seed-attach-1', 'session', 'seed-session-1', 'manual', 'seed attachment content', 'system', 0, 1700000001000);

INSERT INTO slash_commands_user (name, scope, project_id, kind, description, when_to_use,
    does_not_handle, model_invokable, payload_path, icon, hidden_from_panel, created_at, updated_at)
VALUES ('seed-cmd', 'user', NULL, 'prompt', 'seed slash command', 'when seeding', 'production traffic',
    0, '/seed/cmd.md', NULL, 0, 1700000001100, 1700000001100);

INSERT INTO units (id, kind, scope, scope_id, classification, version, load_policy, title, body,
    metadata, created_at, updated_at)
VALUES
    ('seed-unit-1', 'doc', 'session', 'seed-session-1', 'personal', 1, 'always', 'Seed Unit One',
        'seed unit body one', '{}', 1700000001200, 1700000001200),
    ('seed-unit-2', 'doc', 'session', 'seed-session-1', 'personal', 1, 'on_demand', 'Seed Unit Two',
        'seed unit body two', '{}', 1700000001300, 1700000001300);

INSERT INTO unit_edges (id, from_id, to_id, kind, version, created_at)
VALUES ('seed-edge-1', 'seed-unit-1', 'seed-unit-2', 'references', 1, 1700000001400);
