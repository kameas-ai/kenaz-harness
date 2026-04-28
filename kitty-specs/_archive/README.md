# Spec archive

Completed missions move here so the active list in `kitty-specs/` stays
focused on in-flight work without losing the historical record.

## Convention

- Mission directory: `<mission-slug>-<mid8>/` (unchanged from active form).
- Move with `git mv kitty-specs/<slug-mid8>/ kitty-specs/_archive/<slug-mid8>/`
  so file history follows.
- Once archived, the mission is read-only. Re-opening means moving back to
  `kitty-specs/<slug-mid8>/`.
- The runtime resolver (`spec-kitty next`, `spec-kitty mission ...`) only
  walks top-level entries in `kitty-specs/`, so anything under `_archive/`
  is invisible to the active workflow.

## When to archive

A mission is ready to archive when:
- All work packages merged into the target branch (`mission_number` was
  assigned at merge time — it's now a real integer, not `null`).
- Acceptance review signed off (or explicitly waived).
- No active follow-up specs depend on it staying live.

When in doubt, leave it in `kitty-specs/`. Archive is the cleanup step,
not a status flag.

## Recovery

```bash
# List archived missions
ls kitty-specs/_archive/

# Re-open an archived mission
git mv kitty-specs/_archive/<slug-mid8>/ kitty-specs/<slug-mid8>/
```

## Audit trail

Archive moves are commits like any other content move. `git log
--follow kitty-specs/_archive/<slug>/spec.md` reconstructs the
mission's full history including its time as an active spec.
