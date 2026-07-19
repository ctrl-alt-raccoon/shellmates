# Status checkpoint

Updated: 2026-07-19

## Completed tasks

- Restored the baseline and lifecycle integration.
- Implemented transactional install, update, rollback, uninstall, and purge behavior.
- Hardened runtime configuration, proxy credentials, managed settings, proxy YAML handling, setup transactions, dependency policy, doctor/verify checks, and CLI argument contracts.
- Removed automatic execution of mutable third-party installer scripts; Codex CLI is optional and system-level `sudo` remains separately authorized or instruction-only.
- Hardened private configuration reads, setup/install transaction admission and recovery, release publication recovery, owned-release pruning, and session/uninstall lifecycle serialization.
- Built and checksum-verified Darwin/Linux release binaries for amd64/arm64.
- Verified the disposable GNU Screen doctor lifecycle and explicit local proxy fixtures.
- Diagnosed and fixed the managed GNU Screen startup deadlock: `_run-session` no longer reacquires the creator-held state-root admission lock.
- Added a regression test for startup while the creator holds state-root admission.
- Verified a real managed Screen session reaches `running`, records Screen/runner/backend PIDs, stops cleanly, and records `stopped-by-manager`.

## Current task

Resolve the independently verified final-review findings (Task 25), then rerun the full final verification matrix (Tasks 23 and 5).

Current finding classes:

1. Make install-journal recovery fail closed: reject trailing JSON, require the exact stable-launcher snapshot keys, require a valid release digest for every activation, and verify the committed release before deleting the journal.
2. Allow uninstall to ignore an already-missing inactive historical release while still rejecting missing current/previous releases and modified present releases.
3. Prevent Homebrew executable/config approval from escaping the active canonical Homebrew prefix through symlinked path components.
4. Discover Homebrew-installed GNU Screen and CLIProxyAPI even when the Homebrew bin directory is absent from `PATH`.
5. Split release verification from privileged publication, add exact-tag Linux/macOS gates and per-tag serialization, and make publication fail closed rather than exposing a mixed partial rerun.
6. Correct documentation that overstates macOS static linking and installer/checksum authentication; replace misleading pipe-to-shell “review” wording.

## Remaining tasks

1. Add regression tests and implement the six current finding classes.
2. Run focused setup/install/uninstall/Homebrew/workflow tests.
3. Run repository tests, race tests, vet, formatting, module verification, shell syntax, and whitespace checks.
4. Rerun isolated install/update/rollback/uninstall/purge, managed Screen, disposable Screen, and explicit localhost proxy lifecycles.
5. Rebuild a fresh four-platform release asset set and verify exact filenames and SHA-256 checksums.
6. Perform a final adversarial current-tree review and fix every independently confirmed finding.
7. Decide whether to retain or remove generated verification directories under `dist/`.

## Key decisions and constraints

- `sclaude` routes to ordinary Claude Code/Anthropic; managed `sclaudex` keeps Claude Code as the harness and routes through CLIProxyAPI at `http://127.0.0.1:8317`.
- An existing external `claudex` remains opaque and is referenced only by a stable absolute path.
- Prompts/backend arguments exist only in private one-use launch files and are never persisted in session records.
- Automated verification uses temporary HOME/XDG roots and explicit localhost fixtures; it must not touch live OAuth, proxy, service, shell-profile, or credential state.
- Never read or modify `~/.codex/`, inspect Claude credential/keychain storage, expose secrets, log users out, or automatically run system-level `sudo`.
- Existing release assets must never be overwritten in place; fixes require a new release tag.
- Local commits are authorized; do not push, create tags, or publish releases without separate authorization.
- Create a local commit after each tracked task is completed.
- Update this file whenever a tracked task is completed.
