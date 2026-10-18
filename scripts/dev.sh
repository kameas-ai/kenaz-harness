#!/usr/bin/env bash
# dev.sh — launch wails dev with the dev-fleet OTLP endpoint wired.
#
# Sets KENAZ_HARNESS_ENV=dev so ResolveProfile() in core/fleet/env.go
# returns the dev EnvProfile and the harness's fleet OTLP pipeline targets
# https://dev.fleet.kameas.ai (harness-fleet-otlp-export-01NTLMEX01 WP06).
#
# Usage:
#   bash scripts/dev.sh            # standard dev run
#   bash scripts/dev.sh -tags foo  # pass extra wails flags
#
# To disable fleet telemetry export during dev (useful for offline work):
#   KENAZ_HARNESS_ENV= bash scripts/dev.sh
# or leave consent at "none" in Settings → Privacy → Fleet telemetry.
#
# The kenaz-ml engine: before launching, scripts/dev-ml.sh seeds an engine
# build into the dev engine root (~/.kenaz/ml/dev) if it can find one — an
# explicit KENAZ_ML_ONEDIR, a frozen sibling kenaz-ml checkout, the latest
# CI artifact (via gh), or KENAZ_ML_BUILD=1 to freeze one. The harness then
# spawns it on the dev lane (base :7785; engine.port records the port) on
# first advisor demand. Nothing there can stop this
# launch: without an engine the harness runs on its client-side heuristics.
# KENAZ_ML_SEED=0 skips the step.
set -euo pipefail

export KENAZ_HARNESS_ENV="${KENAZ_HARNESS_ENV:-dev}"
bash "$(dirname "${BASH_SOURCE[0]}")/dev-ml.sh" || true
exec wails dev "$@"
