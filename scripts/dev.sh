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
set -euo pipefail

export KENAZ_HARNESS_ENV="${KENAZ_HARNESS_ENV:-dev}"
exec wails dev "$@"
