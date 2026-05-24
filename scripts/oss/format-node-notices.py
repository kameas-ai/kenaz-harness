#!/usr/bin/env python3
"""Format the JSON output of `license-checker --production --json` into a
markdown list suitable for inclusion in a NOTICES file.

Usage:
    format-node-notices.py path/to/node.raw.json

Reads the file, sorts packages by name, deduplicates by name@version, and
writes a markdown list to stdout. Each line is:

    - **<name>@<version>** — <license>. Repository: <url>

Packages without a license, or with the literal license string "UNKNOWN",
are emitted with a "(license not declared)" marker so a reviewer can chase
them down before release.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: format-node-notices.py <node.raw.json>", file=sys.stderr)
        return 2

    raw_path = Path(sys.argv[1])
    if not raw_path.is_file():
        print(f"error: {raw_path} not found", file=sys.stderr)
        return 1

    try:
        data = json.loads(raw_path.read_text())
    except json.JSONDecodeError as exc:
        print(f"error: {raw_path} is not valid JSON: {exc}", file=sys.stderr)
        return 1

    if not isinstance(data, dict):
        print("error: expected a JSON object keyed by name@version", file=sys.stderr)
        return 1

    # license-checker emits keys like "react@18.2.0" mapping to a dict with
    # "licenses", "repository", "publisher", "path", "licenseFile", etc.
    rows: list[tuple[str, str, str, str]] = []
    for key, meta in sorted(data.items()):
        if not isinstance(meta, dict):
            continue
        name_at_version = key
        # split "@scope/name@version" carefully: the last "@" is the version sep
        if "@" in key:
            split_at = key.rfind("@")
            name = key[:split_at]
            version = key[split_at + 1 :]
        else:
            name = key
            version = ""
        license_field = meta.get("licenses", "UNKNOWN")
        if isinstance(license_field, list):
            license_str = ", ".join(str(l) for l in license_field)
        else:
            license_str = str(license_field)
        if not license_str or license_str.upper() == "UNKNOWN":
            license_str = "(license not declared)"
        repo = meta.get("repository") or meta.get("url") or ""
        rows.append((name, version, license_str, repo))

    # Deduplicate by (name, version): license-checker shouldn't emit dupes for
    # the same key, but defend against future schema changes.
    seen: set[tuple[str, str]] = set()
    unique: list[tuple[str, str, str, str]] = []
    for row in rows:
        ident = (row[0], row[1])
        if ident in seen:
            continue
        seen.add(ident)
        unique.append(row)

    for name, version, license_str, repo in unique:
        if version:
            display_name = f"**{name}@{version}**"
        else:
            display_name = f"**{name}**"
        if repo:
            print(f"- {display_name} — {license_str}. Repository: <{repo}>")
        else:
            print(f"- {display_name} — {license_str}.")

    return 0


if __name__ == "__main__":
    sys.exit(main())
