# A project-local harness for Claude and Codex

[Overview](../README.md) · [Architecture](ARCHITECTURE.md) · [Session usage](USAGE.md)

Install one operating model in a selected repository: shared working agreements,
project context, handover and explicit cross-agent review. Shellmates ships the
complete small bundle. You do not need a second checkout, a plugin manager, or a
global harness installation.

## Install

Requirements: a built or installed `sclaude`, Python 3.9+, and an existing Git
repository. Harness management does not need Screen, a configured backend, a
vendor CLI, credentials, or a network connection.

```sh
cd /path/to/your/repository
sclaude harness install --project . --dry-run
sclaude harness install --project .
sclaude harness check --project .
```

Use an absolute binary path if it is not on PATH. The target must be the repository
root, not a parent directory or native agent profile. The project is always explicit;
there is no global mode and no automatic enrollment of other repositories.

If `project.md` is missing, installation creates a clearly marked starter guide.
Replace its placeholders with real architecture, commands, testing expectations
and traps. No agent can infer a test suite from a template. Then run:

```sh
sclaude harness update --project .
```

Start a new Claude or Codex session from that repository or a subdirectory.
Shellmates launches the native CLI normally; direct `claude` and `codex` launches
can discover the same installed project files.

## Shared project installation or private local installation?

| Mode | Intended use | Git behavior |
|---|---|---|
| Default | The repository/team uses the public shared defaults | Generated files can be reviewed and committed |
| `--local` | Only your checkout uses the harness | Adds project-local exclusions in `.git/info/exclude` |
| `--local --preferences FILE` | Your checkout also uses your personal rules | Imports only that explicit UTF-8 file; excludes the installed snapshot |

A local installation is still persistent: it is not a temporary trial.

```sh
sclaude harness install --project . --local \
  --preferences /private/path/to/house-rules.md
```

Personal preferences are never read from your home directory automatically.
The original preference file stays where it is; its installed snapshot replaces
the public default rules for this project. To refresh it later:

```sh
sclaude harness update --project . --preferences /private/path/to/house-rules.md
sclaude harness check --project . --preferences /private/path/to/house-rules.md
```

An update without `--preferences` retains the existing personal snapshot. Neither
the original rules path nor runtime secrets are recorded in the install manifest.

Local mode refuses already-tracked native/harness destinations. **Git ignore does
not protect tracked files.** Use a separate clone or deliberately reconcile the
existing files first; there is no force-overwrite or automatic untracking option.
Private local installation currently requires a regular checkout with its own
`.git` directory, not a linked worktree. Shared installations work in worktrees.
Git exclusions prevent ordinary accidental staging, not `git add -f`, uploads,
other users' tools, or disclosure by a model. Never put credentials in instructions.

Non-global does not mean isolated. Existing global instructions, skills, settings
and managed policy can still load; their precedence remains provider-owned.
Installing this harness does not disable another active global harness. Avoid
keeping the same skill globally and locally unless duplicate discovery is intended.
[Codex instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md),
[Codex skill discovery](https://learn.chatgpt.com/docs/build-skills), and
[Claude memory](https://code.claude.com/docs/en/memory) describe native behavior.

## What gets installed

```text
repository/
├── project.md                         # editable repository knowledge
├── CLAUDE.md                          # generated block + preserved existing notes
├── AGENTS.md                          # generated block + preserved existing notes
├── .claude/skills/{handover,cross-review} ─┐
├── .agents/skills/{handover,cross-review} ─┤ relative links
└── .shellmates/                         │
    ├── harness-install.json             # ownership, hashes, restoration metadata
    └── harness/                         # immutable project snapshot
        ├── harness.py                   # offline installer/recovery entry point
        ├── adapters/{claude,codex}/
        └── shared/                      ◀┘
            ├── house-rules.md
            └── skills/{handover,cross-review}/
```

Both providers use the same skill bodies. Links stay inside the repository and
survive relocation and Git cloning; there are no links to the build machine.
Snapshots are generated distribution artifacts, not separate editable sources.
The maintained public source is `internal/harness/assets/` in Shellmates.

The installed Python entry point can also check/recover a project without an
installed Shellmates binary:

```sh
python3 -I -B .shellmates/harness/harness.py check --project .
```

That checks against the installed snapshot. `sclaude harness check` checks against
the bundle shipped in the particular Shellmates binary you invoke, so an older
project snapshot may correctly be reported as stale after a Shellmates upgrade.
No command fetches the latest harness behind your back.

## Existing instructions: preserve first, consolidate deliberately

Installation retains ordinary existing `CLAUDE.md` and `AGENTS.md` text after a
clearly delimited generated block. It does not pretend that a mechanical merge
resolves contradictory instructions. Move genuinely shared existing rules into
`project.md`, leaving only necessary provider-specific notes outside the block.
Edits outside the block survive updates and removal. Edits inside it are refused
by check/update/remove until reconciled; there is no destructive force flag.

Intact project-only projections from the earlier agent-harness generator can be
adopted without duplicating their shared body. Their exact originals are retained
for restoration. Root and legacy `.claude/project.md` cannot compete; reconcile
those first. Legacy `.claude/code-style.md` is included when present.

Conflicting native overrides (`AGENTS.override.md`, `CLAUDE.local.md`, or
`.claude/CLAUDE.md`), occupied skill destinations, symlinked instruction files,
and legacy `.codex/skills` duplicates are refused before normal installation writes.
Project hooks, unrelated skills, settings, and narrower instructions are not deleted
or silently translated. They remain project/provider-owned.

## Update, removal and recovery

```sh
sclaude harness check --project .
sclaude harness update --project . --dry-run
sclaude harness update --project .
sclaude harness remove --project . --dry-run
sclaude harness remove --project .
```

Update uses the invoking binary's embedded bundle and the current project facts.
Removal removes only unchanged owned bundle files, skill links and generated
instruction blocks. It preserves `project.md`, other skills/settings, and
non-harness instruction edits. It restores adopted originals and removes its local
Git exclusion block. Empty newly created directories are removed without recursive
project deletion. Modified or unexpected bundle files require manual reconciliation.
Removal is not secure erasure; backups and Git history have their own retention.

Operations use a project-directory lock, anchored no-follow writes, preflighted
ownership checks, atomic file replacement and a bounded undo journal. On an ordinary
write failure the original managed files are restored. After an interrupted process:

```sh
sclaude harness recover --project . --dry-run
sclaude harness recover --project .
sclaude harness check --project .
```

Recovery restores the pre-transaction state, not an invented successful installation.
It refuses invalid paths and externally changed files. Preserve the journal and
conflicting files if manual intervention is needed. Do not run Git publication or
other editing tools concurrently with a migration. This is recoverable local file
management, not a defense against a hostile process running as your user.

Exit statuses: `0` successful/up to date; `1` stale check; `2` refused/invalid/
unavailable. `--dry-run` makes no project writes. It is a preview, not permission
to execute a reviewer or install a vendor CLI.

## Handover and cross-review

Claude exposes `/handover` and `/cross-review`; Codex uses `$handover` and
`$cross-review` when discovered. Handover summarizes established context; it does
not copy native conversation databases or grant new authority.

Review is one explicit independent pass. Claude can request Codex; Codex can request
Claude. The implementer evaluates findings and decides authorized fixes.

```sh
# Preview the selected tracked diff: no model contacted.
python3 -B .shellmates/harness/shared/skills/cross-review/scripts/review.py \
  --reviewer claude --repo . --diff working

# After checking the selected content and authorizing provider delivery:
python3 -B .shellmates/harness/shared/skills/cross-review/scripts/review.py \
  --reviewer claude --repo . --diff working --execute
```

Use `--reviewer codex` for the other direction. `--file path` includes explicit
new files or plans; untracked files are never swept into a diff packet. Supply
requirements, tests, callers and narrower instructions where needed. Packet contents
are untrusted review evidence, not permission to execute commands.

The runner bounds packet size/time, uses an empty review working directory,
disables I/O tools, and preserves the vendor's authentication ownership. It does
not use an unsandboxed reviewer with a prompt-only promise. Codex versions lacking
the verified `view_image` disable control are refused before sending the packet;
0.153.4 was verified in the migration. CLI compatibility must be rechecked when
upgrading. Timeout or an empty result is unavailable review, never approval.
There is no automatic review at startup, on commit, or after a previous review.

## Moving to Linux and migrating the earlier trial

For a shared project, commit the reviewed generated files and relative symlinks,
then clone it on the target. For a private local install, take the Shellmates binary
or source and your preference file through a private channel, then install on the
target. Never copy authentication, used agent profiles, caches or transcripts.
Linux/SSH setup is covered in [installation](INSTALLATION.md) and
[session usage](USAGE.md). Native CLI availability and login are separate from
harness file installation. Python 3.9 compatibility is a test claim only for the
platforms recorded in [STATUS.md](../STATUS.md), not a blanket Linux certification.

`scripts/try-harness.py` remains a legacy disposable smoke helper for explicitly
supplied external harnesses; it is not the recommended installation interface.
Do not run the two installers over the same owned files. Keep a previous trial
for reference, migrate its useful `project.md`/notes into a fresh project, and use
`sclaude harness install` there. No original harness or global configuration is
retired automatically.

## Verification

```sh
python3 -B -m unittest discover -s scripts/tests -v
# Optional installed CLIs, temporary profiles, dummy auth, localhost provider:
HARNESS_NATIVE_SMOKE=1 python3 -B -m unittest discover -s scripts/tests -p test_harness_native.py -v
```

Tests exercise install/check/update/removal, recovery, collisions, private mode,
real Git-clone portability, bounded review packets and native tool exposure.
The ordinary Go suite also checks the embedded command and Shellmates' own
project-only entry-file freshness. See [STATUS.md](../STATUS.md) for actual results.
Configuration delivery is testable; future model obedience is not guaranteed.
