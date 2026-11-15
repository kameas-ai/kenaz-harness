#!/bin/sh
# install.sh — install Kenaz Harness from its GitHub Release.
#
#   curl -fsSL https://raw.githubusercontent.com/kameas-ai/kenaz-harness/main/scripts/install.sh | sh
#
# macOS (Apple silicon or Intel): downloads the signed, notarized .dmg and
# copies "Kenaz Harness.app" into /Applications (or ~/Applications when
# /Applications is not writable).
# Linux x64: downloads the AppImage to ~/.local/bin/kenaz-harness.
# Linux Arm64: downloads the tarball and links ~/.local/bin/kenaz-harness.
# Windows: not handled here; download the -setup.exe from the release page.
#
# Every download is checked against the .sha256 file the release publishes
# next to it. Nothing runs as root; nothing is sent anywhere except the
# GitHub downloads themselves.
#
# Environment:
#   KENAZ_HARNESS_VERSION  release tag to install (default: the latest
#                          release, i.e. the newest version promoted to the
#                          stable channel)
#   KENAZ_HARNESS_BIN_DIR  Linux install directory (default ~/.local/bin)
set -eu

REPO="kameas-ai/kenaz-harness"
BASE="https://github.com/${REPO}/releases/download"

say() { printf '%s\n' "$*"; }
die() { printf 'kenaz-harness install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "needs '$1' on PATH"; }

need curl
need uname

os="$(uname -s)"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported CPU architecture: $arch" ;;
esac
case "$os" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS: $os (on Windows, download the -setup.exe from https://github.com/${REPO}/releases/latest)" ;;
esac

version="${KENAZ_HARNESS_VERSION:-}"
if [ -z "$version" ]; then
  # releases/latest skips drafts and prereleases, so this is the newest
  # stable version.
  version="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [ -n "$version" ] || die "could not find the latest release of ${REPO}"
fi
case "$version" in
  v[0-9]*) ;;
  *) die "KENAZ_HARNESS_VERSION must be a release tag like v0.106.3, got '$version'" ;;
esac

case "${os}-${arch}" in
  darwin-*)    asset="kenaz-harness-${version}-${os}-${arch}.dmg" ;;
  linux-amd64) asset="kenaz-harness-${version}-linux-amd64.AppImage" ;;
  linux-arm64) asset="kenaz-harness-${version}-linux-arm64.tar.gz" ;;
esac

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t kenaz-harness)"
mnt=""
cleanup() {
  if [ -n "$mnt" ]; then hdiutil detach -quiet "$mnt" >/dev/null 2>&1 || true; fi
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

say "Downloading ${asset}"
curl -fL --progress-bar -o "${tmp}/${asset}" "${BASE}/${version}/${asset}" ||
  die "download failed: ${BASE}/${version}/${asset}"
curl -fsSL -o "${tmp}/${asset}.sha256" "${BASE}/${version}/${asset}.sha256" ||
  die "checksum download failed: ${BASE}/${version}/${asset}.sha256"

want="$(awk '{print $1; exit}' "${tmp}/${asset}.sha256")"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  got="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
else
  die "needs sha256sum or shasum to check the download"
fi
[ -n "$want" ] && [ "$want" = "$got" ] ||
  die "checksum mismatch for ${asset} (expected ${want:-<empty>}, got ${got})"
say "Checksum OK (${got})"

case "$os" in
  darwin)
    need hdiutil
    mnt="${tmp}/mnt"
    mkdir -p "$mnt"
    hdiutil attach -quiet -nobrowse -readonly -mountpoint "$mnt" "${tmp}/${asset}" ||
      die "could not open ${asset}"
    app="${mnt}/Kenaz Harness.app"
    [ -d "$app" ] || die "${asset} does not contain 'Kenaz Harness.app'"
    dest="/Applications"
    if [ ! -w "$dest" ]; then
      dest="${HOME}/Applications"
      mkdir -p "$dest"
    fi
    if [ -e "${dest}/Kenaz Harness.app" ]; then
      rm -rf "${dest}/Kenaz Harness.app"
    fi
    cp -R "$app" "${dest}/"
    say "Installed Kenaz Harness ${version} to ${dest}/Kenaz Harness.app"
    say "Open it from ${dest}, or run: open \"${dest}/Kenaz Harness.app\""
    ;;
  linux)
    bindir="${KENAZ_HARNESS_BIN_DIR:-${HOME}/.local/bin}"
    mkdir -p "$bindir"
    if [ "$arch" = amd64 ]; then
      install -m 0755 "${tmp}/${asset}" "${bindir}/kenaz-harness"
    else
      need tar
      optdir="${HOME}/.local/opt/kenaz-harness"
      mkdir -p "$optdir"
      tar -xzf "${tmp}/${asset}" -C "$optdir"
      ln -sf "${optdir}/kenaz-harness-${version}-linux-arm64/kenaz-harness" "${bindir}/kenaz-harness"
    fi
    say "Installed Kenaz Harness ${version} as ${bindir}/kenaz-harness"
    say "Linux builds are not code-signed; the checksum above is the integrity check."
    say "It needs WebKitGTK 4.1 (Ubuntu: sudo apt install libwebkit2gtk-4.1-0)."
    case ":${PATH}:" in
      *":${bindir}:"*) ;;
      *) say "Add ${bindir} to your PATH to run 'kenaz-harness' from a shell." ;;
    esac
    ;;
esac
