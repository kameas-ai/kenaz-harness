#!/usr/bin/env python3
"""Format the output of `go list -m -json all` into a markdown attribution
list suitable for inclusion in a NOTICES file.

Replaces a `go-licenses report` invocation. We chose not to use go-licenses
because (a) its built-in classifier fails on modules whose LICENSE file
isn't recognized by its regex set, and (b) newer Go toolchains expose the
standard library to license tooling as "non-module packages," which
go-licenses logs as fatal errors. `go list -m -json all` is part of the Go
distribution itself and produces a reliable enumeration.

Usage:
    format-go-notices.py --modules <file.jsonl> --modcache <path>

`--modules` points to the JSONL stream produced by `go list -m -json all`
(one JSON object per line, NOT a JSON array).

`--modcache` is the absolute path to the Go module cache (from
`go env GOMODCACHE`). The script uses it to discover the LICENSE file
inside each module's cached directory; if a license file is found, its
filename and a short note are included in the output. If none is found,
the module is still listed (with a "(license file not located)" marker)
so a reviewer can chase it before release.

Output (markdown, one line per module):

    - **<module>@<version>** — source: <https://<module>>. License file: <filename>.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

# Filenames go-licenses' regex accepts plus a few common variants.
LICENSE_FILENAMES = (
    "LICENSE",
    "LICENSE.md",
    "LICENSE.txt",
    "LICENSE.rst",
    "LICENCE",
    "LICENCE.md",
    "LICENCE.txt",
    "License",
    "License.md",
    "License.txt",
    "license",
    "license.md",
    "license.txt",
    "COPYING",
    "COPYING.md",
    "COPYING.txt",
    "COPYRIGHT",
    "COPYRIGHT.md",
    "COPYRIGHT.txt",
    "UNLICENSE",
    "NOTICE",
    "NOTICE.md",
    "NOTICE.txt",
)


def find_license_file(mod_dir: Path) -> str | None:
    """Locate a LICENSE file inside an unpacked module directory. Returns
    the filename (not full path) if found, otherwise None."""
    if not mod_dir.is_dir():
        return None

    for filename in LICENSE_FILENAMES:
        candidate = mod_dir / filename
        if candidate.is_file():
            return filename

    # Fall back to a case-insensitive scan for anything that starts with
    # "license", "licence", "copying", "copyright", or "notice".
    try:
        for entry in mod_dir.iterdir():
            if entry.is_file():
                lower = entry.name.lower()
                if (
                    lower.startswith(("license", "licence", "copying", "copyright", "notice"))
                    or lower in ("unlicense",)
                ):
                    return entry.name
    except OSError:
        pass

    return None


def iter_modules(modules_jsonl: Path):
    """Yield module dicts from a JSONL file. Each line is an independent
    JSON object. Skips blank lines and lines that don't parse.

    `go list -m -json all` emits objects in a streaming format where each
    object spans multiple lines (pretty-printed). We accumulate lines until
    the running buffer parses successfully, then yield and reset.
    """
    buffer_lines: list[str] = []
    with modules_jsonl.open() as fp:
        for line in fp:
            buffer_lines.append(line)
            text = "".join(buffer_lines).strip()
            if not text:
                continue
            try:
                obj = json.loads(text)
            except json.JSONDecodeError:
                continue
            yield obj
            buffer_lines = []
    # Anything left in the buffer is an incomplete object; skip silently.


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--modules", required=True, type=Path)
    parser.add_argument("--modcache", required=True, type=Path)
    args = parser.parse_args(argv)

    if not args.modules.is_file():
        print(f"error: {args.modules} not found", file=sys.stderr)
        return 1

    modules: list[dict] = []
    for obj in iter_modules(args.modules):
        if not isinstance(obj, dict):
            continue
        if obj.get("Main") is True:
            # Skip the main module being built; it's not a third-party dep.
            continue
        path = obj.get("Path")
        if not path:
            continue
        modules.append(obj)

    # Deduplicate by (path, version) and sort by path for stable output.
    seen: set[tuple[str, str]] = set()
    unique: list[dict] = []
    for obj in modules:
        key = (obj.get("Path", ""), obj.get("Version", ""))
        if key in seen:
            continue
        seen.add(key)
        unique.append(obj)
    unique.sort(key=lambda m: (m.get("Path", ""), m.get("Version", "")))

    for obj in unique:
        path = obj.get("Path", "")
        version = obj.get("Version", "") or "(no version)"
        # `go list -m` reports the version we resolve to; if a replace
        # directive is in effect, .Replace is populated and .Version is the
        # replacement's version. Surface both when divergent.
        replace = obj.get("Replace")
        replace_note = ""
        if isinstance(replace, dict):
            rpath = replace.get("Path", "")
            rversion = replace.get("Version", "")
            if rpath and rpath != path:
                replace_note = f" (replaced by `{rpath}@{rversion}`)"

        # Prefer the authoritative directory from `go list -m -json`
        # (the `Dir` field). If it's absent, the module wasn't unpacked
        # to the cache (e.g., a download failed or the module is a
        # placeholder); fall back to reconstructing the path with Go's
        # module-cache encoding rules (uppercase letters → "!<lower>"
        # for both path and version).
        dir_field = obj.get("Dir")
        if dir_field:
            mod_dir = Path(dir_field)
        else:
            encoded_path = "".join(
                f"!{c.lower()}" if c.isupper() else c for c in path
            )
            encoded_version = "".join(
                f"!{c.lower()}" if c.isupper() else c for c in version
            )
            mod_dir = args.modcache / f"{encoded_path}@{encoded_version}"

        license_file = find_license_file(mod_dir)
        if license_file:
            license_note = f"License file: `{license_file}`."
        elif not dir_field:
            license_note = (
                "License file: (module not unpacked to cache &mdash; "
                "verify build environment ran `go mod download all`)."
            )
        else:
            license_note = "License file: (not located &mdash; review before release)."

        source_url = f"https://{path}"
        print(
            f"- **{path}@{version}**{replace_note} &mdash; source: <{source_url}>. {license_note}"
        )

    if not unique:
        print("_No third-party Go modules detected._")

    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
