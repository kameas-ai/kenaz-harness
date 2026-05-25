# Changelog

## [Unreleased]

### Changed

- **2026-05-16 — Workbench host-rendered pivot.** The workbench programme pivoted from in-VM rendered UI to host-rendered. kenaz-harness gains an in-VM agent task surface (PIVOT_PLAN Phase 8): the harness runs inside the sandbox VM and exposes a task RPC to the host orchestrator over vsock. Canonical day-to-day plan: [`PIVOT_PLAN.md`](../PIVOT_PLAN.md) in the workspace repo. Architectural rationale: [ADR-workbench-host-rendered-pivot](../.specify/decisions/ADR-workbench-host-rendered-pivot.md). Pre-pivot state preserved at tag `v1.0.0-pivot-baseline` (v0.16.1) for rollback.
