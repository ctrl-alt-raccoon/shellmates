#!/usr/bin/env python3
"""Read-only checks for public documentation: local links, shell syntax and map JSON."""

import json
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parent.parent
FENCE = re.compile(r"^```([^\n]*)\n(.*?)^```[ \t]*$", re.MULTILINE | re.DOTALL)
LINK = re.compile(r"\[[^\]\n]+\]\(([^\s)]+)\)")


def anchors(body):
    result = set()
    counts = {}
    for heading in re.findall(r"^#{1,6}\s+(.+?)\s*#*\s*$", FENCE.sub("", body), re.MULTILINE):
        slug = re.sub(r"[^\w\s-]", "", heading.lower()).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        result.add(slug + (f"-{count}" if count else ""))
    return result


def main():
    files = [ROOT / name for name in ("README.md", "CONTRIBUTING.md", "SECURITY.md", "project.md")]
    files += sorted((ROOT / "docs").glob("*.md"))
    links = shells = 0
    errors = []
    for path in files:
        body = path.read_text(encoding="utf-8")
        for target in LINK.findall(FENCE.sub("", body)):
            parts = urlsplit(target)
            if parts.scheme or parts.netloc:
                continue
            resolved = (path.parent / unquote(parts.path)).resolve() if parts.path else path
            links += 1
            if not resolved.exists():
                errors.append(f"{path.relative_to(ROOT)}: missing target {target}")
            elif parts.fragment and resolved.suffix == ".md":
                if unquote(parts.fragment) not in anchors(resolved.read_text(encoding="utf-8")):
                    errors.append(f"{path.relative_to(ROOT)}: missing anchor {target}")
        for language, snippet in FENCE.findall(body):
            if language.strip() in ("sh", "bash", "shell"):
                shells += 1
                check = subprocess.run(["sh", "-n"], input=snippet, text=True,
                    capture_output=True, timeout=5)
                if check.returncode:
                    errors.append(f"{path.relative_to(ROOT)}: invalid shell example: {check.stderr.strip()}")
    graph = json.loads((ROOT / "docs/architecture/shellmates.architecture.json").read_text(encoding="utf-8"))
    ids = [component["id"] for component in graph["components"]]
    if len(ids) != len(set(ids)) or any(edge[end] not in ids for edge in graph["connections"] for end in ("from", "to")):
        errors.append("Architecture map has duplicate IDs or dangling edges")
    for error in errors:
        print(error, file=sys.stderr)
    print(f"Documentation: {links} local links, {shells} shell examples, architecture JSON; {len(errors)} errors")
    return int(bool(errors))


if __name__ == "__main__":
    sys.exit(main())
