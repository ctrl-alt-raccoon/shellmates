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
- Resolved all six final verified finding classes:
  1. Install-journal recovery is strict and fail closed, including exact launcher snapshots, release digests, trailing-data rejection, and committed-release verification.
  2. Uninstall tolerates an already-missing inactive historical release while retaining current/previous and modification checks.
  3. Homebrew executable/config trust is contained beneath the canonical active prefix after path resolution.
  4. Homebrew-installed GNU Screen and CLIProxyAPI are discovered outside `PATH`.
  5. Release verification/build jobs are read-only and publication is isolated, serialized, draft-first, exact-asset verified, and explicitly targeted through `GH_REPO`.
  6. Documentation accurately describes installer/checksum provenance, pipe-to-shell execution, and macOS linking.

Focused Task 25 verification passed:

- `go test ./internal/setup`
- Ruby YAML parsing for `.github/workflows/ci.yml` and `.github/workflows/release.yml`
- `sh -n install.sh scripts/build-release.sh`
- `git diff --check`
- Direct inspection confirmed both checkout-free publication steps set `GH_REPO` and all seven `gh release` invocations execute within those environments.
- `actionlint` is not installed; GitHub Actions remains the authoritative semantic workflow validator.
- Aligned managed `sclaudex` with the current CLIProxyAPI runbook by enforcing `CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION=1000` in the child environment and strict private settings overlay.
- Added regression coverage proving the setting is required exactly, changed or missing overlay values fail closed, and stale duplicate inherited values are replaced by one authoritative managed value.
- Focused Task 26 verification passed: `go test ./internal/managedsettings ./internal/backend`.

## Current task

Add curated repository-portable `CLAUDE.md` and verification/review/honesty skills (Task 27), followed by the pinned OpenAI Codex plugin declaration before the one-shot final matrix.

## Remaining tasks

1. Add curated repository-portable `CLAUDE.md` and verification/review/honesty skills.
2. Declare and document the pinned `codex@openai-codex` project plugin without installing or invoking it.
3. Freeze the implementation and run the complete final verification matrix exactly once.
4. If the matrix passes, reconcile GitHub metadata, protect the `release` environment, add the authorized public deploy key, push `main`, and wait for CI.

## Key decisions and constraints

- `sclaude` routes to ordinary Claude Code/Anthropic; managed `sclaudex` keeps Claude Code as the harness and routes through CLIProxyAPI at `http://127.0.0.1:8317`.
- An existing external `claudex` remains opaque and is referenced only by a stable absolute path.
- Prompts/backend arguments exist only in private one-use launch files and are never persisted in session records.
- Automated verification uses temporary HOME/XDG roots and explicit localhost fixtures; it must not touch live OAuth, proxy, service, shell-profile, or credential state.
- Never read or modify `~/.codex/`, inspect Claude credential/keychain storage, expose secrets, log users out, or automatically run system-level `sudo`.
- Existing release assets must never be overwritten in place; fixes require a new release tag.
- Local commits and the final verified push of `main` are authorized. Do not create a tag or publish a release.
- Create a local commit after each tracked task is completed and update this file at each boundary.
- After the one-shot final matrix, only `STATUS.md` may change before its verification checkpoint commit.
- Do not run another adversarial review. Remaining ideas belong in the backlog below.

## Backlog

- Add local `actionlint` coverage in a future task if a trusted installation path is selected; do not block this task on installing it.
- Consider automating GitHub attestation verification in the installer in a future release, with a separately reviewed trust model.
