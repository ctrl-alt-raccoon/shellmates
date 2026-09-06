# Using Shellmates

[Overview](../README.md) · [Installation](INSTALLATION.md) · [Project harness](HARNESS.md)

## Usage

### Launch

```sh
sclaude
sclaude --continue
sclaudex
sclaudex --resume
scodex
scodex resume --last
```

A new interactive session requires a topic such as `OAuth callback refactor`. Topics are persistent and partially visible in `screen -ls`, so do not include secrets or private prompt content.

If related sessions already exist, the launcher shows a compact chooser:

```text
1  Running/Detached  OAuth callback refactor   ~/work/api
2  Running/Attached  Investigate flaky tests   ~/work/service
3  Stopped (exit 0)  Update docs               ~/work/docs
n  New named session
m  All sessions
q  Cancel
```

Detach from Screen with **Ctrl-a d**. The backend process keeps running. Selecting a stopped record offers a new conversation or the native resume picker; its Screen ID is never treated as a Claude/Codex conversation ID. Pending stops remain visible and cannot be pruned.

Resuming a Claude conversation under a different route can send its prior content to the newly selected provider—for example, using `sclaudex --resume` for history created with normal Claude. Make that choice deliberately. The wrapper does not translate conversations or silently transfer Claude history into Codex (or vice versa).

### Manage

```sh
sclaude sessions
sclaude list
sclaude list --json
sclaude new --backend claudex --topic "Proxy comparison" --detach -- -p "..."
sclaude attach 3fa1c290
sclaude attach "OAuth callback refactor"
sclaude attach SESSION --multi
sclaude attach SESSION --takeover
sclaude stop SESSION
sclaude prune --older-than 30d
```

An attached session is never taken over implicitly. Choose `--multi` to join it or `--takeover` to detach its other display.

### Product help, version, and pass-through

```sh
sclaude help
sclaude --help
sclaude version
sclaude --version
sclaude -- --help       # pass --help to Claude Code
sclaude -- version      # pass the word "version" to Claude Code
```

A leading product-level `--` is removed by sclaude and sends every following argument directly to the selected backend. Managed `sclaudex` still rejects backend arguments that would replace its private settings or protected routing policy.

### Noninteractive behavior

Claude print mode, Codex exec/review, and pipelines bypass Screen and preserve streams and exit status:

```sh
sclaude -p "Reply with exactly OK"
sclaudex -p "Answer as JSON" | jq .
printf '%s' "$PROMPT" | sclaude -p
printf '%s' "$PROMPT" | scodex exec - --json
```

Inside an unrelated existing Screen, `sclaude` runs directly in that Screen and notes that it is not tracked. Set `SCLAUDE_FORCE_NEST=1` only when deliberate Screen nesting is desired.

## State and privacy

Session metadata lives under `${XDG_STATE_HOME:-~/.local/state}/sclaude/sessions/`. It records topic, backend, the durable working-directory path, Screen status, process IDs, timestamps, and exit state. Topics are also embedded in managed Screen names, so both topics and working-directory paths should be treated as metadata rather than secret storage.

It does **not** store:

- prompts or backend arguments
- environment variables
- CLIProxyAPI or OAuth credentials
- Claude/Codex output
- Claude/Codex transcript content

Stopped records remain until pruned. They are separate from each vendor's conversation storage. The recorded working directory is the launch directory; native options such as Codex `-C` may select another workspace without the wrapper rewriting them.

Arguments briefly occupy mode-`0600` one-use launch files, not durable session records. Consumers and cleanup serialize on the store lock; the next lock holder recovers abandoned `.consume-*` claims and interrupted `.sclaude-*` writes. Session directories reject symlinks, and record/launch/lock files reject symlinks, hardlinks, and non-regular types. Documents have a 4 MiB read/write ceiling. Deletion is not a promise of secure erasure from storage or backups.

Unrecognized Screen output and unconfirmed stops do not establish inactivity. A failed stop remains pending and can be retried; pruning and uninstall refuse uncertain liveness. Surviving sockets from older incorrectly terminal records are reported and can be stopped explicitly.

New runners acknowledge backend exit before `stop` closes Screen and finalizes the record. The stop request is durable, so it continues even if the requesting SSH connection closes. The owning runner sends `SIGTERM`, escalates to `SIGKILL` after two seconds if necessary, and waits for its child before acknowledging exit. A manager timeout leaves `stopping` state intact for retry. Ordinary Screen detach (including its normal SSH-hangup autodetach) is not a stop request. Terminal input, foreground process groups, and backend Ctrl-C handling remain unchanged. [GNU Screen detach behavior](https://www.gnu.org/software/screen/manual/html_node/Detach.html)

This supervision covers the launched backend process. Custom launchers must `exec` their backend or forward termination signals and wait for it; arbitrary daemonized helpers are not adopted or killed. The manager never signals a PID recovered from metadata. Older runners do not implement the acknowledgement protocol: if their backend exit cannot be proved, cleanup stays blocked and shutdown may require manual intervention. A runner killed with `SIGKILL` cannot publish an acknowledgement; this deliberately fails closed rather than guessing that its child exited.

## Update and uninstall

```sh
sclaude update
sclaude rollback
sclaude uninstall
sclaude uninstall --purge-state
```

Updates download the exact platform asset and checksum manifest, verify SHA-256, then run the candidate's noninteractive installer so that the candidate owns its schema migrations, as in bootstrap installation. The current and previous verified releases are retained; older ledger-owned releases are pruned after commit, and pending cleanup is reported without undoing a successful activation. Rollback re-verifies the retained previous release before activating it.

The three-launcher installer uses ledger/journal schema 3 and accepts old schema-1/2 ledgers plus schema-2 journals. Migration preserves existing launcher identities and refuses to replace an unmanaged `scodex`; failures restore prior owned state. Use the new release's verified installer for the first native-Codex upgrade: an old updater does not know to create `scodex`. A new installer can complete migration even if the same binary/tag was already installed by the old updater.

Automatic rollback across the pre-Codex boundary is deliberately refused: an old binary cannot safely interpret the new runtime configuration or `scodex` launcher. The previous release is still retained, but a downgrade across that boundary requires a separately planned restoration of compatible configuration and launchers. Rollback between native-Codex releases remains supported.

Uninstall first proves that managed Screen sessions are inactive and that all ledger-owned launchers, releases, and managed shell blocks are unchanged. The default mode removes the installed launchers/releases and unchanged managed PATH blocks, while preserving runtime/session state. `--purge-state` additionally removes the project runtime configuration, credential, settings overlay, and known session/launch/install-state children. The private state-root directory remains as the admission-lock anchor, and unrelated children are preserved. Neither mode removes vendor CLIs, the external CLIProxyAPI YAML or its sclaude-created backup, service definitions, OAuth/auth-directory contents, or `~/.codex/`; rotate proxy keys and perform external service/config cleanup separately when required.

## Screen compatibility

The parser accepts both legacy listings (including macOS Screen 4.00.03) and timestamped listings used by newer GNU Screen releases. Unknown socket formats are reported as uncertain rather than silently discarded. The implementation uses `-dmS`, `-ls`, `-r`, `-x`, `-d -r`, and `-X quit`; it does not require `screen -Q`, enable Screen logging, run `screen -wipe`, or modify `.screenrc`.
