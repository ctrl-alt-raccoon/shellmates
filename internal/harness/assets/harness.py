#!/usr/bin/env python3
"""Install, check, update or remove a project-local Claude/Codex harness. No network."""

import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parent
PREFIX = ".shellmates/harness/"
MANIFEST = ".shellmates/harness-install.json"
JOURNAL = ".shellmates/harness-transaction.json"
EXCLUDE = ".git/info/exclude"
LIMIT = 1024 * 1024
MAX_CONTEXT = 24 * 1024
SOURCES = (
    "harness.py", "shared/house-rules.md", "adapters/claude/CLAUDE.md",
    "adapters/codex/AGENTS.md", "shared/skills/handover/SKILL.md",
    "shared/skills/cross-review/SKILL.md", "shared/skills/cross-review/scripts/review.py",
)
ENTRIES = {"CLAUDE.md": "claude", "AGENTS.md": "codex"}
LINKS = {f"{directory}/skills/{skill}": f"../../{PREFIX}shared/skills/{skill}"
         for directory in (".claude", ".agents") for skill in ("handover", "cross-review")}
ALLOWED = {*ENTRIES, *LINKS, *(PREFIX + name for name in SOURCES), MANIFEST, EXCLUDE, "project.md"}
DIRECTORIES = {str(parent) for name in ALLOWED for parent in Path(name).parents
               if str(parent) != "."}
OLD_HEADER = re.compile(r"<!-- agent-harness generated sha256=([0-9a-f]{64}); edit sources -->\n")
BEGIN = "<!-- shellmates:harness begin -->\n"
END = "<!-- shellmates:harness end -->\n\n"
IGNORE_BEGIN = "# shellmates:harness begin\n"
IGNORE_END = "# shellmates:harness end\n"
TEMPLATE = """# Project guide

Describe this repository's purpose, architecture, important paths, and constraints.
Add the real development and verification commands before delegating implementation.
Do not invent commands or claim that checks passed because this template exists.
"""


def digest(body):
    return hashlib.sha256(body.encode("utf-8")).hexdigest()


def read(path):
    """Read an explicit bounded UTF-8 instruction, never a special file or link."""
    path = Path(path)
    for parent in path.parents:
        if parent.is_symlink():
            raise ValueError(f"Symlinked instruction parent: {parent}")
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "r", encoding="utf-8", newline="") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > LIMIT:
            raise ValueError(f"Expected a bounded regular file with one link: {path}")
        body = stream.read(LIMIT + 1)
    if "\0" in body or len(body.encode("utf-8")) > LIMIT:
        raise ValueError(f"Not a bounded text file: {path}")
    return body


def project_sources(repo):
    candidates = [repo / "project.md", repo / ".claude/project.md"]
    style = repo / ".claude/code-style.md"
    for path in [*candidates, style]:
        if path.is_symlink():
            raise ValueError(f"Project instructions must not be symlinks: {path}")
    found = [p for p in candidates if p.exists()]
    if len(found) > 1:
        raise ValueError("Both project.md and .claude/project.md exist; reconcile them first")
    return found + ([style] if style.exists() else [])


class Tree:
    """Anchor all mutation beneath an open project directory, without following links."""

    def __init__(self, path):
        self.path = path
        self.fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)

    def close(self):
        os.close(self.fd)

    @contextlib.contextmanager
    def parent(self, name, create=False):
        parts = Path(name).parts
        if not parts or Path(name).is_absolute() or any(p in (".", "..") for p in parts):
            raise ValueError("Invalid project-relative path")
        fd = os.dup(self.fd)
        try:
            for part in parts[:-1]:
                if create:
                    try:
                        os.mkdir(part, 0o700, dir_fd=fd)
                    except FileExistsError:
                        pass
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                os.close(fd)
                fd = child
            yield fd, parts[-1]
        finally:
            os.close(fd)

    def get(self, name):
        try:
            with self.parent(name) as (fd, leaf):
                info = os.stat(leaf, dir_fd=fd, follow_symlinks=False)
                if stat.S_ISLNK(info.st_mode):
                    return {"link": os.readlink(leaf, dir_fd=fd)}
                if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > LIMIT:
                    raise ValueError(f"Refusing non-regular, hardlinked or oversized file: {name}")
                datafd = os.open(leaf, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
                with os.fdopen(datafd, "r", encoding="utf-8", newline="") as stream:
                    current = os.fstat(stream.fileno())
                    if ((current.st_ino, current.st_dev) != (info.st_ino, info.st_dev)
                            or not stat.S_ISREG(current.st_mode) or current.st_nlink != 1
                            or current.st_size > LIMIT):
                        raise ValueError(f"File changed while reading: {name}")
                    body = stream.read(LIMIT + 1)
                if "\0" in body or len(body.encode("utf-8")) > LIMIT:
                    raise ValueError(f"Expected bounded UTF-8 text: {name}")
                return {"text": body, "mode": stat.S_IMODE(info.st_mode)}
        except FileNotFoundError:
            return None

    def put(self, name, value):
        with self.parent(name, create=value is not None) as (fd, leaf):
            if value is None:
                os.unlink(leaf, dir_fd=fd)
            else:
                temp = ".harness-" + uuid.uuid4().hex
                try:
                    if "link" in value:
                        os.symlink(value["link"], temp, dir_fd=fd)
                    else:
                        datafd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                                         0o600, dir_fd=fd)
                        with os.fdopen(datafd, "w", encoding="utf-8", newline="") as stream:
                            stream.write(value["text"])
                            stream.flush()
                            os.fchmod(stream.fileno(), value["mode"])
                            os.fsync(stream.fileno())
                    os.replace(temp, leaf, src_dir_fd=fd, dst_dir_fd=fd)
                finally:
                    try:
                        os.unlink(temp, dir_fd=fd)
                    except FileNotFoundError:
                        pass
            os.fsync(fd)

    def exists_dir(self, name):
        try:
            with self.parent(name + "/child"):
                return True
        except FileNotFoundError:
            return False

    def tidy(self, directories):
        for name in sorted(directories, key=lambda n: (n.count("/"), n), reverse=True):
            try:
                with self.parent(name) as (fd, leaf):
                    os.rmdir(leaf, dir_fd=fd)
            except OSError:
                pass  # only empty, installer-created directories; never recursive deletion


def regular(tree, name, optional=False):
    value = tree.get(name)
    if value is None and optional:
        return None
    if value is None or "text" not in value:
        raise ValueError(f"Expected an ordinary file: {name}")
    return value


def text_file(body, mode=0o600):
    return {"text": body, "mode": mode}


def json_file(value, mode=0o600):
    return text_file(json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False) + "\n", mode)


def load_json(tree, name):
    value = regular(tree, name, optional=True)
    if value is None:
        return None
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError(f"Duplicate JSON key in {name}")
            result[key] = value
        return result
    return json.loads(value["text"], object_pairs_hook=unique)


def valid_snapshot(value):
    return value is None or (isinstance(value, dict) and (
        (set(value) == {"link"} and isinstance(value["link"], str)) or
        (set(value) == {"text", "mode"} and isinstance(value["text"], str)
         and type(value["mode"]) is int and 0 <= value["mode"] <= 0o777)))


def validate_manifest(value):
    if not isinstance(value, dict) or set(value) != {
            "schema", "bundle", "local", "custom_preferences", "files", "native", "directories"}:
        raise ValueError("Invalid harness manifest; preserve it and inspect before recovery")
    if value["schema"] != 1 or type(value["local"]) is not bool or type(value["custom_preferences"]) is not bool:
        raise ValueError("Unsupported harness manifest")
    if set(value["files"]) != set(SOURCES) or set(value["native"]) != set(ENTRIES):
        raise ValueError("Manifest contains unexpected ownership paths")
    for code in [value["bundle"], *value["files"].values()]:
        if not isinstance(code, str) or not re.fullmatch(r"[0-9a-f]{64}", code):
            raise ValueError("Invalid manifest digest")
    if not isinstance(value["directories"], list) or not set(value["directories"]) <= DIRECTORIES:
        raise ValueError("Invalid directory ownership")
    for item in value["native"].values():
        if (not isinstance(item, dict) or set(item) != {"block", "restore", "existed", "replaced"}
                or type(item["existed"]) is not bool or type(item["replaced"]) is not bool
                or not valid_snapshot(item["restore"])
                or not isinstance(item["block"], str) or not item["block"].startswith(BEGIN)
                or not item["block"].endswith(END)):
            raise ValueError("Invalid native instruction ownership")


def ignore_block():
    paths = ["/.shellmates/harness/", "/" + MANIFEST, "/" + JOURNAL,
             "/CLAUDE.md", "/AGENTS.md", *("/" + n for n in LINKS)]
    return "\n" + IGNORE_BEGIN + "\n".join(paths) + "\n" + IGNORE_END


def project_git(repo, *args):
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
    return subprocess.run(["git", "--no-pager", "-c", "core.fsmonitor=false", *args],
                          cwd=repo, env=env, capture_output=True, check=True, timeout=20).stdout


def require_local(tree):
    # Worktree gitdirs can live outside the project. Do not reach out and mutate them.
    if not tree.exists_dir(".git"):
        raise ValueError("--local requires a regular Git checkout with its own .git directory; use a clone, not a linked worktree")
    tracked = project_git(tree.path, "ls-files", "-z", "--", "CLAUDE.md", "AGENTS.md",
                          ".shellmates/harness", MANIFEST, JOURNAL, *LINKS)
    if tracked:
        raise ValueError("Local/private installation would change tracked native or harness files. Use a separate clone or reconcile them first; Git ignore cannot hide tracked files")


def inventory(tree, manifest):
    validate_manifest(manifest)
    expected = set(SOURCES) | {str(p) for name in SOURCES for p in Path(name).parents if str(p) != "."}
    def inspect(directory=""):
        with tree.parent(PREFIX + directory + "/child") as (fd, _):
            for leaf in os.listdir(fd):
                name = (directory + "/" + leaf).lstrip("/")
                if name not in expected:
                    raise ValueError(f"Unexpected file in owned harness bundle: {name}; preserve it outside the bundle first")
                if name not in SOURCES:
                    inspect(name)
    inspect()
    for name, expected in manifest["files"].items():
        if digest(regular(tree, PREFIX + name)["text"]) != expected:
            raise ValueError(f"Installed bundle was edited: {PREFIX + name}; preserve changes before update/remove")
    for name, target in LINKS.items():
        if tree.get(name) != {"link": target}:
            raise ValueError(f"Skill link changed or missing: {name}")
    for name, info in manifest["native"].items():
        body = regular(tree, name)["text"]
        if not body.startswith(info["block"]):
            raise ValueError(f"Generated instruction block was changed: {name}; preserve edits before update/remove")
    if manifest["local"]:
        require_local(tree)
        if ignore_block() not in regular(tree, EXCLUDE)["text"]:
            raise ValueError("Private-install Git exclusions changed; restore them before proceeding")


def project_context(tree):
    sources = project_sources(tree.path)
    if not sources or sources[0].name != "project.md":
        return TEMPLATE, True
    return "\n\n".join(regular(tree, str(p.relative_to(tree.path)))["text"].strip()
                         for p in sources) + "\n", False


def plan(tree, action, local=False, preferences=None):
    manifest = load_json(tree, MANIFEST)
    if manifest is not None:
        inventory(tree, manifest)
        local = manifest["local"]
    elif action in ("check", "update", "remove"):
        raise ValueError("No project harness installed; run sclaude harness install --project .")
    if preferences is not None and not local:
        raise ValueError("Personal --preferences require --local; never embed private guidance in a shared installation")
    if local:
        require_local(tree)
    desired = {}
    if action == "remove":
        for name, info in manifest["native"].items():
            current = regular(tree, name)
            remaining = current["text"][len(info["block"]):]
            desired[name] = (info["restore"] if info["restore"] is not None and remaining == info["restore"]["text"] else
                             text_file(remaining, current["mode"]) if remaining else
                             info["restore"] if info["replaced"] else
                             text_file("", current["mode"]) if info["existed"] else None)
        desired.update({name: None for name in LINKS})
        desired.update({PREFIX + name: None for name in SOURCES})
        desired[MANIFEST] = None
        if local:
            current = regular(tree, EXCLUDE)
            desired[EXCLUDE] = text_file(current["text"].replace(ignore_block(), "", 1), current["mode"])
        return desired, manifest["directories"]

    for name in ("AGENTS.override.md", "CLAUDE.local.md", ".claude/CLAUDE.md"):
        if tree.get(name) is not None:
            raise ValueError(f"Reconcile native override before installation: {name}")
    for skill in ("handover", "cross-review"):
        if tree.exists_dir(f".codex/skills/{skill}") or tree.get(f".codex/skills/{skill}") is not None:
            raise ValueError(f"Reconcile legacy .codex/skills/{skill} to avoid duplicate discovery")
    content, new_project = project_context(tree)
    if new_project:
        desired["project.md"] = text_file(content, 0o644)
    payload = {name: read(ROOT / name) for name in SOURCES}
    custom = bool(manifest and manifest["custom_preferences"])
    if preferences is not None:
        payload["shared/house-rules.md"] = read(preferences)
        custom = True
    elif custom:
        payload["shared/house-rules.md"] = regular(tree, PREFIX + "shared/house-rules.md")["text"]
    if len(payload["shared/house-rules.md"].encode("utf-8")) > 8 * 1024:
        raise ValueError("Working preferences exceed 8 KiB; shorten them before installation")
    native = {}
    for name, vendor in ENTRIES.items():
        current = regular(tree, name, optional=True)
        original = current["text"] if current else ""
        if manifest:
            info = manifest["native"][name]
            suffix = original[len(info["block"]):]
            restore, existed, replaced = info["restore"], info["existed"], info["replaced"]
        else:
            if BEGIN in original or END in original:
                raise ValueError(f"Unowned harness markers: {name}")
            suffix, restore, existed, replaced = original, current, current is not None, False
            match = OLD_HEADER.match(original)
            if match and digest(original[match.end():]) == match[1] and original[match.end():] == content:
                # Losslessly adopt the old project-only generator without duplicating its body.
                suffix, restore, replaced = "", current, True
        body = "\n\n".join([payload["shared/house-rules.md"].strip(),
            payload[f"adapters/{vendor}/{name}"].strip(), content.strip()]) + "\n"
        block = BEGIN + "<!-- Generated; edit project.md, then run sclaude harness update --project . -->\n" + body + END
        if len((block + suffix).encode("utf-8")) > MAX_CONTEXT:
            raise ValueError(f"{name} would exceed {MAX_CONTEXT} bytes; condense shared context first")
        desired[name] = text_file(block + suffix, 0o600 if local else current["mode"] if current else 0o644)
        native[name] = {"block": block, "restore": restore, "existed": existed, "replaced": replaced}
    for name, target in LINKS.items():
        if not manifest and tree.get(name) is not None:
            raise ValueError(f"Skill destination already owned: {name}; preserve and reconcile it first")
        desired[name] = {"link": target}
    for name, body in payload.items():
        if not manifest and tree.get(PREFIX + name) is not None:
            raise ValueError(f"Unmanaged harness payload: {PREFIX + name}")
        desired[PREFIX + name] = text_file(body, 0o600 if local else 0o644)
    if local and not manifest:
        current = regular(tree, EXCLUDE, optional=True)
        body = current["text"] if current else ""
        if IGNORE_BEGIN in body or IGNORE_END in body:
            raise ValueError("Unowned Git exclude markers; reconcile before installation")
        desired[EXCLUDE] = text_file(body + ignore_block(), current["mode"] if current else 0o600)
    files = {name: digest(body) for name, body in payload.items()}
    directories = manifest["directories"] if manifest else [
        name for name in sorted(DIRECTORIES) if not tree.exists_dir(name) and not name.startswith(".git")]
    desired[MANIFEST] = json_file({"schema": 1, "bundle": digest(json.dumps(files, sort_keys=True)),
        "local": local, "custom_preferences": custom, "files": files,
        "native": native, "directories": directories}, 0o600 if local else 0o644)
    return desired, directories


def validate_journal(journal):
    if (not isinstance(journal, dict) or set(journal) != {"schema", "before", "after", "directories"}
            or journal["schema"] != 1 or not isinstance(journal["before"], dict)
            or not isinstance(journal["after"], dict)
            or set(journal["before"]) != set(journal["after"])
            or not set(journal["before"]) <= ALLOWED
            or not isinstance(journal["directories"], list) or not set(journal["directories"]) <= DIRECTORIES):
        raise ValueError("Invalid transaction journal; no recovery writes performed")
    for side in ("before", "after"):
        for name, value in journal[side].items():
            if not valid_snapshot(value) or (value and "link" in value and name not in LINKS):
                raise ValueError("Invalid recovery snapshot")
            if value and "link" in value and value != {"link": LINKS[name]}:
                raise ValueError("Invalid recovery link")


def recover(tree, journal):
    validate_journal(journal)
    for name, before in journal["before"].items():
        if tree.get(name) not in (before, journal["after"][name]):
            raise ValueError(f"Transaction path changed externally: {name}; preserve it before recovery")
    for name, before in reversed(list(journal["before"].items())):
        if tree.get(name) != before:
            tree.put(name, before)
    tree.put(JOURNAL, None)
    tree.tidy(journal["directories"])


def execute(tree, desired, directories):
    changes = {name: value for name, value in desired.items() if tree.get(name) != value}
    if not changes:
        return
    # Install privacy exclusions first; remove them only after private outputs.
    removing = desired.get(MANIFEST, True) is None
    order = sorted(changes, key=lambda n: ((n == EXCLUDE) if removing else (n != EXCLUDE), n == MANIFEST, n))
    changes = {name: changes[name] for name in order}
    before = {name: tree.get(name) for name in changes}
    missing = [name for name in directories if not tree.exists_dir(name)]
    journal = {"schema": 1, "before": before, "after": changes, "directories": missing}
    validate_journal(journal)
    if len(json_file(journal)["text"].encode("utf-8")) > LIMIT:
        raise ValueError("Transaction exceeds bounded journal capacity; condense instructions first")
    tree.put(JOURNAL, json_file(journal))
    try:
        for name, value in changes.items():
            if tree.get(name) != before[name]:
                raise ValueError(f"File changed during transaction: {name}")
            tree.put(name, value)
    except BaseException:
        recover(tree, journal)
        raise
    tree.put(JOURNAL, None)
    if desired.get(MANIFEST, True) is None:
        tree.tidy(directories)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, prog="sclaude harness")
    parser.add_argument("action", choices=("install", "check", "update", "remove", "recover"))
    parser.add_argument("--project", type=Path, required=True, help="explicit repository root; never a global profile")
    parser.add_argument("--dry-run", action="store_true", help="show changes without writing")
    parser.add_argument("--local", action="store_true", help="install only for this checkout and add private Git exclusions (install only)")
    parser.add_argument("--preferences", type=Path, help="explicit personal UTF-8 rules; requires a local install")
    args = parser.parse_args(argv)
    tree = None
    try:
        if sys.version_info < (3, 9) or os.name != "posix":
            raise ValueError("Requires Python 3.9+ on macOS or Linux")
        if args.local and args.action != "install":
            raise ValueError("--local is install-only; subsequent operations preserve the chosen scope")
        if args.preferences and args.action not in ("install", "update", "check"):
            raise ValueError("--preferences is only valid with install/update/check")
        project = args.project.resolve(strict=True)
        protected = {Path(os.path.abspath(value)) for key in ("HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR")
                     if (value := os.environ.get(key))}
        if (project == Path(project.anchor) or project in protected
                or project.name in (".claude", ".codex", ".agents") or not (project / ".git").exists()):
            raise ValueError("Choose an explicit Git repository root, not a home/profile or ordinary directory")
        if Path(os.fsdecode(project_git(project, "rev-parse", "--show-toplevel")).strip()).resolve() != project:
            raise ValueError("--project must select the Git repository root")
        tree = Tree(project)
        fcntl.flock(tree.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        journal = load_json(tree, JOURNAL)
        if args.action == "recover":
            if journal is not None:
                validate_journal(journal)
                if not args.dry_run:
                    recover(tree, journal)
            print("Recovery preview" if args.dry_run else "Recovery complete; original instructions preserved")
            return 0
        if journal is not None:
            raise ValueError("Interrupted transaction: run sclaude harness recover --project . before retrying")
        desired, directories = plan(tree, args.action, args.local, args.preferences)
        changed = [name for name, value in desired.items() if tree.get(name) != value]
        for name in changed:
            print(f"{'preview' if args.dry_run else args.action}: {name}")
        if args.action == "check":
            print("STALE: run sclaude harness update --project ." if changed else "Harness up to date")
            return int(bool(changed))
        if not args.dry_run:
            execute(tree, desired, directories)
        print("No global agent configuration, authentication, or sessions changed.")
        if args.action == "remove":
            print("Project knowledge and non-harness instructions preserved.")
        elif not changed:
            print("Harness up to date")
        elif not args.dry_run:
            print("Start a fresh Claude/Codex session in this project to load the instructions and skills.")
        return 0
    except (OSError, UnicodeError, ValueError, TypeError, KeyError, subprocess.SubprocessError) as exc:
        print(f"HARNESS REFUSED: {exc}", file=sys.stderr)
        return 2
    finally:
        if tree:
            tree.close()


if __name__ == "__main__":
    sys.exit(main())
