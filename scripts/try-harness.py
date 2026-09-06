#!/usr/bin/env python3
"""Prepare a fresh, project-scoped Shellmates harness trial; preview unless --apply."""

import argparse
from pathlib import Path
import shlex
import subprocess
import sys

PROJECT = """# Shellmates harness trial

This disposable repository tests native Claude/Codex instruction and skill discovery
through Shellmates. It is not an application and has no application test suite.
Personal defaults and the handover/cross-review skills come from the explicitly
selected agent-harness checkout; do not create competing copies of those sources.

For the initial smoke test, report the instruction sources and available shared
skills. Do not edit files, call a reviewer, or claim that a real review has run.
Only save HANDOVER.md when requested. Existing native permissions still apply.
The parent .shellmates directory holds isolated manager state, not task content.
"""

RUNNER = """#!/bin/sh
# Trial environment, not a security sandbox. Native HOME/auth/settings are inherited.
set -eu
test "$#" -gt 0 || { printf '%s\\n' 'Usage: ./run /absolute/path/to/sclaude <arguments...>' >&2; exit 64; }
trial_root=$(CDPATH= cd -P "$(dirname "$0")" && pwd -P)
umask 077
export XDG_CONFIG_HOME="$trial_root/.shellmates/config"
export XDG_STATE_HOME="$trial_root/.shellmates/state"
export XDG_DATA_HOME="$trial_root/.shellmates/data"
export SCREENDIR="$trial_root/.shellmates/screen"
mkdir -p "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$XDG_DATA_HOME" "$SCREENDIR"
cd "$trial_root/project"
exec "$@"
"""


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--harness", required=True, type=Path, help="trusted agent-harness source directory")
    parser.add_argument("--directory", required=True, type=Path, help="new private trial directory; must not exist")
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args(argv)
    target = args.directory.absolute()
    try:
        source = args.harness.resolve(strict=True)
        if not (source / "harness.py").is_file():
            raise ValueError("Expected an agent-harness directory containing harness.py")
        # Canonicalize /tmp on Mac; do not create arbitrary missing parent trees.
        target = target.parent.resolve(strict=True) / target.name
        if target.exists() or target.is_symlink():
            raise ValueError("Trial directory already exists; nothing will be overwritten")
        command = [sys.executable, "-B", str(source / "harness.py"), "project",
                   str(target / "project"), "--with-harness", "--apply"]
        print("Project-scoped trial:", target)
        print("Harness source:", source)
        print("Includes private preferences. Do not publish or commit the generated trial.")
        if not args.apply:
            print("Preview only. With --apply: create a new Git project, then", shlex.join(command))
            return 0
        target.mkdir(mode=0o700)
        project = target / "project"
        project.mkdir(mode=0o700)
        (project / "project.md").write_text(PROJECT, encoding="utf-8")
        subprocess.run(["git", "init", "--quiet", "--template=", str(project)], check=True, timeout=30)
        subprocess.run(command, check=True, timeout=30)
        subprocess.run(command[:-1] + ["--check"], check=True, timeout=30)
        runner = target / "run"
        runner.write_text(RUNNER, encoding="utf-8")
        runner.chmod(0o700)
        print("Ready. No global instructions, shell profiles, setup, login or model calls were changed/run.")
        print("Use", shlex.quote(str(runner)), "/absolute/path/to/sclaude <arguments...>")
        return 0
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        print("ERROR:", exc, file=sys.stderr)
        if args.apply and target.is_dir():
            print("Existing/partial directory retained for inspection:", target, file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
