# Try the shared Claude/Codex harness through Shellmates

Shellmates handles the terminal session. The separately supplied agent-harness
provides personal preferences and the two shared skills, handover and cross-review.
This opt-in trial joins them **without installing global instructions or changing
your normal Shellmates configuration**. Gemini, Beads, plugins and a proxy are not
required.

## Quick spin on a Mac or Linux machine

Prerequisites: an existing Shellmates binary, GNU Screen, Python 3.9+, Git, and the
native Claude/Codex CLIs you want to try. Supply the trusted agent-harness source
directory; it must support `project --with-harness`. It is not downloaded or bundled
with public Shellmates releases. See the private-transfer example below.

From the Shellmates checkout, with the harness source in a sibling directory:

```sh
# Preview: nothing is created or installed.
python3 scripts/try-harness.py --harness ../agent-harness --directory /tmp/sm-demo
# Explicitly create a NEW private trial directory; existing paths are refused.
python3 scripts/try-harness.py --harness ../agent-harness --directory /tmp/sm-demo --apply
```

Use a short path for GNU Screen's socket-name limit. The helper creates a new
disposable Git project, generates each provider's native instructions, and links
both providers to the same shared skill sources. Keep the source checkout in place.
No setup, package installation, authentication or model request runs at this step.

Set this to your existing or [source-built binary](../README.md#build-from-source):

```sh
shellmates_bin=/absolute/path/to/sclaude
command -v screen >/dev/null && command -v claude >/dev/null && command -v codex >/dev/null && \
sh /tmp/sm-demo/run "$shellmates_bin" setup --backends claude,codex \
  --non-interactive --no-modify-path --skip-smoke
sh /tmp/sm-demo/run "$shellmates_bin" new --backend codex --topic 'harness trial' -- \
  --sandbox read-only 'Report the project instructions and available handover/cross-review skills. Do not edit files or invoke reviewers.'
```

The prerequisite chain skips setup if a command is missing. This matters because
ordinary Mac setup can otherwise install missing Screen through Homebrew.
Select only `--backends codex` or `--backends claude`, and its matching prerequisite
check, if that is what you have.
Missing dependencies should be supplied deliberately; do not run system-level
sudo or vendor installers just to get through this trial. Native login remains
the vendor's responsibility. The run helper inherits HOME, CODEX_HOME,
CLAUDE_CONFIG_DIR, native settings and authentication; it never copies them.

To try Claude instead:

```sh
sh /tmp/sm-demo/run "$shellmates_bin" new --backend claude --topic 'harness trial' -- \
  --permission-mode plan 'Report the project instructions and available handover/cross-review skills. Do not edit files or invoke reviewers.'
```

Detach with Ctrl-a d. Over a new SSH connection, set shellmates_bin again and use
the **same trial directory**, then:

```sh
sh /tmp/sm-demo/run "$shellmates_bin" list
trial_session=ID_FROM_LIST
sh /tmp/sm-demo/run "$shellmates_bin" attach "$trial_session"
# When finished (detach first):
sh /tmp/sm-demo/run "$shellmates_bin" stop "$trial_session"
sh /tmp/sm-demo/run "$shellmates_bin" prune
```

Replace ID_FROM_LIST with the ID from list. The run helper sets the working directory to the trial
project and isolates Shellmates' XDG state and Screen socket directory. It forwards
the command's argument array unchanged. Native permissions still apply; **the trial
directory is not a security sandbox**. Explicit task/review authorization remains
necessary. No startup reinjection or automatic review loop is added.

The same native project discovery works when launched through Shellmates:
[Codex project instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
and [repository skills](https://learn.chatgpt.com/docs/build-skills). Claude receives
its native CLAUDE.md and .claude/skills; Codex receives AGENTS.md and .agents/skills.
Managed sclaudex still uses Claude's harness, but this quick spin deliberately
tests native Claude/Codex without configuring a proxy or opaque external claudex.

## Move the source to another machine

The private harness currently has no published remote. From committed checkouts,
Git can export only its runtime sources, without audit reports, backup material,
credentials, caches or session history:

```sh
umask 077
harness_bundle_dir=$(mktemp -d)
git -C ../agent-harness archive --format=tar --prefix=agent-harness/ \
  --output="$harness_bundle_dir/agent-harness.tar" HEAD harness.py adapters shared
git archive --format=tar --prefix=shellmates/ \
  --output="$harness_bundle_dir/shellmates.tar" HEAD
printf 'Private source archives: %s\n' "$harness_bundle_dir"
```

Inspect the two archive listings, transfer them privately, and extract them into a
new directory on the target. Build Shellmates for that target or use a separately
verified matching binary, then follow the quick spin there. **The harness archive
contains your personal preferences; do not publish it.** It is a deployment
snapshot, not another editable source of truth. Generate the trial on the target:
do not copy a used trial directory containing native state or old absolute links.

## Updating, checks and removal

Edit personal rules/skills only in the authoritative harness. Skill links see
those source edits; regenerate project instruction projections after rule/adapter
changes, using the same mode:

```sh
python3 ../agent-harness/harness.py project /tmp/sm-demo/project --with-harness --apply
python3 ../agent-harness/harness.py project /tmp/sm-demo/project --with-harness --check
```

Restart agent sessions to refresh discovery. A conflicting or hand-edited native
file is refused; there is no force-overwrite mode. If preparation fails, the partial
trial is retained for inspection, not silently deleted or retried.

After stopping the trial's sessions, keep any wanted HANDOVER.md separately and
remove/archive only that exact trial directory. The original project, global
instructions and original Shellmates state were never migrated and need no rollback.
Do not use the trial run helper for update/uninstall/purge or proxy setup: those
are separate operations, not part of this quick spin.

Shellmates' own project.md remains public repository knowledge. Its generated
CLAUDE.md/AGENTS.md intentionally exclude private preferences. The ordinary Go suite
checks those projections. CI also runs the trial helper's standard-library tests:

```sh
python3 -B -m unittest discover -s scripts/tests -v
```

See [STATUS.md](../STATUS.md) for actual integration results and remaining real
Linux/backend limits. A successful context-discovery test does not guarantee model
obedience, certify a review, or prove every SSH failure mode.
