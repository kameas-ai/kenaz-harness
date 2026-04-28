# Spec: Compaction strategy authoring UI

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`core/agentgraph/compaction/` supports custom strategies via YAML/manifest config (3 sites × 4 strategies cascading; FR-039). Today there is no UI to author or edit them. Power users can drop YAML into `<DataDir>/config/compaction.yaml`, but the harness's premise is that everything is configurable from the UI.

## 2. Goals

- New `/compaction` view exposing per-site strategy chains.
- Visual editor for the cascade: drag to reorder, click to edit per-strategy attrs.
- "Test against current session" affordance — show what compaction would produce given the live message buffer.
- Strategy library: shipped strategies + user-authored custom ones.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | RPC methods: `Compaction.GetEffectiveConfig`, `Compaction.SetSiteConfig`, `Compaction.ListCustomStrategies`, `Compaction.SaveCustomStrategy`. | proposed |
| FR-002 | View has three tabs (one per site: per-call, per-budget, per-tool) with the same cascade editor. | proposed |
| FR-003 | "Test" pane runs `Compaction.ManualOpts` against the session and shows the diff. | proposed |
| FR-004 | Strategy library shows shipped + custom; clicking adds it to the active cascade. | proposed |
| FR-005 | Custom strategy editor with attribute schema (auto-generated from manifest). | proposed |
| FR-006 | Changes take effect on next compaction without restart. | proposed |

## 4. Success criteria

- A user can create a "summarise older than 50 turns" custom strategy and see it active on the next compaction without leaving the UI.
