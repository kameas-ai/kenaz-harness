#!/usr/bin/env bash
# sign-mac.sh — sign a wails-built .app bundle with your local Developer ID
# Application certificate. Run after `wails build -platform darwin/<arch>`.
#
# Requires:
#   - macOS host
#   - A "Developer ID Application: <name> (<team-id>)" cert in your keychain
#   - Optionally: an app-specific Apple ID password for notarization stored
#     in your keychain via `xcrun notarytool store-credentials`
#
# Usage:
#   ./scripts/sign-mac.sh                              # auto-detect .app
#   ./scripts/sign-mac.sh build/bin/kaneaz-harness.app # explicit path
#   NOTARIZE=1 NOTARY_PROFILE=kaneaz ./scripts/sign-mac.sh
#
# Set IDENTITY to override the cert lookup:
#   IDENTITY="Developer ID Application: Alec Feeman (TEAMID)" ./scripts/sign-mac.sh
#
set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
  echo "sign-mac.sh: must run on macOS" >&2
  exit 1
fi

APP_PATH="${1:-}"
if [[ -z "$APP_PATH" ]]; then
  APP_PATH=$(find build/bin -maxdepth 2 -name '*.app' -type d | head -n1 || true)
fi
if [[ -z "$APP_PATH" || ! -d "$APP_PATH" ]]; then
  echo "sign-mac.sh: cannot find .app bundle (looked at build/bin/*.app)" >&2
  echo "Run \`wails build -platform darwin/<arch>\` first, or pass the path." >&2
  exit 1
fi

# Auto-discover identity if not supplied. Picks the first
# "Developer ID Application" cert in the default keychain.
if [[ -z "${IDENTITY:-}" ]]; then
  IDENTITY=$(security find-identity -v -p codesigning \
    | awk -F'"' '/Developer ID Application/ {print $2; exit}')
fi
if [[ -z "$IDENTITY" ]]; then
  echo "sign-mac.sh: no Developer ID Application cert in keychain." >&2
  echo "List candidates: security find-identity -v -p codesigning" >&2
  exit 1
fi

ENTITLEMENTS="${ENTITLEMENTS:-build/darwin/entitlements.plist}"
ENT_FLAG=()
if [[ -f "$ENTITLEMENTS" ]]; then
  ENT_FLAG=(--entitlements "$ENTITLEMENTS")
fi

echo "→ Signing $APP_PATH"
echo "  identity: $IDENTITY"
[[ ${#ENT_FLAG[@]} -gt 0 ]] && echo "  entitlements: $ENTITLEMENTS"

# Sign embedded helpers/frameworks first (deep), then the bundle itself.
codesign --force --deep --options runtime --timestamp \
  "${ENT_FLAG[@]}" \
  --sign "$IDENTITY" \
  "$APP_PATH"

echo "→ Verifying signature"
codesign --verify --deep --strict --verbose=2 "$APP_PATH"
spctl --assess --type execute --verbose=4 "$APP_PATH" || {
  echo "spctl reports the bundle is not yet trusted (expected pre-notarization)."
}

if [[ "${NOTARIZE:-0}" == "1" ]]; then
  PROFILE="${NOTARY_PROFILE:-kaneaz}"
  echo "→ Submitting to Apple notary service (profile=$PROFILE)"

  # Wails outputs the .app uncompressed; notary needs zip/dmg/pkg.
  ZIP="${APP_PATH%.app}.notary.zip"
  /usr/bin/ditto -c -k --keepParent "$APP_PATH" "$ZIP"

  xcrun notarytool submit "$ZIP" \
    --keychain-profile "$PROFILE" \
    --wait

  echo "→ Stapling ticket"
  xcrun stapler staple "$APP_PATH"
  rm -f "$ZIP"

  echo "→ Verifying notarized + stapled"
  spctl --assess --type execute --verbose=4 "$APP_PATH"
fi

echo "✓ done: $APP_PATH"
echo
echo "If you haven't stored notary credentials yet:"
echo "  xcrun notarytool store-credentials kaneaz \\"
echo "    --apple-id alec@example.com \\"
echo "    --team-id YOURTEAMID \\"
echo "    --password APP-SPECIFIC-PASSWORD"
echo
echo "Then re-run with NOTARIZE=1."
