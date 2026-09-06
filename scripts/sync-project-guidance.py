#!/usr/bin/env python3
"""Refresh Shellmates' project-only native entry files; never install personal rules."""

import argparse
import hashlib
from pathlib import Path
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    body = (root / "project.md").read_text(encoding="utf-8").strip() + "\n"
    header = hashlib.sha256(body.encode("utf-8")).hexdigest()
    expected = f"<!-- agent-harness generated sha256={header}; edit sources -->\n{body}"
    stale = False
    for name in ("CLAUDE.md", "AGENTS.md"):
        target = root / name
        if target.is_symlink():
            raise ValueError(f"Refusing native file symlink: {name}")
        if target.read_text(encoding="utf-8") != expected:
            stale = True
            print(f"{'stale' if args.check else 'refresh'}: {name}")
            if not args.check:
                target.write_text(expected, encoding="utf-8")
    return int(args.check and stale)


if __name__ == "__main__":
    sys.exit(main())
